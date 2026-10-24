package scanner

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

func pngBytes(t *testing.T) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 30, 45))
	for x := 0; x < 30; x++ {
		for y := 0; y < 45; y++ {
			img.Set(x, y, color.RGBA{10, 120, 200, 255})
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func zipFile(t *testing.T, path string, files [][2]string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, kv := range files {
		w, _ := zw.Create(kv[0])
		w.Write([]byte(kv[1]))
	}
	zw.Close()
	f.Close()
}

func epub(t *testing.T, path, title, author string, cover []byte) {
	opf := `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="2.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
<dc:title>` + title + `</dc:title><dc:creator>` + author + `</dc:creator><dc:language>en</dc:language><meta name="cover" content="cov"/></metadata>
<manifest><item id="c" href="c.xhtml" media-type="application/xhtml+xml"/><item id="cov" href="cover.png" media-type="image/png"/></manifest><spine><itemref idref="c"/></spine></package>`
	zipFile(t, path, [][2]string{{"mimetype", "application/epub+zip"}, {"META-INF/container.xml", `<container><rootfiles><rootfile full-path="content.opf"/></rootfiles></container>`},
		{"content.opf", opf}, {"c.xhtml", "<html><body><p>one two three</p></body></html>"}, {"cover.png", string(cover)}})
}

func setup(t *testing.T) (*store.Store, *Scanner) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st, &Scanner{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), CoversDir: filepath.Join(dir, "covers")}
}

