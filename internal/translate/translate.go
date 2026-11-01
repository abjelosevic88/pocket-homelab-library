// Package translate turns an English EPUB into a Serbian one, chapter by
// chapter, with a text model (Pocket AI through Pocket Core).
//
// The book is cut into block-level elements (paragraphs, headings, list
// items…), batched into requests of at most MaxChunk bytes of markup, and
// each reply must give back the same elements, in the same order, with only
// their text changed; it replaces exactly those bytes of the file. A reply
// that does not fit is asked again, then element by element, and only then
// left in English with a warning. Replies are kept on disk by request, so a
// stopped or retried job continues where it was without paying twice.
package translate

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/abjelosevic88/pocket-homelab-library/internal/epub"
)

// Model answers one request. jsonMode asks for a single JSON object.
type Model interface {
	Complete(ctx context.Context, system, prompt string, jsonMode bool) (string, error)
}

// Options are the reader's choices for one book.
type Options struct {
	Script string // "latin" (default) or "cyrillic"
	Notes  string // free-text instructions for the translator
	Model  string // only for the cache key: a different model asks again
}

// Progress is reported as the work goes on.
type Progress struct {
	Step    string // analyze, translate, assemble
	Units   int
	Chars   int
	Batches int
	Done    int
	Cached  int
	Active  int // requests out at the model right now
}

// Result describes the translated book.
type Result struct {
	Title, TitleTranslated, Author string
	Warnings                       []string
}

// Job runs one book.
type Job struct {
	Model    Model
	Options  Options
	CacheDir string // replies, kept between runs
	MaxChunk int    // bytes of markup per request (default 6000)
	Parallel int    // requests at once (default 3)
	Log      func(format string, a ...any)
	Progress func(Progress)
	Meta     func(title, author string) // the book's own title and author, once read

	mu       sync.Mutex
	prog     Progress
	warnings []string
	fails    atomic.Int32 // requests in a row that failed for the model's sake
}

// ErrUnavailable is returned when the model keeps failing: the job stops
// rather than leaving most of the book in English. Retrying continues.
var ErrUnavailable = errors.New("the model stopped answering")

// maxFailsInARow is how many requests may fail one after the other before the
// job gives up (Pocket AI down, the link switched off, the account limited).
const maxFailsInARow = 6

var scriptLabel = map[string]string{
	"latin":    "Serbian Latin script (latinica)",
	"cyrillic": "Serbian Cyrillic script (ćirilica)",
}

// ScriptLabel names a script choice for the prompt.
func ScriptLabel(s string) string {
	if l, ok := scriptLabel[s]; ok {
		return l
	}
	return scriptLabel["latin"]
}

func (j *Job) logf(format string, a ...any) {
	if j.Log != nil {
		j.Log(format, a...)
	}
}

func (j *Job) warn(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	j.logf("WARN %s", msg)
	j.mu.Lock()
	j.warnings = append(j.warnings, msg)
	j.mu.Unlock()
}

func (j *Job) report(f func(p *Progress)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	f(&j.prog)
	if j.Progress != nil {
		j.Progress(j.prog) // under the lock, so reports arrive in order
	}
}

type doc struct {
	name    string
	kind    string // xhtml or ncx
	data    []byte
	batches [][]*node
	edits   []edit
}

// Run translates the EPUB at src into dst.
func (j *Job) Run(ctx context.Context, src, dst string) (*Result, error) {
	if j.MaxChunk <= 0 {
		j.MaxChunk = 6000
	}
	if j.Parallel <= 0 {
		j.Parallel = 3
	}
	if err := os.MkdirAll(j.CacheDir, 0o755); err != nil {
		return nil, err
	}
	b, err := epub.Open(src)
	if err != nil {
		return nil, err
	}
	defer b.Close()

	j.report(func(p *Progress) { p.Step = "analyze" })
	opf, err := b.Read(b.OPFPath)
	if err != nil {
		return nil, err
	}
	if j.Meta != nil {
		root, _ := parse(opf)
		title, author := "", ""
		if ts := root.find("title"); len(ts) > 0 {
			title = innerText(opf, ts[0])
		}
		for _, c := range root.find("creator") {
			if author = innerText(opf, c); author != "" {
				break
			}
		}
		j.Meta(title, author)
	}
	docs, err := j.plan(b, opf)
	if err != nil {
		return nil, err
	}
	type task struct {
		d     *doc
		i     int
		batch []*node
	}
	var tasks []task
	units, chars := 0, 0
	for _, d := range docs {
		for i, bt := range d.batches {
			tasks = append(tasks, task{d, i, bt})
			units += len(bt)
			for _, n := range bt {
				chars += int(n.size())
			}
		}
	}
	if len(tasks) == 0 {
		return nil, errNoText
	}
	j.logf("%d documents, %d blocks, %d characters of markup, %d requests", len(docs), units, chars, len(tasks))
	j.report(func(p *Progress) {
		*p = Progress{Step: "translate", Units: units, Chars: chars, Batches: len(tasks)}
	})

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	work := make(chan task)
	var wg sync.WaitGroup
	for w := 0; w < j.Parallel; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range work {
				if ctx.Err() != nil {
					continue
				}
				j.report(func(p *Progress) { p.Active++ })
				edits, cached, err := j.batch(ctx, t.d, t.i, t.batch)
				j.report(func(p *Progress) { p.Active-- })
				if err != nil {
					cancel(err)
					continue
				}
				j.mu.Lock()
				t.d.edits = append(t.d.edits, edits...)
				j.mu.Unlock()
				j.report(func(p *Progress) {
					p.Done++
					if cached {
						p.Cached++
					}
				})
			}
		}()
	}
