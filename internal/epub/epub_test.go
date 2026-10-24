package epub

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func writeZip(t *testing.T, p string, files map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	w.Write([]byte("application/epub+zip"))
	// a folder entry with a (deflated, empty) body, as some tools write them
	if w, err := zw.CreateRaw(&zip.FileHeader{Name: "OPS/", Method: zip.Deflate, CompressedSize64: 2, CRC32: 0}); err == nil {
		w.Write([]byte{3, 0})
	}
	for n, body := range files {
		w, _ := zw.Create(n)
		w.Write([]byte(body))
	}
	zw.Close()
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

const container = `<?xml version="1.0"?><?xml version="1.0"?><container><rootfiles><rootfile full-path="OPS/book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`

const opf2 = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="BookId">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:title>Old title</dc:title>
    <dc:creator opf:role="aut">Old Author</dc:creator>
    <dc:creator opf:role="trl">Translator Person</dc:creator>
    <dc:identifier id="BookId" opf:scheme="uuid">abc-123</dc:identifier>
    <dc:identifier opf:scheme="ISBN">111</dc:identifier>
    <dc:language>Unknown</dc:language>
    <meta name="calibre:series" content="Old series"/>
    <meta name="cover" content="cov"/>
  </metadata>
  <manifest>
    <item id="cov" href="images/cover.png" media-type="image/png"/>
    <item id="c1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="c1"/></spine>
</package>`

func TestEmbedAndKindleFix(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.epub")
	writeZip(t, src, map[string]string{
		"META-INF/container.xml": container,
		"OPS/book.opf":           opf2,
		"OPS/images/cover.png":   "PNGDATA",
		"OPS/ch1.xhtml":          `<html><body id="start"><p>Hi <a href="ch1.xhtml#start">top</a></p><img alt="x"/><img src=""/><img src="a.jpg"/></body></html>`,
	})
	b, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	err = b.Embed(Meta{Title: "Na Drini ćuprija", Authors: []string{"Ivo Andrić"}, AuthorSorts: []string{"Andrić, Ivo"}, Series: "Bosnian trilogy", SeriesIndex: 1,
		Language: "", ISBN: "978-86-1234-567-8", Cover: []byte{0xff, 0xd8, 0xff, 1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.KindleFix("sr"); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.epub")
	if err := b.Write(out); err != nil {
		t.Fatal(err)
	}
	b.Close()

	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	if zr.File[0].Name != "mimetype" || zr.File[0].Method != zip.Store {
		t.Fatalf("first entry %s method %d", zr.File[0].Name, zr.File[0].Method)
	}
	get := func(n string) string {
		for _, f := range zr.File {
			if f.Name == n {
				rc, _ := f.Open()
				var buf bytes.Buffer
				buf.ReadFrom(rc)
				rc.Close()
				return buf.String()
			}
		}
		t.Fatalf("no %s", n)
		return ""
	}
	o := get("OPS/book.opf")
	for _, want := range []string{"<dc:title id=\"pl-title\">Na Drini ćuprija</dc:title>", `opf:file-as="Andrić, Ivo"`, ">Ivo Andrić</dc:creator>", "Translator Person",
		`opf:scheme="uuid">abc-123`, `opf:scheme="ISBN">9788612345678`, `name="calibre:series" content="Bosnian trilogy"`, `calibre:series_index" content="1"`,
		"<dc:language>sr</dc:language>", `content="pl-cover"`, `href="pocket-library-cover.jpg"`} {
		if !strings.Contains(o, want) {
			t.Errorf("opf lacks %q:\n%s", want, o)
		}
	}
	for _, gone := range []string{"Old title", "Old Author", ">111<", "Old series"} {
		if strings.Contains(o, gone) {
			t.Errorf("opf still has %q", gone)
		}
	}
	if get("OPS/pocket-library-cover.jpg") != "\xff\xd8\xff\x01" {
		t.Error("cover not added")
	}
	ch := get("OPS/ch1.xhtml")
	if !strings.HasPrefix(ch, `<?xml version="1.0" encoding="utf-8"?>`) || strings.Contains(ch, "#start") || strings.Count(ch, "<img") != 1 {
		t.Errorf("chapter not fixed: %s", ch)
	}
	if c := get("META-INF/container.xml"); strings.Count(c, "<?xml") != 1 {
		t.Errorf("container: %s", c)
	}
}

func TestEmbedEPUB3(t *testing.T) {
	data, err := Minimal("Test", "Someone", "en", "Hello.\n\nSecond.")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "a.epub")
	os.WriteFile(src, data, 0o644)
	b, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Embed(Meta{Title: "New", Authors: []string{"A One", "B Two"}, Series: "S", SeriesIndex: 2.5, Tags: []string{"x", "y"}, Description: "D & d"}); err != nil {
		t.Fatal(err)
	}
	o, _ := b.Read(b.OPFPath)
	s := string(o)
	for _, want := range []string{`<meta refines="#pl-creator2" property="role" scheme="marc:relators">aut</meta>`, `property="belongs-to-collection" id="pl-series">S</meta>`,
		`property="group-position">2.5</meta>`, "<dc:description>D &amp; d</dc:description>", "<dc:subject>y</dc:subject>", "dcterms:modified"} {
		if !strings.Contains(s, want) {
			t.Errorf("lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "Someone") {
		t.Error("old creator kept")
	}
	b.Close()
}

func TestNormalizeLanguage(t *testing.T) {
	for in, want := range map[string]string{"sr-Latn": "sr", "srp": "sr", "EN-us": "en", "Unknown": "", "xx": "xx", "ger": "de"} {
		if got := NormalizeLanguage(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

// TestRealBooks rewrites real EPUBs from a folder (PLB_REAL_EPUBS) into a
// temporary folder and checks the results open again; originals are only read.
func TestRealBooks(t *testing.T) {
	dir := os.Getenv("PLB_REAL_EPUBS")
	if dir == "" {
		t.Skip("set PLB_REAL_EPUBS to a folder of EPUBs")
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "*", "*.epub"))
	if len(paths) > 150 {
		paths = paths[:150]
	}
	out := t.TempDir()
	fixes, failed := 0, 0
	for i, p := range paths {
		b, err := Open(p)
		if err != nil {
			t.Logf("open %s: %v", filepath.Base(p), err)
			failed++
			continue
		}
		if err := b.Embed(Meta{Title: "T", Authors: []string{"A B"}, Series: "S", SeriesIndex: 1, Cover: []byte{0xff, 0xd8, 0xff}}); err != nil {
			t.Logf("embed %s: %v", filepath.Base(p), err)
		}
		if err := b.KindleFix("sr"); err != nil {
			t.Logf("fix %s: %v", filepath.Base(p), err)
		}
		fixes += len(b.Fixed)
		dst := filepath.Join(out, strconv.Itoa(i)+".epub")
		if err := b.Write(dst); err != nil {
			t.Errorf("write %s: %v", filepath.Base(p), err)
		}
		b.Close()
		if again, err := Open(dst); err != nil {
			t.Errorf("reopen %s: %v", filepath.Base(p), err)
		} else {
			again.Close()
		}
	}
	t.Logf("%d books, %d fixes, %d not EPUBs", len(paths), fixes, failed)
}
