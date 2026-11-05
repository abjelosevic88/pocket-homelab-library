package server

// Improve the main library (Review page, admins): four jobs, each optional.
//   - tidy: "1. Izabrana" → "Izabrana" (1 kept as its number in the series),
//     ALL-CAPS titles and authors in normal case, "Author - Title" titles
//     without the author, "Last, First" authors as "First Last"; edited
//     fields are locked, so a rescan never undoes them;
//   - pdf_off: books with only a PDF kept off e-readers' catalogues;
//   - ask_ai: books that still look doubtful asked about (AI sorting);
//   - fill: details looked up for books missing a description or cover.

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

var numberedTitleRE = regexp.MustCompile(`^\s*(\d{1,3})\s*[.\-_)]\s*(\S.*)$`)

// lower words in titles (Serbian, Croatian, Bosnian, English), unless first
var smallWords = map[string]bool{"i": true, "u": true, "na": true, "od": true, "do": true, "za": true, "s": true, "sa": true, "o": true, "a": true, "ili": true,
	"iz": true, "po": true, "pod": true, "nad": true, "pred": true, "kroz": true, "bez": true, "kod": true, "the": true, "of": true, "and": true, "in": true,
	"to": true, "an": true, "on": true, "for": true, "with": true, "at": true, "by": true, "or": true, "da": true, "je": true, "se": true, "ni": true, "ne": true}

var romanRE = regexp.MustCompile(`^(?i)[ivxlcdm]+$`)

// shouting is mostly capital letters (a title or a name typed in caps).
func shouting(s string) bool {
	up, low := 0, 0
	for _, r := range s {
		if unicode.IsUpper(r) {
			up++
		} else if unicode.IsLower(r) {
			low++
		}
	}
	return up >= 5 && low == 0
}

// normalCase writes a shouted title or name the usual way: "DO VIĐENJA I HVALA" → "Do viđenja i hvala".
func normalCase(s string, title bool) string {
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		switch {
		case title && i > 0 && smallWords[w]:
		case romanRE.MatchString(w) && len(w) <= 4 && i > 0:
			words[i] = strings.ToUpper(w)
		case title && i > 0:
			// titles: only the first word capitalised (as Serbian and Croatian titles are written)
		default:
			r := []rune(w)
			r[0] = unicode.ToUpper(r[0])
			words[i] = string(r)
		}
	}
	return strings.Join(words, " ")
}

type tidyChange struct {
	BookID int64  `json:"book_id"`
	Field  string `json:"field"`
	From   string `json:"from"`
	To     string `json:"to"`
}

// tidyBook is what tidying would change in a book's details (nothing for a locked field).
func tidyBook(b *store.Book) (store.BookMeta, []tidyChange) {
	m := store.MetaOf(b)
	locked := map[string]bool{}
	for _, f := range b.Locked {
		locked[f] = true
	}
	var ch []tidyChange
	authors := []string{}
	for _, a := range b.Authors {
		authors = append(authors, a.Name)
	}
	if !locked["authors"] {
		out := make([]string, len(authors))
		changed := false
		for i, a := range authors {
			n := strings.TrimSpace(a)
			if last, first, ok := strings.Cut(n, ","); ok && !strings.ContainsAny(n, "@0123456789") && strings.Count(n, ",") == 1 &&
				strings.TrimSpace(first) != "" && len(strings.Fields(first)) <= 3 && len(strings.Fields(last)) <= 3 {
				n = strings.TrimSpace(first) + " " + strings.TrimSpace(last)
			}
			if shouting(n) {
				n = normalCase(n, false)
			}
			out[i] = n
			changed = changed || n != a
		}
		if changed {
			ch = append(ch, tidyChange{b.ID, "authors", strings.Join(authors, ", "), strings.Join(out, ", ")})
			m.Authors, authors = out, out
		}
	}
	if !locked["title"] {
		t := strings.TrimSpace(m.Title)
		orig := t
		if g := numberedTitleRE.FindStringSubmatch(t); g != nil && !strings.HasPrefix(strings.TrimSpace(g[2]), "-") {
			n, _ := strconv.Atoi(g[1])
			// "1984", "2010: Odiseja" are titles, not numbers: only short prefixes with a separator
			if n > 0 && n < 200 && !locked["series_index"] {
				t = strings.TrimSpace(g[2])
				if m.SeriesIndex == 0 {
					m.SeriesIndex = float64(n)
					ch = append(ch, tidyChange{b.ID, "series_index", "", g[1]})
				}
			}
		}
		for _, a := range authors {
			for _, form := range []string{a, reverseName(a)} {
				if p := form + " - "; len(t) > len(p)+2 && strings.EqualFold(fold(t[:min(len(t), len(p))]), fold(p)) {
					t = strings.TrimSpace(t[len(p):])
				}
			}
		}
		if shouting(t) {
			t = normalCase(t, true)
		}
		if t != orig && t != "" {
			ch = append(ch, tidyChange{b.ID, "title", orig, t})
			m.Title, m.SortTitle = t, ""
		}
	}
	return m, ch
}

