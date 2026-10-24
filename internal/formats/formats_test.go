package formats

import (
	"archive/zip"
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 60))
	for x := 0; x < 40; x++ {
		for y := 0; y < 60; y++ {
			img.Set(x, y, color.RGBA{200, 30, 60, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func writeZip(t *testing.T, path string, files map[string][]byte, order ...string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	if order == nil {
		for k := range files {
			order = append(order, k)
		}
	}
	for _, name := range order {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(files[name])
	}
	zw.Close()
	f.Close()
}

// MakeTestEPUB builds a small EPUB 3 with a cover, series and two chapters.
func makeEPUB(t *testing.T, path string, cover []byte) {
	opf := `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
 <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
  <dc:identifier id="uid">urn:uuid:1234-5678</dc:identifier>
  <dc:identifier>urn:isbn:9780306406157</dc:identifier>
  <dc:title id="t1">Na Drini ćuprija</dc:title>
  <meta refines="#t1" property="title-type">main</meta>
  <dc:title id="t2">Višegradska hronika</dc:title>
  <meta refines="#t2" property="title-type">subtitle</meta>
  <dc:creator id="c1">Ivo Andrić</dc:creator>
  <meta refines="#c1" property="role" scheme="marc:relators">aut</meta>
  <dc:creator id="c2">Some Translator</dc:creator>
  <meta refines="#c2" property="role" scheme="marc:relators">trl</meta>
  <dc:language>srp</dc:language>
  <dc:publisher>Prosveta</dc:publisher>
  <dc:date>1945-03-01T00:00:00+00:00</dc:date>
  <dc:subject>Roman</dc:subject>
  <dc:description>&lt;p&gt;A bridge.&lt;/p&gt;</dc:description>
  <meta property="belongs-to-collection" id="col">Bosanska trilogija</meta>
  <meta refines="#col" property="collection-type">series</meta>
  <meta refines="#col" property="group-position">1</meta>
 </metadata>
 <manifest>
  <item id="c1x" href="text/ch1.xhtml" media-type="application/xhtml+xml"/>
  <item id="c2x" href="text/ch2.xhtml" media-type="application/xhtml+xml"/>
  <item id="cov" href="images/cover.png" media-type="image/png" properties="cover-image"/>
 </manifest>
 <spine><itemref idref="c1x"/><itemref idref="c2x"/></spine>
</package>`
	ch := `<html><head><title>x y z</title><style>p{}</style></head><body><p>Jedan dva tri četiri&nbsp;pet.</p><p>Šest sedam.</p></body></html>`
	writeZip(t, path, map[string][]byte{
		"mimetype":               []byte("application/epub+zip"),
		"META-INF/container.xml": []byte(`<container><rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles></container>`),
		"OEBPS/content.opf":      []byte(opf),
		"OEBPS/text/ch1.xhtml":   []byte(ch),
		"OEBPS/text/ch2.xhtml":   []byte(ch),
		"OEBPS/images/cover.png": cover,
	}, "mimetype", "META-INF/container.xml", "OEBPS/content.opf", "OEBPS/text/ch1.xhtml", "OEBPS/text/ch2.xhtml", "OEBPS/images/cover.png")
}

func TestEPUB(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "book.epub")
	cover := tinyPNG(t)
	makeEPUB(t, p, cover)
	m, err := Read(p, true)
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "Na Drini ćuprija" || m.Subtitle != "Višegradska hronika" {
		t.Errorf("title %q / %q", m.Title, m.Subtitle)
	}
	if len(m.Authors) != 1 || m.Authors[0] != "Ivo Andrić" {
		t.Errorf("authors %v", m.Authors)
	}
	if m.Series != "Bosanska trilogija" || m.SeriesIndex != 1 {
		t.Errorf("series %q %v", m.Series, m.SeriesIndex)
	}
	if m.ISBN != "9780306406157" || m.Identifiers["uuid"] != "1234-5678" {
		t.Errorf("ids %q %v", m.ISBN, m.Identifiers)
	}
	if m.Language != "sr" || m.Published != "1945-03-01" || m.Publisher != "Prosveta" {
		t.Errorf("lang %q date %q pub %q", m.Language, m.Published, m.Publisher)
	}
	if m.Words != 14 {
		t.Errorf("words %d, want 14", m.Words)
	}
	if !bytes.Equal(m.Cover, cover) {
		t.Errorf("cover not found")
	}
	if Detect(p) != "epub" || Detect("x.kepub.epub") != "kepub" || Detect("x.fb2.zip") != "fbz" || Detect("x.opf") != "" {
		t.Error("detect")
	}
	if BaseName("/a/B c.kepub.epub") != "B c" {
		t.Error("basename")
	}
}

func TestCalibreSidecar(t *testing.T) {
	opf := `<?xml version='1.0' encoding='utf-8'?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="uuid_id" version="2.0">
    <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
        <dc:identifier opf:scheme="calibre" id="calibre_id">876</dc:identifier>
        <dc:identifier opf:scheme="uuid" id="uuid_id">41d0cbd2-97b2-499a-b65d-363c116a6b94</dc:identifier>
        <dc:title>Bijela kao mlijeko, crvena kao krv</dc:title>
        <dc:creator opf:file-as="D'Avenia, Alessandro" opf:role="aut">Alessandro D'Avenia</dc:creator>
        <dc:contributor opf:file-as="calibre" opf:role="bkp">calibre (9.1.0)</dc:contributor>
        <dc:date>0101-01-01T00:00:00+00:00</dc:date>
        <dc:identifier opf:scheme="MOBI-ASIN">e40bb88b-6010-435d-9962-8d2348ff6c94</dc:identifier>
        <dc:language>srp</dc:language>
        <meta name="calibre:series" content="Trilogija"/>
        <meta name="calibre:series_index" content="2.0"/>
        <meta name="calibre:title_sort" content="Bijela kao mlijeko"/>
    </metadata>
    <guide/>
</package>`
	m, err := ReadOPF(strings.NewReader(opf))
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "Bijela kao mlijeko, crvena kao krv" || len(m.Authors) != 1 || m.Authors[0] != "Alessandro D'Avenia" {
		t.Errorf("%q %v", m.Title, m.Authors)
	}
	if m.Identifiers["calibre"] != "876" || m.Identifiers["uuid"] == "" || m.Identifiers["asin"] != "" {
		t.Errorf("ids %v", m.Identifiers)
	}
	if m.Published != "" || m.Language != "sr" || m.Series != "Trilogija" || m.SeriesIndex != 2 || m.SortTitle != "Bijela kao mlijeko" {
		t.Errorf("%q %q %q %v %q", m.Published, m.Language, m.Series, m.SeriesIndex, m.SortTitle)
	}
}

func TestComicCBZ(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "Dylan Dog 001.cbz")
	pg := tinyPNG(t)
	ci := `<?xml version="1.0"?><ComicInfo><Series>Dylan Dog</Series><Number>1</Number><Title>L'alba dei morti viventi</Title><Writer>Tiziano Sclavi, Other</Writer><Year>1986</Year><Month>10</Month><Manga>YesAndRightToLeft</Manga><Genre>Horror</Genre><Pages><Page Image="1" Type="FrontCover"/></Pages></ComicInfo>`
	writeZip(t, p, map[string][]byte{"10.png": pg, "2.png": pg, "1.png": append(append([]byte{}, pg...), 0), "__MACOSX/._1.png": []byte("x"), "ComicInfo.xml": []byte(ci)})
	m, err := Read(p, true)
	if err != nil {
		t.Fatal(err)
	}
	if m.Series != "Dylan Dog" || m.SeriesIndex != 1 || m.Pages != 3 || !m.Manga || !m.RTL || m.Published != "1986-10-01" {
		t.Errorf("%+v", m)
	}
	if len(m.Authors) != 2 || m.Authors[0] != "Tiziano Sclavi" {
		t.Errorf("authors %v", m.Authors)
	}
	if !bytes.Equal(m.Cover, pg) { // image 1 = "2.png" in natural order
		t.Errorf("front cover page not used")
	}
	c, err := OpenComic(p, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	names := []string{}
	for _, pg := range c.Pages() {
		names = append(names, pg.Name)
	}
	if strings.Join(names, ",") != "1.png,2.png,10.png" {
		t.Errorf("order %v", names)
	}
	rc, mime, err := c.Page(2)
	if err != nil || mime != "image/png" {
		t.Fatal(err, mime)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(b, pg) {
		t.Error("page bytes differ")
	}
}

func TestFB2(t *testing.T) {
	cover := tinyPNG(t)
	doc := `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0" xmlns:l="http://www.w3.org/1999/xlink">
<description><title-info><genre>sf</genre><author><first-name>Arkadij</first-name><last-name>Strugacki</last-name></author>
<book-title>Piknik pored puta</book-title><annotation><p>Zona.</p></annotation><lang>sr</lang><sequence name="Svijet podne" number="3"/>
<coverpage><image l:href="#c.png"/></coverpage></title-info><publish-info><publisher>Laguna</publisher><year>2010</year></publish-info></description>
<body><section><p>Prva rečenica ovde.</p></section></body>
<binary id="c.png" content-type="image/png">` + base64.StdEncoding.EncodeToString(cover) + `</binary></FictionBook>`
	m, err := readFB2(strings.NewReader(doc), true)
	if err != nil {
		t.Fatal(err)
	}
	m.tidy()
	if m.Title != "Piknik pored puta" || m.Authors[0] != "Arkadij Strugacki" || m.Series != "Svijet podne" || m.SeriesIndex != 3 || m.Publisher != "Laguna" || m.Published != "2010" || m.Words != 3 {
		t.Errorf("%+v", m)
	}
	if !bytes.Equal(m.Cover, cover) {
		t.Error("cover")
	}
}

func TestPartialMD5(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.bin")
	data := make([]byte, 70*1024)
	for i := range data {
		data[i] = byte(i * 7)
	}
	os.WriteFile(p, data, 0o644)
	// samples at 0, 1K, 4K, 16K, 64K (the last one short), then stop
	h := md5.New()
	for _, off := range []int{0, 1024, 4096, 16384, 65536} {
		end := min(off+1024, len(data))
		h.Write(data[off:end])
	}
	want := hex.EncodeToString(h.Sum(nil))
	got, err := PartialMD5(p)
	if err != nil || got != want {
		t.Fatalf("got %s want %s (%v)", got, want, err)
	}
	if NameMD5("/a/b/Book.epub") != hexMD5("Book.epub") {
		t.Error("name md5")
	}
}

func hexMD5(s string) string { h := md5.Sum([]byte(s)); return hex.EncodeToString(h[:]) }

func TestNormalizers(t *testing.T) {
	if NormalizeISBN("0-306-40615-2") != "9780306406157" || NormalizeISBN("9780306406158") != "" {
		t.Error("isbn")
	}
	for in, want := range map[string]string{"en-US": "en", "srp": "sr", "Serbian": "sr", "und": "", "xyz": ""} {
		if Language(in) != want {
			t.Errorf("lang %q = %q", in, Language(in))
		}
	}
	for in, want := range map[string]string{"2012-09-12T12:58:05+00:00": "2012-09-12", "0101-01-01T00:00:00+00:00": "", "1999": "1999", "March 2004": "2004"} {
		if NormalizeDate(in) != want {
			t.Errorf("date %q = %q", in, NormalizeDate(in))
		}
	}
	if !naturalLess("page2.jpg", "page10.jpg") || naturalLess("b", "a") {
		t.Error("natural order")
	}
}

// Real files copied to /tmp/plb-a-samples (not part of the repository) are
// read when present, to catch what generated fixtures miss.
func TestRealSamples(t *testing.T) {
	dir := "/tmp/plb-a-samples"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skip("no samples")
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if Detect(p) == "" {
			continue
		}
		m, err := Read(p, true)
		if err != nil {
			t.Errorf("%s: %v", e.Name(), err)
			continue
		}
		t.Logf("%s: title=%q authors=%v series=%q #%v lang=%q pub=%q date=%q isbn=%q pages=%d words=%d cover=%d tags=%v", e.Name(), m.Title, m.Authors, m.Series, m.SeriesIndex, m.Language, m.Publisher, m.Published, m.ISBN, m.Pages, m.Words, len(m.Cover), m.Tags)
		if IsComic(Detect(p)) {
			c, err := OpenComic(p, t.TempDir())
			if err != nil {
				t.Errorf("open comic %s: %v", e.Name(), err)
				continue
			}
			rc, mime, err := c.Page(0)
			if err != nil {
				t.Errorf("page: %v", err)
			} else {
				rc.Close()
			}
			t.Logf("  %d pages, first %s", len(c.Pages()), mime)
			c.Close()
		}
	}
}
