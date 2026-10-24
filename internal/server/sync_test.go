package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
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

// syncWorld is a box with a library, one EPUB, one PDF and two people.
type syncWorld struct {
	t       *testing.T
	srv     *httptest.Server
	s       *Server
	st      *store.Store
	ana     *store.User
	bob     *store.User
	anaPass string // ana's KOReader app password
	bobPass string
	book    int64 // the EPUB book
	file    *store.File
	pdfBook int64
	device  *store.Device // ana's KOReader
}

func writeTestEPUB(t *testing.T, p string, chapters ...string) {
	t.Helper()
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("mimetype")
	w.Write([]byte("application/epub+zip"))
	w, _ = zw.Create("META-INF/container.xml")
	w.Write([]byte(`<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`))
	var man, spine strings.Builder
	for i, c := range chapters {
		fmt.Fprintf(&man, `<item id="c%d" href="c%d.xhtml" media-type="application/xhtml+xml"/>`, i, i)
		fmt.Fprintf(&spine, `<itemref idref="c%d"/>`, i)
		w, _ = zw.Create(fmt.Sprintf("c%d.xhtml", i))
		w.Write([]byte(`<html xmlns="http://www.w3.org/1999/xhtml"><head><title>x</title></head><body>` + c + `</body></html>`))
	}
	w, _ = zw.Create("content.opf")
	w.Write([]byte(`<package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata/><manifest>` + man.String() + `</manifest><spine>` + spine.String() + `</spine></package>`))
	zw.Close()
	f.Close()
}

