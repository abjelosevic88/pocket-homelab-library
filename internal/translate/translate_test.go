package translate

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
)

func writeEPUB(t *testing.T, p string, files map[string]string) {
	t.Helper()
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	w, _ := z.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	w.Write([]byte("application/epub+zip"))
	for name, body := range files {
		w, _ := z.Create(name)
		w.Write([]byte(body))
	}
	z.Close()
	f.Close()
}

func readEntry(t *testing.T, p, name string) string {
	t.Helper()
	z, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	for _, f := range z.File {
		if f.Name == name {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			return string(b)
		}
	}
	t.Fatalf("%s missing", name)
	return ""
}

var (
	textRe   = regexp.MustCompile(`>([^<]+)<`)
	entityRe = regexp.MustCompile(`&[^;\s]+;|[^&]+|&`)
)

// upperText upper-cases text but leaves entities alone.
func upperText(s string) string {
	return entityRe.ReplaceAllStringFunc(s, func(p string) string {
		if strings.HasPrefix(p, "&") && len(p) > 1 {
			return p
		}
		return strings.ToUpper(p)
	})
}

// upper "translates" by upper-casing the text between tags.
type upper struct {
	calls   atomic.Int32
	breakIt func(prompt string) bool // reply with one element too few
	err     error
}

func (u *upper) Complete(_ context.Context, system, prompt string, jsonMode bool) (string, error) {
	u.calls.Add(1)
	if u.err != nil {
		return "", u.err
	}
	if jsonMode {
		var in map[string]string
		_ = json.Unmarshal([]byte(prompt), &in)
		b, _ := json.Marshal(map[string]string{"title": "PREVOD " + in["title"], "description": strings.ToUpper(in["description"])})
		return string(b), nil
	}
	body := strings.TrimPrefix(prompt, "INPUT:\n")
	out := textRe.ReplaceAllStringFunc(body, upperText)
	if u.breakIt != nil && u.breakIt(prompt) {
		out = out[:strings.LastIndex(strings.TrimRight(out, "\n"), "\n")+1] // drop the last element
	}
	return "```xhtml\n" + out + "```", nil
}

const opf = `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>The Three Black Pennys</dc:title>
    <dc:creator>Joseph Hergesheimer</dc:creator>
    <dc:language>en</dc:language>
    <dc:identifier id="uid">urn:isbn:123</dc:identifier>
    <dc:description>&lt;p&gt;A family saga.&lt;/p&gt;</dc:description>
  </metadata>
  <manifest>
    <item id="c1" href="Text/ch%201.xhtml" media-type="application/xhtml+xml"/>
    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
    <item id="css" href="style.css" media-type="text/css"/>
  </manifest>
</package>`

const chapter = `<?xml version="1.0" encoding="utf-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<head><title>Chapter</title></head>
<body>
  <div class="chapter">
    <h1 id="c1">Chapter One</h1>
    <p class="first">It was a&nbsp;dark <i>and</i> stormy night.</p>
    <p><img src="a.png" alt="x"/></p>
    <p>She said &#8220;no&#8221;.<br/>Then left.</p>
  </div>
  <script>var x = "do not touch";</script>
</body>
</html>`

const ncx = `<?xml version="1.0"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/"><navMap>
<navPoint id="n1"><navLabel><text>Chapter One</text></navLabel><content src="Text/ch%201.xhtml"/></navPoint>
</navMap></ncx>`

func book(t *testing.T) (string, string) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.epub")
	writeEPUB(t, src, map[string]string{
		"META-INF/container.xml": `<container><rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles></container>`,
		"OEBPS/content.opf":      opf,
		"OEBPS/Text/ch 1.xhtml":  chapter,
		"OEBPS/toc.ncx":          ncx,
		"OEBPS/style.css":        "p { margin: 0 }",
	})
	return dir, src
}

