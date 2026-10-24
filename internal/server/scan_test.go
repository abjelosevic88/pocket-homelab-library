package server

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-library/internal/covers"
	"github.com/abjelosevic88/pocket-homelab-library/internal/formats"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

type scanFixture struct {
	srv    *Server
	ts     *httptest.Server
	st     *store.Store
	admin  string // tokens
	member string
	editor string
	lib    *store.Library // open to all, takes uploads
	priv   *store.Library // admins only
}

func newScanFixture(t *testing.T) *scanFixture {
	t.Helper()
	dir := t.TempDir()
	cfg := testConfig(dir)
	cfg.MaxBookUploadBytes = 10 << 20
	st := openTestStore(t, cfg)
	s := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := s.loadSecretKey(); err != nil {
		t.Fatal(err)
	}
	s.startScan(ctx)
	f := &scanFixture{srv: s, st: st}
	mk := func(email, role string, edit bool) string {
		u := &store.User{Email: email, Name: email, Role: role, CanEdit: edit}
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		tok, _, err := st.CreateAPIToken(ctx, u.ID, "t", "token")
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	f.admin = mk("admin@example.com", "admin", false)
	f.member = mk("member@example.com", "member", false)
	f.editor = mk("editor@example.com", "member", true)
	f.lib = &store.Library{Name: "Books", Kind: "books", Paths: []string{filepath.Join(dir, "books")}, Uploads: true}
	f.priv = &store.Library{Name: "Private", Kind: "books", Paths: []string{filepath.Join(dir, "private")}, Access: "people"}
	for _, l := range []*store.Library{f.lib, f.priv} {
		os.MkdirAll(l.Paths[0], 0o755)
		if err := st.CreateLibrary(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	f.ts = httptest.NewServer(s.Router())
	t.Cleanup(f.ts.Close)
	return f
}

func (f *scanFixture) do(t *testing.T, method, path, tok string, body io.Reader, ctype string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, f.ts.URL+path, body)
	req.Header.Set("Authorization", "Bearer "+tok)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func testPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 60, 90))
	for x := 0; x < 60; x++ {
		for y := 0; y < 90; y++ {
			img.Set(x, y, color.RGBA{180, 20, 60, 255})
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func testEPUB(title, author string) []byte {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	add := func(name, body string) { w, _ := zw.Create(name); w.Write([]byte(body)) }
	add("mimetype", "application/epub+zip")
	add("META-INF/container.xml", `<container><rootfiles><rootfile full-path="content.opf"/></rootfiles></container>`)
	add("content.opf", `<package xmlns="http://www.idpf.org/2007/opf" version="2.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>`+title+`</dc:title><dc:creator>`+author+`</dc:creator></metadata><manifest><item id="c" href="c.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c"/></spine></package>`)
	add("c.xhtml", "<html><body><p>Hello there.</p></body></html>")
	zw.Close()
	return b.Bytes()
}

// A cover of a book in a library someone can't open answers 404, like the book.
func TestCoverAccess(t *testing.T) {
	f := newScanFixture(t)
	ctx := context.Background()
	id, err := f.st.CreateBook(ctx, f.priv.ID, "x", store.BookMeta{Title: "Secret"})
	if err != nil {
		t.Fatal(err)
	}
	name, color, err := covers.Save(f.srv.cfg.CoversDir, "book-1", testPNG())
	if err != nil {
		t.Fatal(err)
	}
	f.st.SetBookCover(ctx, id, name, color)
	path := "/api/v1/books/" + itoa(id) + "/cover?w=200"
	if res := f.do(t, "GET", path, f.member, nil, ""); res.StatusCode != http.StatusNotFound {
		t.Errorf("member got %d, want 404", res.StatusCode)
	}
	if res := f.do(t, "GET", path, f.admin, nil, ""); res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "image/jpeg" {
		t.Errorf("admin got %d", res.StatusCode)
	}
}

func uploadBody(t *testing.T, files map[string][]byte) (io.Reader, string) {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for name, data := range files {
		w, _ := mw.CreateFormFile("files", name)
		w.Write(data)
	}
	mw.Close()
	return &b, mw.FormDataContentType()
}

func TestUploadAndDelete(t *testing.T) {
	f := newScanFixture(t)
	body, ct := uploadBody(t, map[string][]byte{"x.epub": testEPUB("Derviš i smrt", "Meša Selimović"), "notes.exe": []byte("no")})
	if res := f.do(t, "POST", "/api/v1/upload", f.member, body, ct); res.StatusCode != http.StatusForbidden {
		t.Fatalf("member upload: %d", res.StatusCode)
	}
	body, ct = uploadBody(t, map[string][]byte{"x.epub": testEPUB("Derviš i smrt", "Meša Selimović"), "notes.exe": []byte("no")})
	res := f.do(t, "POST", "/api/v1/upload", f.editor, body, ct)
	out, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || !strings.Contains(string(out), `"books":[`) || !strings.Contains(string(out), "not a book format") {
		t.Fatalf("editor upload: %d %s", res.StatusCode, out)
	}
	want := filepath.Join(f.lib.Paths[0], "Meša Selimović", "Meša Selimović - Derviš i smrt.epub")
	if _, err := os.Stat(want); err != nil {
		out, _ := exec.Command("find", f.lib.Paths[0]).CombinedOutput()
		t.Fatalf("file not filed by the template: %v\n%s", err, out)
	}
	id, err := f.st.BookIDByPath(context.Background(), want)
	if err != nil {
		t.Fatal(err)
	}
	// a second copy gets " (2)"
	body, ct = uploadBody(t, map[string][]byte{"y.epub": testEPUB("Derviš i smrt", "Meša Selimović")})
	f.do(t, "POST", "/api/v1/upload", f.editor, body, ct)
	if _, err := os.Stat(strings.TrimSuffix(want, ".epub") + " (2).epub"); err != nil {
		t.Errorf("second copy: %v", err)
	}
	// deleting needs the files to go too
	if res := f.do(t, "DELETE", "/api/v1/books/"+itoa(id), f.editor, nil, ""); res.StatusCode != http.StatusBadRequest {
		t.Errorf("delete without files: %d", res.StatusCode)
	}
	if res := f.do(t, "DELETE", "/api/v1/books/"+itoa(id)+"?files=1", f.member, nil, ""); res.StatusCode != http.StatusForbidden {
		t.Errorf("member delete: %d", res.StatusCode)
	}
	if res := f.do(t, "DELETE", "/api/v1/books/"+itoa(id)+"?files=1", f.editor, nil, ""); res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Error("file still there")
	}
}

func TestIngest(t *testing.T) {
	f := newScanFixture(t)
	in := filepath.Join(t.TempDir(), "ingest")
	os.MkdirAll(filepath.Join(in, "sub"), 0o755)
	os.WriteFile(filepath.Join(in, "sub", "book.epub"), testEPUB("Tvrđava", "Meša Selimović"), 0o644)
	os.WriteFile(filepath.Join(in, "readme.nfo"), []byte("x"), 0o644)
	f.srv.cfg.IngestDir = in
	if n := f.srv.ingestOnce(context.Background(), 0); n != 1 {
		t.Fatalf("ingested %d", n)
	}
	if _, err := os.Stat(filepath.Join(f.lib.Paths[0], "Meša Selimović", "Meša Selimović - Tvrđava.epub")); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(in, "sub")); !os.IsNotExist(err) {
		t.Error("empty folder left behind")
	}
	if _, err := os.Stat(filepath.Join(in, "readme.nfo")); err != nil {
		t.Error("a file that is not a book must stay")
	}
}

func TestTemplatePath(t *testing.T) {
	m := &formats.Meta{Title: "Tvrđava", Authors: []string{"Meša Selimović"}}
	if got := templatePath("{series}/{author} - {title}", m, "x.epub"); got != "Meša Selimović - Tvrđava" {
		t.Errorf("empty series: %q", got)
	}
	if got := safeComponent(`a/b:c*?"<>|d`); got != "a b c d" {
		t.Errorf("%q", got)
	}
}