// reverseName is "Last First" for "First Last".
func reverseName(n string) string {
	f := strings.Fields(n)
	if len(f) < 2 {
		return n
	}
	return f[len(f)-1] + " " + strings.Join(f[:len(f)-1], " ")
}

type improvePlan struct {
	Library string       `json:"library"`
	Tidy    int          `json:"tidy"`
	TidyEx  []tidyChange `json:"tidy_examples"`
	PDFOff  int          `json:"pdf_off"`
	AskAI   int          `json:"ask_ai"`
	AskEx   []string     `json:"ask_ai_examples"`
	Fill    int          `json:"fill"`
	ids     struct{ tidy, pdf, ask, fill []int64 }
	main    *store.Library
}

func (s *Server) planImprove(ctx context.Context) (*improvePlan, error) {
	main, _, _ := s.cleanupLibraries(ctx)
	if main == nil {
		return nil, fmt.Errorf("there is no library named “Books”")
	}
	p := &improvePlan{Library: main.Name, main: main}
	books, err := s.store.ReviewBooks(ctx, []int64{main.ID})
	if err != nil {
		return nil, err
	}
	people := authorBooks(books)
	for _, rb := range books {
		if f := badFlags(rb, people); len(f) > 0 {
			p.ids.ask = append(p.ids.ask, rb.ID)
			if len(p.AskEx) < 8 {
				p.AskEx = append(p.AskEx, rb.Title+" · "+strings.Join(rb.Authors, ", "))
			}
		}
		if !rb.EReaderOff && strings.Contains(rb.Formats, "pdf") && !strings.Contains(rb.Formats, "epub") {
			p.ids.pdf = append(p.ids.pdf, rb.ID)
		}
		if !rb.HasDesc || rb.Cover == "" {
			p.ids.fill = append(p.ids.fill, rb.ID)
		}
		// tidy candidates: cheap checks first, the full book only when something may change
		maybe := numberedTitleRE.MatchString(rb.Title) || shouting(rb.Title) || strings.Contains(rb.Title, " - ")
		for _, a := range rb.Authors {
			maybe = maybe || strings.Contains(a, ",") || shouting(a)
		}
		if maybe {
			if b, err := s.store.GetBookAny(ctx, rb.ID); err == nil {
				if _, ch := tidyBook(b); len(ch) > 0 {
					p.ids.tidy = append(p.ids.tidy, rb.ID)
					if len(p.TidyEx) < 10 {
						p.TidyEx = append(p.TidyEx, ch...)
					}
				}
			}
		}
	}
	p.Tidy, p.PDFOff, p.AskAI, p.Fill = len(p.ids.tidy), len(p.ids.pdf), len(p.ids.ask), len(p.ids.fill)
	return p, nil
}

