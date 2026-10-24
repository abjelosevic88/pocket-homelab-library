package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-library/internal/formats"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

// readerFixture is a box with two people: ana sees the "Books" library, bob
// only an own "Private" one that ana can't open.
type readerFixture struct {
	t        *testing.T
	srv      *httptest.Server
	st       *store.Store
	ana, bob *store.User
	anaC     string
	bobC     string
	book     int64 // in Books
	file     int64
	private  int64 // a book in Private (bob only)
	dir      string
}

func newReaderFixture(t *testing.T) *readerFixture {
	t.Helper()
	dir := t.TempDir()
	cfg := testConfig(dir)
	st := openTestStore(t, cfg)
	s := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	ctx := context.Background()
	if err := s.loadSecretKey(); err != nil {
		t.Fatal(err)
	}
	f := &readerFixture{t: t, st: st, dir: dir}
	f.ana = &store.User{Email: "ana@example.com", Name: "Ana", Role: "admin", PasswordHash: "x"}
	f.bob = &store.User{Email: "bob@example.com", Name: "Bob", Role: "member", PasswordHash: "x"}
	for _, u := range []*store.User{f.ana, f.bob} {
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	books := &store.Library{Name: "Books", Paths: []string{dir}, Access: "all"}
	priv := &store.Library{Name: "Private", Paths: []string{dir}, Access: "people", People: []int64{f.bob.ID}}
	for _, l := range []*store.Library{books, priv} {
		if err := st.CreateLibrary(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	// ana is admin and would see Private: make her a member for the access test
	f.ana.Role = "member"
	if err := st.UpdateUser(ctx, f.ana); err != nil {
		t.Fatal(err)
	}
	content := []byte("0123456789abcdefghij")
	path := filepath.Join(dir, "Ana Author - A Book.epub")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	var err error
	if f.book, err = st.CreateBook(ctx, books.ID, "a book", store.BookMeta{Title: "A Book", Authors: []string{"Ana Author"}, Pages: 200}); err != nil {
		t.Fatal(err)
	}
	fl := &store.File{BookID: f.book, LibraryID: books.ID, Path: path, Format: "epub", Size: int64(len(content)), MTime: 1}
	if err := st.UpsertFile(ctx, fl); err != nil {
		t.Fatal(err)
	}
	f.file = fl.ID
	if f.private, err = st.CreateBook(ctx, priv.ID, "secret", store.BookMeta{Title: "Secret"}); err != nil {
		t.Fatal(err)
	}
	pf := &store.File{BookID: f.private, LibraryID: priv.ID, Path: filepath.Join(dir, "secret.epub"), Format: "epub"}
	_ = os.WriteFile(pf.Path, []byte("secret"), 0o644)
	if err := st.UpsertFile(ctx, pf); err != nil {
		t.Fatal(err)
	}
	f.anaC, _ = st.CreateSession(ctx, f.ana.ID, time.Hour)
	f.bobC, _ = st.CreateSession(ctx, f.bob.ID, time.Hour)
	f.srv = httptest.NewServer(s.Router())
	t.Cleanup(f.srv.Close)
	return f
}

func (f *readerFixture) do(cookie, method, path string, body any, hdr ...string) (*http.Response, []byte) {
	f.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, f.srv.URL+path, rd)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res, out
}

func TestFileContentRangeAndAccess(t *testing.T) {
	f := newReaderFixture(t)
	res, body := f.do(f.anaC, "GET", "/api/v1/files/"+itoa(f.file)+"/content", nil, "Range", "bytes=10-14")
	if res.StatusCode != http.StatusPartialContent || string(body) != "abcde" {
		t.Fatalf("range: %d %q", res.StatusCode, body)
	}
	// ana can't open bob's private library: 404, not 403
	var privFile int64
	_ = f.st.DB.QueryRow(`SELECT id FROM files WHERE book_id = ?`, f.private).Scan(&privFile)
	if res, _ := f.do(f.anaC, "GET", "/api/v1/files/"+itoa(privFile)+"/content", nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("other library's file: %d, want 404", res.StatusCode)
	}
	if res, _ := f.do(f.bobC, "GET", "/api/v1/files/"+itoa(privFile)+"/content", nil); res.StatusCode != http.StatusOK {
		t.Fatalf("own library's file: %d", res.StatusCode)
	}
	if res, _ := f.do(f.anaC, "GET", "/api/v1/books/"+itoa(f.private)+"/reader", nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("reader of other library's book: %d", res.StatusCode)
	}
}

func TestReaderOpenAndProgress(t *testing.T) {
	f := newReaderFixture(t)
	res, body := f.do(f.anaC, "GET", "/api/v1/books/"+itoa(f.book)+"/reader", nil)
	if res.StatusCode != 200 {
		t.Fatalf("reader: %d %s", res.StatusCode, body)
	}
	var open struct {
		Engine string      `json:"engine"`
		File   *store.File `json:"file"`
	}
	_ = json.Unmarshal(body, &open)
	if open.Engine != "foliate" || open.File == nil || open.File.ID != f.file {
		t.Fatalf("open = %+v", open)
	}
	loc := map[string]any{"type": "epub", "cfi": "epubcfi(/6/4!/4/2)", "spine": 1, "fraction": 0.5}
	// sendBeacon posts text/plain
	res, body = f.do(f.anaC, "POST", "/api/v1/books/"+itoa(f.book)+"/progress", map[string]any{"file_id": f.file, "percentage": 0.25, "locator": loc, "seconds": 40, "pages": 3, "device": "Safari"}, "Content-Type", "text/plain;charset=UTF-8")
	if res.StatusCode != 200 {
		t.Fatalf("progress: %d %s", res.StatusCode, body)
	}
	var out struct {
		State store.UserBook `json:"state"`
		Stale bool           `json:"stale"`
	}
	_ = json.Unmarshal(body, &out)
	if out.State.Status != "reading" || out.State.Percentage != 0.25 || !strings.Contains(out.State.Locator, "epubcfi") || out.State.LastDevice != "Web reader · Safari" {
		t.Fatalf("state = %+v", out.State)
	}
	ss, _ := f.st.Sessions(context.Background(), f.ana.ID, store.SessionFilter{})
	if len(ss) != 1 || ss[0].Seconds != 40 || ss[0].Pages != 3 || ss[0].Source != "web" {
		t.Fatalf("sessions = %+v", ss)
	}
	// bob has no progress in ana's book
	ub, _ := f.st.GetUserBook(context.Background(), f.bob.ID, f.book)
	if ub.Status != "" {
		t.Fatalf("bob's state = %+v", ub)
	}
	// a session list with titles; bob sees none of ana's
	_, body = f.do(f.anaC, "GET", "/api/v1/sessions", nil)
	if !strings.Contains(string(body), `"book_title":"A Book"`) {
		t.Fatalf("sessions list: %s", body)
	}
	_, body = f.do(f.bobC, "GET", "/api/v1/sessions", nil)
	if strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("bob sees sessions: %s", body)
	}
	if res, _ := f.do(f.bobC, "DELETE", "/api/v1/sessions/"+itoa(ss[0].ID), nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("bob deletes ana's session: %d", res.StatusCode)
	}
	// a wrong file is refused
	if res, _ := f.do(f.anaC, "POST", "/api/v1/books/"+itoa(f.book)+"/progress", map[string]any{"file_id": 9999, "percentage": 0.3}); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("foreign file: %d", res.StatusCode)
	}
	// reaching the end finishes the book
	_, body = f.do(f.anaC, "POST", "/api/v1/books/"+itoa(f.book)+"/progress", map[string]any{"file_id": f.file, "percentage": 1, "seconds": 10})
	_ = json.Unmarshal(body, &out)
	if out.State.Status != "read" || out.State.TimesRead != 1 {
		t.Fatalf("finished state = %+v", out.State)
	}
}