feed:
	for _, t := range tasks {
		select {
		case work <- t:
		case <-ctx.Done():
			break feed
		}
	}
	close(work)
	wg.Wait()
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}

	j.report(func(p *Progress) { p.Step = "assemble" })
	for _, d := range docs {
		if len(d.edits) > 0 {
			b.Put(d.name, splice(d.data, d.edits))
		}
	}
	res := &Result{}
	newOPF, err := j.fixOPF(ctx, opf, res)
	if err != nil {
		return nil, err
	}
	b.Put(b.OPFPath, newOPF)
	if err := b.Write(dst); err != nil {
		return nil, err
	}
	j.mu.Lock()
	res.Warnings = append([]string(nil), j.warnings...)
	j.mu.Unlock()
	return res, nil
}

// plan reads the spine's documents and the table of contents into batches.
func (j *Job) plan(b *epub.Book, opf []byte) ([]*doc, error) {
	root, _ := parse(opf)
	base := path.Dir(b.OPFPath)
	if base == "." {
		base = ""
	}
	var docs []*doc
	seen := map[string]bool{}
	for _, it := range root.find("item") {
		href, mt := it.attrs["href"], it.attrs["media-type"]
		kind := ""
		switch mt {
		case "application/xhtml+xml":
			kind = "xhtml"
		case "application/x-dtbncx+xml":
			kind = "ncx"
		default:
			continue
		}
		name := path.Join(base, unescapeHref(strings.SplitN(href, "#", 2)[0]))
		if seen[name] {
			continue
		}
		seen[name] = true
		data, err := b.Read(name)
		if err != nil {
			continue
		}
		tree, err := parse(data)
		if err != nil {
			j.warn("%s: left in English, it is not readable XHTML (%v)", path.Base(name), err)
			continue
		}
		var units []*node
		if kind == "xhtml" {
			if body := tree.find("body"); len(body) > 0 {
				units = blocks(body[0], int64(j.MaxChunk), nil)
			}
		} else {
			for _, t := range tree.find("text") {
				if t.text > 0 {
					units = append(units, t)
				}
			}
		}
		if len(units) == 0 {
			continue
		}
		docs = append(docs, &doc{name: name, kind: kind, data: data, batches: batchUnits(units, int64(j.MaxChunk))})
	}
	return docs, nil
}

func unescapeHref(h string) string {
	if u, err := url.PathUnescape(h); err == nil {
		return u
	}
	return h
}