func newSyncWorld(t *testing.T) *syncWorld {
	t.Helper()
	dir := t.TempDir()
	cfg := testConfig(dir)
	st := openTestStore(t, cfg)
	s := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	ctx := context.Background()
	w := &syncWorld{t: t, s: s, st: st}
	w.ana = &store.User{Email: "ana@example.com", Name: "Ana", Role: "admin"}
	w.bob = &store.User{Email: "bob@example.com", Name: "Bob", Role: "member"}
	for _, u := range []*store.User{w.ana, w.bob} {
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	books := filepath.Join(dir, "books")
	os.MkdirAll(books, 0o755)
	lib := &store.Library{Name: "Books", Kind: "books", Paths: []string{books}, Access: "all"}
	if err := st.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	epub := filepath.Join(books, "George Orwell - 1984.epub")
	writeTestEPUB(t, epub, `<p>`+strings.Repeat("War is peace. ", 40)+`</p>`, `<p>`+strings.Repeat("Freedom is slavery. ", 40)+`</p><p>`+strings.Repeat("Ignorance is strength. ", 40)+`</p>`)
	var err error
	w.book, err = st.CreateBook(ctx, lib.ID, "orwell - 1984", store.BookMeta{Title: "1984", Authors: []string{"George Orwell"}, Pages: 300})
	if err != nil {
		t.Fatal(err)
	}
	md5, _ := formats.PartialMD5(epub)
	if md5 == "" {
		md5 = "0123456789abcdef0123456789abcdef"
	}
	fi, _ := os.Stat(epub)
	w.file = &store.File{BookID: w.book, LibraryID: lib.ID, Path: epub, Format: "epub", Size: fi.Size(), MTime: fi.ModTime().Unix(), PartialMD5: md5, NameMD5: store.MD5Hex(filepath.Base(epub))}
	if err := st.UpsertFile(ctx, w.file); err != nil {
		t.Fatal(err)
	}
	pdf := filepath.Join(books, "Manual.pdf")
	os.WriteFile(pdf, []byte("%PDF-1.4 test"), 0o644)
	w.pdfBook, _ = st.CreateBook(ctx, lib.ID, "manual", store.BookMeta{Title: "Manual", Pages: 40})
	if err := st.UpsertFile(ctx, &store.File{BookID: w.pdfBook, LibraryID: lib.ID, Path: pdf, Format: "pdf", Size: 13, Pages: 40, PartialMD5: "fedcba9876543210fedcba9876543210"}); err != nil {
		t.Fatal(err)
	}
	var tok *store.APIToken
	w.anaPass, tok, _ = st.CreateAPIToken(ctx, w.ana.ID, "KOReader", "device")
	w.device = &store.Device{Kind: "koreader", Name: "Kindle", TokenID: tok.ID}
	if err := st.CreateDevice(ctx, w.ana.ID, w.device); err != nil {
		t.Fatal(err)
	}
	w.bobPass, _, _ = st.CreateAPIToken(ctx, w.bob.ID, "KOReader", "device")
	w.srv = httptest.NewServer(s.Router())
	t.Cleanup(w.srv.Close)
	return w
}

// do sends a request with KOReader's headers (user + MD5 key); user "" = none.
func (w *syncWorld) kosync(method, path, user, pass string, body any) (int, map[string]any) {
	w.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, w.srv.URL+path, rd)
	req.Header.Set("Accept", "application/vnd.koreader.v1+json")
	if user != "" {
		req.Header.Set("X-Auth-User", user)
		req.Header.Set("X-Auth-Key", store.MD5Hex(pass))
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		w.t.Fatal(err)
	}
	defer res.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func (w *syncWorld) basic(method, path, user, pass string, body any) (*http.Response, []byte) {
	w.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, w.srv.URL+path, rd)
	if user != "" {
		req.Header.Set("Authorization", basicHeader(user, pass))
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		w.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, b
}

func TestKosyncAuth(t *testing.T) {
	w := newSyncWorld(t)
	if code, _ := w.kosync("GET", "/kosync/users/auth", "ana@example.com", w.anaPass, nil); code != 200 {
		t.Fatalf("md5 key: %d", code)
	}
	if code, _ := w.kosync("GET", "/kosync/users/auth", "ana", w.anaPass, nil); code != 200 {
		t.Fatalf("local part as username: %d", code)
	}
	if res, _ := w.basic("GET", "/kosync/users/auth", "ana@example.com", w.anaPass, nil); res.StatusCode != 200 {
		t.Fatalf("basic: %d", res.StatusCode)
	}
	code, body := w.kosync("GET", "/kosync/users/auth", "ana@example.com", "wrong", nil)
	if code != 401 || body["code"] != float64(2001) {
		t.Fatalf("wrong key: %d %v", code, body)
	}
	// bob's password with ana's name
	if code, _ := w.kosync("GET", "/kosync/users/auth", "ana@example.com", w.bobPass, nil); code != 401 {
		t.Fatalf("someone else's password: %d", code)
	}
	if code, body := w.kosync("POST", "/kosync/users/create", "", "", map[string]string{"username": "x", "password": "y"}); code != 402 || body["code"] != float64(2005) {
		t.Fatalf("register: %d %v", code, body)
	}
	for i := 0; i < syncMaxFails; i++ {
		w.kosync("GET", "/kosync/users/auth", "ana@example.com", "wrong", nil)
	}
	if code, _ := w.kosync("GET", "/kosync/users/auth", "ana@example.com", w.anaPass, nil); code != 429 {
		t.Fatalf("after many failures: %d", code)
	}
}

func TestKosyncProgress(t *testing.T) {
	w := newSyncWorld(t)
	doc := w.file.PartialMD5
	ana := "ana@example.com"
	if code, b := w.kosync("PUT", "/kosync/syncs/progress", ana, w.anaPass, map[string]any{"document": doc, "progress": "x", "percentage": 0.1}); code != 403 || b["code"] != float64(2003) {
		t.Fatalf("missing device: %d %v", code, b)
	}
	if code, b := w.kosync("PUT", "/kosync/syncs/progress", ana, w.anaPass, map[string]any{"progress": "x", "percentage": 0.1, "device": "K"}); code != 403 || b["code"] != float64(2004) {
		t.Fatalf("missing document: %d %v", code, b)
	}
	if code, b := w.kosync("GET", "/kosync/syncs/progress/"+doc+"?position_kinds=locator,percentage", ana, w.anaPass, nil); code != 200 || len(b) != 0 {
		t.Fatalf("unknown document: %d %v", code, b)
	}
	xp := "/body/DocFragment[2]/body/p[1]/text().20"
	code, b := w.kosync("PUT", "/kosync/syncs/progress", ana, w.anaPass, map[string]any{"document": doc, "progress": xp, "percentage": 0.42, "device": "Kindle", "device_id": "K1"})
	if code != 200 || b["document"] != doc || b["timestamp"] == nil || len(b) != 2 {
		t.Fatalf("put: %d %v", code, b)
	}
	code, b = w.kosync("GET", "/kosync/syncs/progress/"+doc, ana, w.anaPass, nil)
	if code != 200 || b["progress"] != xp || b["percentage"] != 0.42 || b["device"] != "Kindle" || b["device_id"] != "K1" {
		t.Fatalf("get: %d %v", code, b)
	}
	ub, _ := w.st.GetUserBook(context.Background(), w.ana.ID, w.book)
	if ub.Status != "reading" || ub.Percentage != 0.42 || ub.FileID != w.file.ID || !strings.Contains(ub.Locator, `"spine":1`) {
		t.Fatalf("book place: %+v", ub)
	}
	// the file-name hash (CrossPoint's default) lands on the same book
	nameDoc := w.file.NameMD5
	if code, _ := w.kosync("PUT", "/kosync/syncs/progress", ana, w.anaPass, map[string]any{"document": nameDoc, "progress": "/body/DocFragment[2]/body/p[2]/text().5", "percentage": 0.7, "device": "CrossPoint", "device_id": "crosspoint-reader"}); code != 200 {
		t.Fatal(code)
	}
	ub, _ = w.st.GetUserBook(context.Background(), w.ana.ID, w.book)
	if ub.Percentage != 0.7 || ub.LastDevice != "CrossPoint" {
		t.Fatalf("by name hash: %+v", ub)
	}
	// bob sees nothing of ana's, and his own sync doesn't touch hers
	if code, b := w.kosync("GET", "/kosync/syncs/progress/"+doc, "bob@example.com", w.bobPass, nil); code != 200 || b["percentage"] != nil {
		// bob has no place of his own; the book is in his library, but ana's position is hers
		t.Fatalf("bob read ana's place: %v", b)
	}
	w.kosync("PUT", "/kosync/syncs/progress", "bob@example.com", w.bobPass, map[string]any{"document": doc, "progress": "/body/DocFragment[1]/body/p[1]/text().1", "percentage": 0.01, "device": "Kobo"})
	ub, _ = w.st.GetUserBook(context.Background(), w.ana.ID, w.book)
	if ub.Percentage != 0.7 {
		t.Fatalf("bob moved ana's place: %+v", ub)
	}
}

func TestKosyncPrivateLibrary(t *testing.T) {
	w := newSyncWorld(t)
	ctx := context.Background()
	dir := t.TempDir()
	lib := &store.Library{Name: "Ana only", Kind: "books", Paths: []string{dir}, Access: "people", People: []int64{w.ana.ID}}
	w.st.CreateLibrary(ctx, lib)
	p := filepath.Join(dir, "secret.epub")
	writeTestEPUB(t, p, "<p>secret</p>")
	id, _ := w.st.CreateBook(ctx, lib.ID, "secret", store.BookMeta{Title: "Secret"})
	f := &store.File{BookID: id, LibraryID: lib.ID, Path: p, Format: "epub", PartialMD5: "11111111111111111111111111111111"}
	w.st.UpsertFile(ctx, f)
	w.kosync("PUT", "/kosync/syncs/progress", "bob@example.com", w.bobPass, map[string]any{"document": f.PartialMD5, "progress": "/body/DocFragment[1]/body", "percentage": 0.5, "device": "Kobo"})
	if ub, _ := w.st.GetUserBook(ctx, w.bob.ID, id); ub.Status != "" {
		t.Fatalf("bob got a place in a library he can't open: %+v", ub)
	}
}

func TestKosyncNewerPlaceFromWeb(t *testing.T) {
	w := newSyncWorld(t)
	ctx := context.Background()
	doc := w.file.PartialMD5
	ana := "ana@example.com"
	w.kosync("PUT", "/kosync/syncs/progress", ana, w.anaPass, map[string]any{"document": doc, "progress": "/body/DocFragment[1]/body/p[1]/text().10", "percentage": 0.05, "device": "Kindle"})
	// read on in the web reader, a little later
	loc := `{"type":"epub","cfi":"epubcfi(/6/4!/4/2/1:10)","spine":1,"fraction":0.5}`
	if _, _, _, err := w.st.SetProgress(ctx, w.ana.ID, w.book, store.Progress{FileID: w.file.ID, Percentage: 0.66, Locator: loc, Device: "Web", At: time.Now().Add(10 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	code, b := w.kosync("GET", "/kosync/syncs/progress/"+doc, ana, w.anaPass, nil)
	if code != 200 || b["device"] != "Pocket Library" || !strings.HasPrefix(b["progress"].(string), "/body/DocFragment[2]/body/p[") {
		t.Fatalf("synthesized: %d %v", code, b)
	}
	if pct := b["percentage"].(float64); pct < 0.5 || pct > 0.9 {
		t.Fatalf("percentage %v", pct)
	}
	// a PDF place is a page number
	pdfFile := "fedcba9876543210fedcba9876543210"
	w.st.SetProgress(ctx, w.ana.ID, w.pdfBook, store.Progress{Percentage: 0.5, Locator: `{"type":"pdf","page":20}`})
	_, b = w.kosync("GET", "/kosync/syncs/progress/"+pdfFile, ana, w.anaPass, nil)
	if b["progress"] != "20" {
		t.Fatalf("pdf page: %v", b)
	}
}

type testFeed struct {
	XMLName xml.Name `xml:"feed"`
	Links   []struct {
		Rel  string `xml:"rel,attr"`
		Href string `xml:"href,attr"`
		Type string `xml:"type,attr"`
	} `xml:"link"`
	Entries []struct {
		Title string `xml:"title"`
		Links []struct {
			Rel  string `xml:"rel,attr"`
			Href string `xml:"href,attr"`
			Type string `xml:"type,attr"`
		} `xml:"link"`
	} `xml:"entry"`
}

func TestOPDS(t *testing.T) {
	w := newSyncWorld(t)
	res, _ := w.basic("GET", "/opds/", "", "", nil)
	if res.StatusCode != 401 || !strings.HasPrefix(res.Header.Get("WWW-Authenticate"), "Basic") {
		t.Fatalf("no auth: %d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}
	res, body := w.basic("GET", "/opds/", "ana@example.com", w.anaPass, nil)
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Type"), "kind=navigation") {
		t.Fatalf("root: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	var root testFeed
	if err := xml.Unmarshal(body, &root); err != nil {
		t.Fatalf("root xml: %v\n%s", err, body)
	}
	searchTemplate := false
	for _, l := range root.Links {
		if l.Rel == "search" && strings.Contains(l.Href, "{searchTerms}") && strings.HasPrefix(l.Href, "http") {
			searchTemplate = true
		}
	}
	if !searchTemplate || len(root.Entries) < 5 || !strings.HasPrefix(root.Entries[0].Title, "To send to") {
		t.Fatalf("root feed: %s", body)
	}
	res, body = w.basic("GET", "/opds/recent", "ana@example.com", w.anaPass, nil)
	var recent testFeed
	if err := xml.Unmarshal(body, &recent); err != nil || res.StatusCode != 200 {
		t.Fatalf("recent: %d %v", res.StatusCode, err)
	}
	if len(recent.Entries) != 2 {
		t.Fatalf("recent entries: %d", len(recent.Entries))
	}
	var epubHref string
	for _, e := range recent.Entries {
		for _, l := range e.Links {
			if l.Rel == "http://opds-spec.org/acquisition" && l.Type == "application/epub+zip" {
				epubHref = l.Href
			}
		}
	}
	if epubHref == "" || !strings.Contains(epubHref, "George%20Orwell%20-%201984.epub") {
		t.Fatalf("no epub acquisition link: %s", body)
	}
	res, b := w.basic("GET", strings.TrimPrefix(epubHref, w.srv.URL), "ana@example.com", w.anaPass, nil)
	if res.StatusCode != 200 || len(b) < 100 {
		t.Fatalf("download: %d %d bytes", res.StatusCode, len(b))
	}
	// bob can't download from a library he can't open
	ctx := context.Background()
	dir := t.TempDir()
	lib := &store.Library{Name: "Ana only", Kind: "books", Paths: []string{dir}, Access: "people", People: []int64{w.ana.ID}}
	w.st.CreateLibrary(ctx, lib)
	id, _ := w.st.CreateBook(ctx, lib.ID, "secret", store.BookMeta{Title: "Secret"})
	p := filepath.Join(dir, "secret.epub")
	writeTestEPUB(t, p, "<p>secret</p>")
	f := &store.File{BookID: id, LibraryID: lib.ID, Path: p, Format: "epub"}
	w.st.UpsertFile(ctx, f)
	if res, _ := w.basic("GET", fmt.Sprintf("/opds/download/%d/x.epub", f.ID), "bob@example.com", w.bobPass, nil); res.StatusCode != 404 {
		t.Fatalf("bob downloaded ana's book: %d", res.StatusCode)
	}
	// a CrossPoint sees EPUBs only
	pass, tok, _ := w.st.CreateAPIToken(ctx, w.ana.ID, "X4", "device")
	w.st.CreateDevice(ctx, w.ana.ID, &store.Device{Kind: "crosspoint", Name: "X4", TokenID: tok.ID})
	_, body = w.basic("GET", "/opds/recent", "ana", pass, nil)
	var cp testFeed
	xml.Unmarshal(body, &cp)
	for _, e := range cp.Entries {
		if e.Title == "Manual" {
			t.Fatalf("crosspoint got the PDF: %s", body)
		}
	}
	// search
	_, body = w.basic("GET", "/opds/search?q=1984", "ana@example.com", w.anaPass, nil)
	var found testFeed
	xml.Unmarshal(body, &found)
	if len(found.Entries) != 1 || found.Entries[0].Title != "1984" {
		t.Fatalf("search: %s", body)
	}
}

func TestQueueAndPluginZip(t *testing.T) {
	w := newSyncWorld(t)
	ctx := context.Background()
	send := &store.Send{DeviceID: w.device.ID, DeviceName: "Kindle", BookID: w.book, FileID: w.file.ID, Title: "1984", Format: "epub", Method: "pull"}
	if err := w.st.CreateSend(ctx, w.ana.ID, send); err != nil {
		t.Fatal(err)
	}
	res, body := w.basic("GET", "/koreader/v1/queue", "ana@example.com", w.anaPass, nil)
	var items []map[string]any
	json.Unmarshal(body, &items)
	if res.StatusCode != 200 || len(items) != 1 || items[0]["title"] != "1984" || !strings.HasPrefix(items[0]["url"].(string), "/opds/download/") {
		t.Fatalf("queue: %d %s", res.StatusCode, body)
	}
	// bob can't mark ana's send
	if res, _ := w.basic("POST", fmt.Sprintf("/koreader/v1/queue/%d/done", send.ID), "bob@example.com", w.bobPass, map[string]any{}); res.StatusCode != 404 {
		t.Fatalf("bob marked ana's send: %d", res.StatusCode)
	}
	// downloading through the catalogue delivers it
	if res, _ := w.basic("GET", items[0]["url"].(string), "ana@example.com", w.anaPass, nil); res.StatusCode != 200 {
		t.Fatalf("download %d", res.StatusCode)
	}
	_, body = w.basic("GET", "/koreader/v1/queue", "ana@example.com", w.anaPass, nil)
	if strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("queue after download: %s", body)
	}
	res, body = w.basic("GET", "/koreader/plugin.zip", "", "", nil)
	if res.StatusCode != 200 {
		t.Fatalf("plugin zip: %d", res.StatusCode)
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["pocketlibrary.koplugin/_meta.lua"] || !names["pocketlibrary.koplugin/main.lua"] {
		t.Fatalf("plugin zip holds %v", names)
	}
}

func TestKOReaderAnnotationsAndStats(t *testing.T) {
	w := newSyncWorld(t)
	ctx := context.Background()
	doc := w.file.PartialMD5
	anns := []map[string]any{
		{"datetime": "2026-10-20 21:00:00", "text": "War is peace.", "chapter": "One", "pageno": 3, "pos0": "/body/DocFragment[1]/body/p[1]/text().0", "pos1": "/body/DocFragment[1]/body/p[1]/text().13", "color": "green", "drawer": "lighten"},
		{"datetime": "2026-10-20 21:05:00", "text": "Freedom is slavery.", "note": "!", "pageno": 9, "pos0": "/body/DocFragment[2]/body/p[1]/text().0", "pos1": "/body/DocFragment[2]/body/p[1]/text().19", "drawer": "underscore"},
	}
	post := func(list []map[string]any) map[string]any {
		_, body := w.basic("POST", "/koreader/v1/annotations", "ana@example.com", w.anaPass, map[string]any{"document": doc, "title": "1984", "complete": true, "annotations": list})
		out := map[string]any{}
		json.Unmarshal(body, &out)
		return out
	}
	if r := post(anns); r["matched"] != true || r["created"] != float64(2) {
		t.Fatalf("first sync: %v", r)
	}
	if r := post(anns); r["created"] != float64(0) || r["updated"] != float64(2) {
		t.Fatalf("second sync: %v", r)
	}
	list, _ := w.st.Annotations(ctx, w.ana.ID, store.AnnotationFilter{BookID: w.book})
	if len(list) != 2 || list[1].Color != "underline" || list[0].Color != "green" || list[1].Percentage <= list[0].Percentage {
		t.Fatalf("annotations: %+v", list)
	}
	if r := post(anns[:1]); r["deleted"] != float64(1) {
		t.Fatalf("complete sync: %v", r)
	}
	if list, _ := w.st.Annotations(ctx, w.bob.ID, store.AnnotationFilter{BookID: w.book}); len(list) != 0 {
		t.Fatal("bob got ana's highlights")
	}
	// reading time: two sittings an hour apart
	base := time.Date(2026, 10, 20, 20, 0, 0, 0, time.Local).Unix()
	var pages []map[string]any
	for i := 0; i < 10; i++ {
		pages = append(pages, map[string]any{"page": i + 1, "start_time": base + int64(i*60), "duration": 55, "total_pages": 100})
		pages = append(pages, map[string]any{"page": 50 + i, "start_time": base + 7200 + int64(i*60), "duration": 50, "total_pages": 100})
	}
	_, body := w.basic("POST", "/koreader/v1/stats", "ana@example.com", w.anaPass, map[string]any{"document": doc, "pages": pages})
	if !strings.Contains(string(body), `"sessions":2`) {
		t.Fatalf("stats: %s", body)
	}
	_, body = w.basic("POST", "/koreader/v1/stats", "ana@example.com", w.anaPass, map[string]any{"document": doc, "pages": pages})
	if !strings.Contains(string(body), `"sessions":0`) {
		t.Fatalf("stats sent twice: %s", body)
	}
	ss, _ := w.st.Sessions(ctx, w.ana.ID, store.SessionFilter{BookID: w.book})
	total := 0
	for _, s := range ss {
		total += s.Seconds
		if s.Estimated {
			t.Fatalf("measured session marked estimated: %+v", s)
		}
	}
	if len(ss) != 2 || total != 10*55+10*50 {
		t.Fatalf("sessions: %+v", ss)
	}
}

func TestCrossPointClippings(t *testing.T) {
	w := newSyncWorld(t)
	ctx := context.Background()
	doc := w.file.NameMD5
	ana := "ana@example.com"
	code, b := w.kosync("GET", "/kosync/api/v1/clippings/"+doc+"?cursor=0&limit=1&format=reader", ana, w.anaPass, nil)
	if code != 200 || b["sync_version"] != float64(2) || b["more"] != false {
		t.Fatalf("probe: %d %v", code, b)
	}
	item := map[string]any{"id": "c0ffee0011223344", "spine": 1, "start_page": 2, "end_page": 2, "pages": 10, "start_word": 5, "end_word": 9, "words": 300,
		"chapter": "Two", "text": "Freedom is slavery.", "created_at": 1760000000, "layout_signature": 77}
	code, b = w.kosync("PUT", "/kosync/api/v1/clippings/"+doc, ana, w.anaPass, map[string]any{"items": []any{item}})
	if code != 200 || b["accepted"] != float64(1) {
		t.Fatalf("put: %d %v", code, b)
	}
	code, b = w.kosync("GET", "/kosync/api/v1/clippings/"+doc+"?cursor=0&limit=1&format=reader", ana, w.anaPass, nil)
	items := b["items"].([]any)
	got := items[0].(map[string]any)
	if len(items) != 1 || got["id"] != "c0ffee0011223344" || got["layout_signature"] != float64(77) || got["revision"] == nil || b["cursor"].(float64) <= 0 {
		t.Fatalf("get: %v", b)
	}
	list, _ := w.st.Annotations(ctx, w.ana.ID, store.AnnotationFilter{BookID: w.book})
	if len(list) != 1 || list[0].Source != "crosspoint" || list[0].Text != "Freedom is slavery." {
		t.Fatalf("annotation: %+v", list)
	}
	cursor := b["cursor"].(float64)
	if _, b = w.kosync("GET", fmt.Sprintf("/kosync/api/v1/clippings/%s?cursor=%d&limit=1&format=reader", doc, int(cursor)), ana, w.anaPass, nil); len(b["items"].([]any)) != 0 || b["more"] != false {
		t.Fatalf("after the last: %v", b)
	}
	// delete, then a late upload of the same id stays deleted
	w.kosync("PUT", "/kosync/api/v1/clippings/"+doc, ana, w.anaPass, map[string]any{"items": []any{map[string]any{"id": "c0ffee0011223344", "deleted": 1}}})
	w.kosync("PUT", "/kosync/api/v1/clippings/"+doc, ana, w.anaPass, map[string]any{"items": []any{item}})
	if list, _ := w.st.Annotations(ctx, w.ana.ID, store.AnnotationFilter{BookID: w.book}); len(list) != 0 {
		t.Fatalf("deleted clipping came back: %+v", list)
	}
	_, b = w.kosync("GET", "/kosync/api/v1/clippings/"+doc+"?cursor=0&limit=5", ana, w.anaPass, nil)
	if it := b["items"].([]any); len(it) != 1 || it[0].(map[string]any)["deleted"] != float64(1) {
		t.Fatalf("tombstone: %v", b)
	}
	// bob has none
	if _, b = w.kosync("GET", "/kosync/api/v1/clippings/"+doc+"?cursor=0&limit=5", "bob@example.com", w.bobPass, nil); len(b["items"].([]any)) != 0 {
		t.Fatalf("bob sees ana's clippings: %v", b)
	}
}