func TestAnnotationsPerPerson(t *testing.T) {
	f := newReaderFixture(t)
	res, body := f.do(f.anaC, "POST", "/api/v1/books/"+itoa(f.book)+"/annotations", map[string]any{
		"kind": "highlight", "text": "It was a bright cold day", "color": "green", "chapter": "One", "percentage": 0.1,
		"locator": map[string]any{"type": "epub", "cfi": "epubcfi(/6/2!/4/2,/1:0,/1:10)"}, "file_id": f.file})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", res.StatusCode, body)
	}
	var a store.Annotation
	_ = json.Unmarshal(body, &a)
	if a.ID == 0 || a.Color != "green" || a.Source != "web" || !strings.Contains(a.Locator, "epubcfi") {
		t.Fatalf("annotation = %+v", a)
	}
	id := itoa(a.ID)
	if res, _ := f.do(f.bobC, "PUT", "/api/v1/annotations/"+id, map[string]any{"note": "mine"}); res.StatusCode != http.StatusNotFound {
		t.Fatalf("bob edits: %d", res.StatusCode)
	}
	if res, _ := f.do(f.bobC, "DELETE", "/api/v1/annotations/"+id, nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("bob deletes: %d", res.StatusCode)
	}
	_, body = f.do(f.bobC, "GET", "/api/v1/books/"+itoa(f.book)+"/annotations", nil)
	if strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("bob lists: %s", body)
	}
	res, body = f.do(f.anaC, "PUT", "/api/v1/annotations/"+id, map[string]any{"note": "Orwell's opening", "color": "pink"})
	_ = json.Unmarshal(body, &a)
	if res.StatusCode != 200 || a.Note != "Orwell's opening" || a.Color != "pink" {
		t.Fatalf("edit: %d %+v", res.StatusCode, a)
	}
	_, body = f.do(f.anaC, "GET", "/api/v1/annotations?q=bright", nil)
	if !strings.Contains(string(body), `"book_title":"A Book"`) {
		t.Fatalf("search: %s", body)
	}
	res, body = f.do(f.anaC, "GET", "/api/v1/annotations/export?book="+itoa(f.book)+"&format=md", nil)
	if res.StatusCode != 200 || !strings.Contains(string(body), "# A Book — Ana Author") || !strings.Contains(string(body), "> It was a bright cold day") || !strings.Contains(string(body), "**Note:** Orwell's opening") {
		t.Fatalf("export: %d %s", res.StatusCode, body)
	}
	if res, _ := f.do(f.anaC, "DELETE", "/api/v1/annotations/"+id, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", res.StatusCode)
	}
}