type improveState struct {
	Running  bool     `json:"running"`
	Step     string   `json:"step"`
	Done     int      `json:"done"`
	Total    int      `json:"total"`
	Result   []string `json:"result"`
	Finished string   `json:"finished_at,omitempty"`
}

var (
	improveMu  sync.Mutex
	improveNow improveState
)

// GET /review/improve — Admins.
func (s *Server) handleImprovePlan(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	improveMu.Lock()
	st := improveNow
	improveMu.Unlock()
	if st.Running {
		writeJSON(w, http.StatusOK, map[string]any{"run": st})
		return
	}
	p, err := s.planImprove(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": err.Error(), "run": st})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plan": p, "run": st})
}

// POST /review/improve {"tidy","pdf_off","ask_ai","fill": bool} — Admins.
func (s *Server) handleImproveRun(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in struct {
		Tidy   bool `json:"tidy"`
		PDFOff bool `json:"pdf_off"`
		AskAI  bool `json:"ask_ai"`
		Fill   bool `json:"fill"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	p, err := s.planImprove(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	improveMu.Lock()
	if improveNow.Running {
		improveMu.Unlock()
		writeErr(w, http.StatusConflict, "already running")
		return
	}
	improveNow = improveState{Running: true, Step: "starting", Result: []string{}}
	improveMu.Unlock()
	uid := userFrom(r.Context()).ID
	go s.runImprove(p, in.Tidy, in.PDFOff, in.AskAI, in.Fill, uid)
	writeJSON(w, http.StatusAccepted, map[string]any{"run": improveNow})
}

func (s *Server) runImprove(p *improvePlan, tidy, pdfOff, askAI, fill bool, uid int64) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	step := func(name string, total int) {
		improveMu.Lock()
		improveNow.Step, improveNow.Done, improveNow.Total = name, 0, total
		improveMu.Unlock()
	}
	result := func(line string) {
		improveMu.Lock()
		improveNow.Result = append(improveNow.Result, line)
		improveMu.Unlock()
	}
	if tidy {
		step("tidy", len(p.ids.tidy))
		n := 0
		for _, id := range p.ids.tidy {
			if b, err := s.store.GetBookAny(ctx, id); err == nil {
				if m, ch := tidyBook(b); len(ch) > 0 {
					locked := map[string]bool{}
					for _, f := range b.Locked {
						locked[f] = true
					}
					for _, c := range ch {
						locked[c.Field] = true
					}
					lock := []string{}
					for f := range locked {
						lock = append(lock, f)
					}
					if s.store.SaveBookMeta(ctx, id, m) == nil && s.store.SetLocked(ctx, id, lock) == nil {
						n++
					}
				}
			}
			improveMu.Lock()
			improveNow.Done++
			improveMu.Unlock()
		}
		result(fmt.Sprintf("Tidied %d books", n))
	}
	if pdfOff && len(p.ids.pdf) > 0 {
		step("pdf_off", len(p.ids.pdf))
		if err := s.store.SetEReaderOff(ctx, p.ids.pdf, true); err == nil {
			result(fmt.Sprintf("Kept %d PDF-only books off e-readers", len(p.ids.pdf)))
		}
	}
	if askAI && len(p.ids.ask) > 0 {
		step("ask_ai", len(p.ids.ask))
		if err := s.store.QueueSort(ctx, p.ids.ask, "asked", uid, true); err == nil {
			s.kickSort()
			result(fmt.Sprintf("Asked Pocket AI about %d books (Review → AI sorting)", len(p.ids.ask)))
		}
	}
	if fill && len(p.ids.fill) > 0 {
		result(fmt.Sprintf("Looking up details for %d books in the background (Admin shows progress)", len(p.ids.fill)))
		go s.fetchMissing(context.Background(), p.main.ID, min(len(p.ids.fill), 5000))
	}
	improveMu.Lock()
	improveNow.Running, improveNow.Step, improveNow.Finished = false, "done", store.Now()
	improveMu.Unlock()
}