func TestScanBooks(t *testing.T) {
	ctx := context.Background()
	st, sc := setup(t)
	root := t.TempDir()
	cover := pngBytes(t)
	// the archive layout: EPUB + MOBI twin + sidecar OPF in an author folder
	a := filepath.Join(root, "biblioteka", "Ivo Andrić")
	epub(t, filepath.Join(a, "Ivo Andrić - Na Drini ćuprija.epub"), "Embedded Title", "Embedded Author", cover)
	os.WriteFile(filepath.Join(a, "Ivo Andrić - Na Drini ćuprija.mobi"), []byte("not really a mobi"), 0o644)
	os.WriteFile(filepath.Join(a, "Ivo Andrić - Na Drini ćuprija.opf"), []byte(`<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
<dc:title>Na Drini ćuprija</dc:title><dc:creator opf:role="aut">Ivo Andrić</dc:creator><dc:identifier opf:scheme="calibre">42</dc:identifier></metadata></package>`), 0o644)
	// a file with no metadata, named after its folder's author
	os.WriteFile(filepath.Join(a, "Ivo Andrić - Prokleta avlija.txt"), []byte("text"), 0o644)
	// Calibre's layout: one folder per book
	c := filepath.Join(root, "calibre", "Orwell", "1984 (12)")
	epub(t, filepath.Join(c, "1984 - George Orwell.epub"), "1984", "George Orwell", cover)
	os.WriteFile(filepath.Join(c, "metadata.opf"), []byte(`<package xmlns="http://www.idpf.org/2007/opf"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Nineteen Eighty-Four</dc:title><dc:creator>George Orwell</dc:creator></metadata></package>`), 0o644)
	os.WriteFile(filepath.Join(c, "cover.jpg"), cover, 0o644)
	os.WriteFile(filepath.Join(c, "1984 - George Orwell.azw3"), []byte("x"), 0o644)

	lib := &store.Library{Name: "Books", Kind: "books", Paths: []string{root}}
	if err := st.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	res, err := sc.Scan(ctx, lib, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 3 || res.Files != 5 {
		t.Fatalf("added %d books from %d files, want 3 from 5 (%v)", len(res.Added), res.Files, res.Errors)
	}
	all := []int64{lib.ID}
	books, _, _ := st.ListBooks(ctx, store.BookQuery{Libraries: all, Limit: 50})
	byTitle := map[string]store.Book{}
	for _, b := range books {
		byTitle[b.Title] = b
	}
	drina, ok := byTitle["Na Drini ćuprija"]
	if !ok || len(drina.Authors) != 1 || drina.Authors[0].Name != "Ivo Andrić" || len(drina.Formats) != 2 || drina.Cover == "" {
		t.Fatalf("sidecar book wrong: %+v", drina)
	}
	if d, _ := st.GetBookAny(ctx, drina.ID); d.Identifiers["calibre"] != "42" || d.MetaSource != "sidecar" {
		t.Errorf("identifiers %v source %q", d.Identifiers, d.MetaSource)
	}
	if b, ok := byTitle["Prokleta avlija"]; !ok || len(b.Authors) != 1 || b.Authors[0].Name != "Ivo Andrić" {
		t.Errorf("file-name fallback: %+v", b)
	}
	if b, ok := byTitle["Nineteen Eighty-Four"]; !ok || len(b.Formats) != 2 || b.Cover == "" {
		t.Errorf("calibre folder: %+v", b)
	}

	// a title edited by hand survives a full rescan
	m := store.MetaOf(&drina)
	m.Title = "The Bridge on the Drina"
	st.SaveBookMeta(ctx, drina.ID, m)
	st.SetLocked(ctx, drina.ID, []string{"title"})
	if _, err := sc.Scan(ctx, lib, true, nil); err != nil {
		t.Fatal(err)
	}
	if d, _ := st.GetBookAny(ctx, drina.ID); d.Title != "The Bridge on the Drina" || len(d.Authors) != 1 {
		t.Errorf("locked title lost: %q", d.Title)
	}

	// a vanished file is missing; the whole book when all its files are gone
	os.Remove(filepath.Join(a, "Ivo Andrić - Prokleta avlija.txt"))
	res, _ = sc.Scan(ctx, lib, false, nil)
	if res.Removed != 1 {
		t.Errorf("removed %d", res.Removed)
	}
	if b, _ := st.GetBookAny(ctx, byTitle["Prokleta avlija"].ID); !b.Missing {
		t.Error("book should be missing")
	}

	// a renamed file keeps its book (and everyone's progress)
	old := filepath.Join(c, "1984 - George Orwell.epub")
	time.Sleep(10 * time.Millisecond)
	nd := filepath.Join(root, "moved")
	os.MkdirAll(nd, 0o755)
	os.Rename(filepath.Join(a, "Ivo Andrić - Na Drini ćuprija.epub"), filepath.Join(nd, "drina.epub"))
	_ = old
	res, _ = sc.Scan(ctx, lib, false, nil)
	if len(res.Added) != 0 {
		t.Errorf("a moved file made a new book: %v", res.Added)
	}

	// an unreachable folder marks nothing missing
	bad := &store.Library{ID: lib.ID, Kind: "books", Paths: []string{filepath.Join(root, "nope")}}
	if _, err := sc.Scan(ctx, bad, false, nil); err == nil {
		t.Error("scan of a missing folder should fail")
	}
}

func TestScanComics(t *testing.T) {
	ctx := context.Background()
	st, sc := setup(t)
	root := t.TempDir()
	pg := string(pngBytes(t))
	zipFile(t, filepath.Join(root, "Weeb Central (EN)", "Slime", "Official_Chapter 6.5.cbz"), [][2]string{{"01.png", pg}, {"02.png", pg}})
	zipFile(t, filepath.Join(root, "Weeb Central (EN)", "Slime", "Official_Chapter 7.cbz"), [][2]string{{"01.png", pg}})
	lib := &store.Library{Name: "Manga", Kind: "manga", Paths: []string{root}}
	st.CreateLibrary(ctx, lib)
	res, err := sc.Scan(ctx, lib, false, nil)
	if err != nil || len(res.Added) != 2 {
		t.Fatalf("%v %+v", err, res)
	}
	books, _, _ := st.ListBooks(ctx, store.BookQuery{Libraries: []int64{lib.ID}, Sort: "series"})
	if len(books) != 2 || books[0].Series != "Slime" || books[0].SeriesIndex != 6.5 || books[0].Title != "Chapter 6.5" || books[0].Kind != "manga" || books[0].ReadingDirection != "rtl" {
		t.Fatalf("%+v", books[0])
	}
	if d, _ := st.GetBookAny(ctx, books[0].ID); d.Files[0].Pages != 2 || d.Files[0].PartialMD5 == "" {
		t.Errorf("file %+v", d.Files[0])
	}
}