func TestTranslateBook(t *testing.T) {
	dir, src := book(t)
	m := &upper{}
	var last Progress
	j := &Job{Model: m, CacheDir: filepath.Join(dir, "cache"), MaxChunk: 80, Progress: func(p Progress) { last = p }}
	dst := filepath.Join(dir, "out.epub")
	res, err := j.Run(context.Background(), src, dst)
	if err != nil {
		t.Fatal(err)
	}
	ch := readEntry(t, dst, "OEBPS/Text/ch 1.xhtml")
	for _, want := range []string{
		`<h1 id="c1">CHAPTER ONE</h1>`,
		`<p class="first">IT WAS A&nbsp;DARK <i>AND</i> STORMY NIGHT.</p>`,
		`<p>SHE SAID &#8220;NO&#8221;.<br/>THEN LEFT.</p>`,
		`<p><img src="a.png" alt="x"/></p>`, // no text: not sent
		`<script>var x = "do not touch";</script>`,
		"<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<!DOCTYPE html>", // untouched around the blocks
	} {
		if !strings.Contains(ch, want) {
			t.Errorf("chapter lacks %q:\n%s", want, ch)
		}
	}
	if !strings.Contains(readEntry(t, dst, "OEBPS/toc.ncx"), "<text>CHAPTER ONE</text>") {
		t.Error("toc label not translated")
	}
	if readEntry(t, dst, "OEBPS/style.css") != "p { margin: 0 }" {
		t.Error("css changed")
	}
	o := readEntry(t, dst, "OEBPS/content.opf")
	for _, want := range []string{"<dc:title>PREVOD The Three Black Pennys</dc:title>", "<dc:language>sr</dc:language>", `<dc:identifier id="uid">urn:uuid:`, "&lt;P&gt;A FAMILY SAGA.&lt;/P&gt;"} {
		if !strings.Contains(o, want) {
			t.Errorf("opf lacks %q:\n%s", want, o)
		}
	}
	if res.Title != "The Three Black Pennys" || res.TitleTranslated != "PREVOD The Three Black Pennys" || res.Author != "Joseph Hergesheimer" {
		t.Errorf("result %+v", res)
	}
	if last.Done != last.Batches || last.Batches < 3 || last.Cached != 0 {
		t.Errorf("progress %+v", last)
	}

	// a second run is answered from the cache
	before := m.calls.Load()
	j2 := &Job{Model: m, CacheDir: filepath.Join(dir, "cache"), MaxChunk: 80, Progress: func(p Progress) { last = p }}
	if _, err := j2.Run(context.Background(), src, filepath.Join(dir, "out2.epub")); err != nil {
		t.Fatal(err)
	}
	if m.calls.Load() != before || last.Cached != last.Batches {
		t.Errorf("second run asked again: %d calls, %+v", m.calls.Load()-before, last)
	}
}

func TestBadRepliesFallBackToOneByOne(t *testing.T) {
	dir, src := book(t)
	// replies to multi-element requests lose an element; single ones are fine
	m := &upper{breakIt: func(p string) bool { return strings.Count(p, "\n<") > 1 }}
	j := &Job{Model: m, CacheDir: filepath.Join(dir, "cache"), MaxChunk: 150}
	dst := filepath.Join(dir, "out.epub")
	res, err := j.Run(context.Background(), src, dst)
	if err != nil {
		t.Fatal(err)
	}
	ch := readEntry(t, dst, "OEBPS/Text/ch 1.xhtml")
	if !strings.Contains(ch, "STORMY NIGHT") || !strings.Contains(ch, "CHAPTER ONE") {
		t.Errorf("not translated one by one:\n%s", ch)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings %v", res.Warnings)
	}
}

func TestModelDownStopsTheJob(t *testing.T) {
	dir, src := book(t)
	m := &upper{err: errors.New("Pocket AI HTTP 403: Pocket AI text-generation link rejected")}
	j := &Job{Model: m, CacheDir: filepath.Join(dir, "cache"), MaxChunk: 80}
	_, err := j.Run(context.Background(), src, filepath.Join(dir, "out.epub"))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out.epub")); err == nil {
		t.Error("wrote a book anyway")
	}
}

func TestFragments(t *testing.T) {
	out, err := fragments("Here you go:\n<p>a&nbsp;b</p>\n<h2 x=\"1\">c</h2>", []string{"p", "h2"})
	if err != nil || len(out) != 2 || string(out[0]) != "<p>a&nbsp;b</p>" || string(out[1]) != `<h2 x="1">c</h2>` {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := fragments("<p>a</p><p>b</p>", []string{"p"}); err == nil {
		t.Error("count not checked")
	}
	if _, err := fragments("<h2>a</h2>", []string{"p"}); err == nil {
		t.Error("names not checked")
	}
	if _, err := fragments("<p>a<p>b</p>", []string{"p"}); err == nil {
		t.Error("broken markup accepted")
	}
}