func batchUnits(units []*node, max int64) [][]*node {
	var out [][]*node
	var cur []*node
	var size int64
	for _, u := range units {
		if len(cur) > 0 && size+u.size() > max {
			out = append(out, cur)
			cur, size = nil, 0
		}
		cur = append(cur, u)
		size += u.size()
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

func (j *Job) system(kind string, n int) string {
	what := "XHTML fragments from an EPUB"
	if kind == "ncx" {
		what = "table-of-contents label elements from an EPUB NCX file"
	}
	rules := []string{
		fmt.Sprintf("You are an expert literary translator. Translate the human-readable text in the %s you are given from English into Serbian, %s, standard ekavian.", what, ScriptLabel(j.Options.Script)),
		"Preserve ALL markup exactly: every tag, attribute, id, class, href, style and entity stays byte-identical; only text content changes. Never add, drop, merge or reorder elements.",
		fmt.Sprintf("The input has exactly %d top-level element(s). Output exactly %d top-level element(s), in the same order, one per input element, and nothing else.", n, n),
		"Do not output explanations, code fences, an XML declaration or a DOCTYPE. Output only the translated fragments.",
		"Translate naturally and idiomatically, keeping the author's tone, register and paragraph structure. Personal names follow standard Serbian transcription conventions; keep URLs, code, ISBNs and numbers unchanged.",
		"If an element contains no translatable text (e.g. only an image or a number) return it unchanged.",
	}
	if notes := strings.TrimSpace(j.Options.Notes); notes != "" {
		rules = append(rules, "Additional instructions from the reader (follow them): "+notes)
	}
	return "- " + strings.Join(rules, "\n- ")
}

// batch translates one batch and returns its edits; a batch that cannot be
// translated is left as it is (with a warning), only a model that stopped
// answering is an error.
func (j *Job) batch(ctx context.Context, d *doc, i int, batch []*node) ([]edit, bool, error) {
	label := fmt.Sprintf("%s#%d", path.Base(d.name), i)
	frags := make([][]byte, len(batch))
	names := make([]string, len(batch))
	for k, n := range batch {
		frags[k] = d.data[n.start:n.end]
		names[k] = n.name
	}
	out, cached, err := j.translate(ctx, d.kind, frags, names, label)
	if err == nil {
		return toEdits(batch, out), cached, nil
	}
	if !errors.Is(err, errBadReply) {
		return nil, false, err
	}
	if len(batch) == 1 {
		j.warn("%s: left in English (%v)", label, err)
		return nil, false, nil
	}
	j.logf("%s: asking element by element", label)
	var edits []edit
	for k, n := range batch {
		one, _, err := j.translate(ctx, d.kind, frags[k:k+1], names[k:k+1], fmt.Sprintf("%s.%d", label, k))
		if errors.Is(err, errBadReply) {
			j.warn("%s: one paragraph left in English (%v)", label, err)
			continue
		}
		if err != nil {
			return nil, false, err
		}
		edits = append(edits, edit{start: n.start, end: n.end, with: one[0]})
	}
	return edits, false, nil
}

func toEdits(batch []*node, out [][]byte) []edit {
	e := make([]edit, len(batch))
	for k, n := range batch {
		e[k] = edit{start: n.start, end: n.end, with: out[k]}
	}
	return e
}

// errBadReply wraps a reply that came back but does not fit the input.
var errBadReply = errors.New("the reply does not match the input")

type badReply struct{ err error }

func (b badReply) Error() string        { return b.err.Error() }
func (b badReply) Is(target error) bool { return target == errBadReply }

// translate asks twice for a reply that fits.
func (j *Job) translate(ctx context.Context, kind string, frags [][]byte, names []string, label string) ([][]byte, bool, error) {
	system := j.system(kind, len(frags))
	prompt := "INPUT:\n" + string(joinBytes(frags)) + "\n"
	var last error
	for attempt := 1; attempt <= 2; attempt++ {
		reply, cached, err := j.ask(ctx, system, prompt, false, attempt > 1)
		if err != nil {
			return nil, false, err
		}
		out, err := fragments(reply, names)
		if err == nil {
			return out, cached, nil
		}
		last = err
		j.logf("%s: attempt %d does not fit: %v", label, attempt, err)
	}
	return nil, false, badReply{last}
}

func joinBytes(parts [][]byte) []byte {
	var n int
	for _, p := range parts {
		n += len(p) + 1
	}
	out := make([]byte, 0, n)
	for i, p := range parts {
		if i > 0 {
			out = append(out, '\n')
		}
		out = append(out, p...)
	}
	return out
}

// ask returns the cached reply to a request, or asks the model. A busy
// model is waited for; other failures are tried three times, and too many
// in a row anywhere in the job stop it.
func (j *Job) ask(ctx context.Context, system, prompt string, jsonMode, fresh bool) (string, bool, error) {
	sum := sha256.Sum256([]byte(j.Options.Model + "\x00" + system + "\x00" + prompt))
	file := filepath.Join(j.CacheDir, hex.EncodeToString(sum[:])+".txt")
	if !fresh {
		if b, err := os.ReadFile(file); err == nil {
			return string(b), true, nil
		}
	}
	var lastErr error
	for try, busy := 0, 0; try < 3; {
		if err := ctx.Err(); err != nil {
			return "", false, context.Cause(ctx)
		}
		text, err := j.Model.Complete(ctx, system, prompt, jsonMode)
		if err == nil {
			j.fails.Store(0)
			_ = os.WriteFile(file, []byte(text), 0o644)
			return text, false, nil
		}
		if ctx.Err() != nil {
			return "", false, context.Cause(ctx)
		}
		lastErr = err
		wait := time.Duration(try+1) * 10 * time.Second
		switch {
		case isFatal(err):
			return "", false, fmt.Errorf("%w: %v", ErrUnavailable, err)
		case isBusy(err) && busy < 60:
			busy++
			wait = 10 * time.Second
		default:
			try++
			if j.fails.Add(1) >= maxFailsInARow {
				return "", false, fmt.Errorf("%w: %v", ErrUnavailable, err)
			}
			j.logf("request failed (%v), trying again", err)
		}
		select {
		case <-ctx.Done():
			return "", false, context.Cause(ctx)
		case <-time.After(wait):
		}
	}
	return "", false, fmt.Errorf("%w: %v", ErrUnavailable, lastErr)
}

func isBusy(err error) bool { return strings.Contains(err.Error(), "HTTP 429") }

// isFatal: the link is off or refused, no point asking again.
func isFatal(err error) bool {
	s := err.Error()
	return strings.Contains(s, "HTTP 401") || strings.Contains(s, "HTTP 403") ||
		strings.Contains(s, "enable this app's Pocket AI") || strings.Contains(s, "connect this app to Pocket Core")
}

// fixOPF translates the title and description, sets the language and gives
// the book a new identifier so it is a book of its own.
func (j *Job) fixOPF(ctx context.Context, opf []byte, res *Result) ([]byte, error) {
	root, _ := parse(opf)
	var edits []edit
	titles := root.find("title")
	if len(titles) > 0 {
		res.Title = innerText(opf, titles[0])
	}
	for _, c := range root.find("creator") {
		if t := innerText(opf, c); t != "" {
			res.Author = t
			break
		}
	}
	descs := root.find("description")
	desc := ""
	if len(descs) > 0 {
		desc = innerText(opf, descs[0])
	}
	res.TitleTranslated = res.Title
	if res.Title != "" || desc != "" {
		title, description, err := j.translateMeta(ctx, res.Title, desc)
		switch {
		case errors.Is(err, ErrUnavailable) || ctx.Err() != nil:
			return nil, err
		case err != nil:
			j.warn("title not translated (%v)", err)
		default:
			if title != "" && len(titles) > 0 {
				res.TitleTranslated = title
				edits = append(edits, edit{titles[0].innerStart, titles[0].innerEnd, []byte(escapeText(title))})
			}
			if description != "" && len(descs) > 0 {
				edits = append(edits, edit{descs[0].innerStart, descs[0].innerEnd, []byte(escapeText(description))})
			}
		}
	}
	langs := root.find("language")
	for _, l := range langs {
		edits = append(edits, edit{l.innerStart, l.innerEnd, []byte("sr")})
	}
	if len(langs) == 0 {
		if md := root.find("metadata"); len(md) > 0 {
			edits = append(edits, edit{md[0].innerEnd, md[0].innerEnd, []byte("<dc:language>sr</dc:language>\n")})
		}
	}
	uid := ""
	if pk := root.find("package"); len(pk) > 0 {
		uid = pk[0].attrs["unique-identifier"]
	}
	for _, id := range root.find("identifier") {
		if uid == "" || id.attrs["id"] == uid {
			edits = append(edits, edit{id.innerStart, id.innerEnd, []byte("urn:uuid:" + newUUID())})
			break
		}
	}
	return splice(opf, edits), nil
}

var htmlTagRe = regexp.MustCompile(`<[a-zA-Z/][^>]*>`)

func (j *Job) translateMeta(ctx context.Context, title, desc string) (string, string, error) {
	if len(desc) > 6000 {
		desc = ""
	}
	in, _ := json.Marshal(map[string]string{"title": title, "description": desc})
	system := fmt.Sprintf("You translate book metadata from English into Serbian, %s, standard ekavian. "+
		`Return {"title": "...", "description": "..."}: the title as a Serbian edition would print it, and the description translated in full (keep any HTML tags exactly). An empty field stays empty.`, ScriptLabel(j.Options.Script))
	if notes := strings.TrimSpace(j.Options.Notes); notes != "" {
		system += " Additional instructions from the reader (follow them): " + notes
	}
	reply, _, err := j.ask(ctx, system, string(in), true, false)
	if err != nil {
		return "", "", err
	}
	var out struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(cleanReply(reply)), &out); err != nil {
		return "", "", fmt.Errorf("unreadable reply: %w", err)
	}
	out.Title = strings.Trim(strings.TrimSpace(out.Title), `"“”„`)
	if strings.Contains(out.Title, "\n") || len(out.Title) > 4*len(title)+40 {
		out.Title = ""
	}
	if desc == "" || (htmlTagRe.MatchString(out.Description) && !htmlTagRe.MatchString(desc)) {
		out.Description = ""
	}
	return out.Title, strings.TrimSpace(out.Description), nil
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