func TestComicPages(t *testing.T) {
	f := newReaderFixture(t)
	// a CBZ with three pages, the first wide enough to be scaled
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i, name := range []string{"p10.png", "p2.png", "p1.png"} {
		img := image.NewRGBA(image.Rect(0, 0, 1000+i, 1400))
		img.Set(1, 1, color.RGBA{200, 0, 0, 255})
		w, _ := zw.Create(name)
		_ = png.Encode(w, img)
	}
	_ = zw.Close()
	path := filepath.Join(f.dir, "Comic 001.cbz")
	_ = os.WriteFile(path, buf.Bytes(), 0o644)
	if _, err := formats.OpenComic(path, t.TempDir()); errors.Is(err, formats.ErrUnsupported) {
		t.Skip("formats.OpenComic not implemented yet")
	}
	ctx := context.Background()
	var lib int64
	_ = f.st.DB.QueryRow(`SELECT id FROM libraries WHERE name = 'Books'`).Scan(&lib)
	bid, _ := f.st.CreateBook(ctx, lib, "comic 001", store.BookMeta{Title: "Comic 1", Kind: "comic"})
	fl := &store.File{BookID: bid, LibraryID: lib, Path: path, Format: "cbz", Size: int64(buf.Len()), MTime: 2}
	_ = f.st.UpsertFile(ctx, fl)
	res, body := f.do(f.anaC, "GET", "/api/v1/files/"+itoa(fl.ID)+"/pages", nil)
	var pages struct {
		Count int `json:"count"`
		Pages []struct {
			N    int    `json:"n"`
			Name string `json:"name"`
		} `json:"pages"`
	}
	_ = json.Unmarshal(body, &pages)
	if res.StatusCode != 200 || pages.Count != 3 || pages.Pages[0].Name != "p1.png" || pages.Pages[2].Name != "p10.png" {
		t.Fatalf("pages: %d %s", res.StatusCode, body)
	}
	res, body = f.do(f.anaC, "GET", "/api/v1/files/"+itoa(fl.ID)+"/pages/1?w=500", nil)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("scaled page: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(body)); err != nil || cfg.Width != 720 {
		t.Fatalf("scaled width = %d (%v), want 720", cfg.Width, err)
	}
	if res, _ := f.do(f.anaC, "GET", "/api/v1/files/"+itoa(fl.ID)+"/pages/4", nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("page past the end: %d", res.StatusCode)
	}
	res, _ = f.do(f.anaC, "GET", "/api/v1/books/"+itoa(bid)+"/reader", nil)
	if res.StatusCode != 200 {
		t.Fatalf("reader: %d", res.StatusCode)
	}
}
