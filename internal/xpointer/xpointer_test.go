package xpointer

import (
	"archive/zip"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// MakeEPUB writes a small EPUB with the given XHTML bodies as its spine.
func makeEPUB(t *testing.T, bodies ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "book.epub")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("mimetype")
	w.Write([]byte("application/epub+zip"))
	w, _ = zw.Create("META-INF/container.xml")
	w.Write([]byte(`<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`))
	var manifest, spine strings.Builder
	for i, body := range bodies {
		id := "c" + string(rune('a'+i))
		manifest.WriteString(`<item id="` + id + `" href="Text/` + id + `.xhtml" media-type="application/xhtml+xml"/>`)
		spine.WriteString(`<itemref idref="` + id + `"/>`)
		w, _ = zw.Create("OEBPS/Text/" + id + ".xhtml")
		w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?><!DOCTYPE html><html xmlns="http://www.w3.org/1999/xhtml"><head><title>T</title><style>p{}</style></head><body>` + body + `</body></html>`))
	}
	w, _ = zw.Create("OEBPS/content.opf")
	w.Write([]byte(`<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata/><manifest>` + manifest.String() + `</manifest><spine>` + spine.String() + `</spine></package>`))
	zw.Close()
	f.Close()
	return p
}

const chapter1 = `
  <h1>Chapter One</h1>
  <p>It was a bright cold day in April.</p>
  <p>The clocks were <i>striking</i> thirteen.</p>
  <div><p>Winston Smith slipped quickly.</p><p>Nothing &amp; nobody.<a id="x"/> More.</p></div>
`

func TestRoundTrip(t *testing.T) {
	p := makeEPUB(t, chapter1, `<p>Second chapter text here.</p>`)
	for _, frac := range []float64{0.05, 0.2, 0.37, 0.5, 0.66, 0.8, 0.95} {
		xp, err := Build(p, 0, frac)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(xp, "/body/DocFragment[1]/body/") || !strings.Contains(xp, "/text()[") {
			t.Fatalf("frac %v: odd pointer %q", frac, xp)
		}
		spine, got, err := Resolve(p, xp)
		if err != nil || spine != 0 {
			t.Fatalf("resolve %q: %v %v", xp, spine, err)
		}
		if math.Abs(got-frac) > 0.03 {
			t.Fatalf("frac %v → %q → %v", frac, xp, got)
		}
	}
	xp, _ := Build(p, 1, 0)
	if xp != "/body/DocFragment[2]/body" {
		t.Fatalf("start of item 2 = %q", xp)
	}
}

func TestCREStylePointers(t *testing.T) {
	p := makeEPUB(t, chapter1)
	cases := []struct {
		xp     string
		lo, hi float64
	}{
		{"/body/DocFragment[1]/body/h1/text().0", 0, 0.01},             // no indices at all (older CREngine)
		{"/body/DocFragment[1]/body/p[1]/text().3", 0.1, 0.3},          // "It was …"
		{"/body/DocFragment[1]/body/p[2]/i/text().0", 0.45, 0.6},       // inside the inline <i>
		{"/body/DocFragment[1]/body/p[2]/text()[2].1", 0.5, 0.65},      // the text after </i>
		{"/body/DocFragment[1]/body/div/p[2]/text().2", 0.8, 0.95},     // div without index
		{"/body/DocFragment[1]/body/div[1]/p[2]/text()[2].1", 0.93, 1}, // after the <a/> anchor
		{"/body/DocFragment[1]/body/div/p[9]/text().0", 0.6, 0.7},      // a p that isn't there → its div
		{"/body/DocFragment[1]/body/p[2]", 0.3, 0.4},                   // element pointer: its start
		{"/body/DocFragment[1]/body/p[2]/text()[1].999", 0.45, 0.55},   // offset past the end is clamped
	}
	for _, c := range cases {
		spine, frac, err := Resolve(p, c.xp)
		if err != nil || spine != 0 {
			t.Fatalf("%s: %v %v", c.xp, spine, err)
		}
		if frac < c.lo || frac > c.hi {
			t.Errorf("%s → %.3f, want %.2f..%.2f", c.xp, frac, c.lo, c.hi)
		}
	}
	if _, _, err := Resolve(p, "/body/DocFragment[7]/body/p"); err == nil {
		t.Fatal("a fragment past the spine should not resolve")
	}
	if _, _, err := Resolve(p, "#chapter"); err == nil {
		t.Fatal("garbage should not resolve")
	}
}

func TestOverallAndLocate(t *testing.T) {
	p := makeEPUB(t, `<p>`+strings.Repeat("a ", 100)+`</p>`, `<p>`+strings.Repeat("b ", 300)+`</p>`)
	o, err := Overall(p, 1, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(o-0.625) > 0.01 {
		t.Fatalf("overall = %v", o)
	}
	spine, frac, err := Locate(p, o)
	if err != nil || spine != 1 || math.Abs(frac-0.5) > 0.01 {
		t.Fatalf("locate = %d %v %v", spine, frac, err)
	}
}
