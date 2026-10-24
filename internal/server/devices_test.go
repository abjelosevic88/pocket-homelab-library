package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-library/internal/epub"
	"github.com/abjelosevic88/pocket-homelab-library/internal/mail/mailtest"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

// devFixture is a server with two people, a library with one EPUB and API tokens.
type devFixture struct {
	t      *testing.T
	s      *Server
	st     *store.Store
	srv    *httptest.Server
	ana    *store.User
	bob    *store.User
	anaTok string
	bobTok string
	book   int64
	file   int64
	dir    string
}

func newDevFixture(t *testing.T) *devFixture {
	t.Helper()
	dir := t.TempDir()
	cfg := testConfig(dir)
	cfg.BaseURL = "https://library.example.com"
	cfg.EbookConvert = "off"
	st := openTestStore(t, cfg)
	s := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err := s.loadSecretKey(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	f := &devFixture{t: t, s: s, st: st, dir: dir}
	f.ana = &store.User{Email: "ana@example.com", Name: "Ana", Role: "admin", PasswordHash: "x"}
	f.bob = &store.User{Email: "bob@example.com", Name: "Bob", Role: "member", PasswordHash: "x"}
	for _, u := range []*store.User{f.ana, f.bob} {
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	f.anaTok, _, _ = st.CreateAPIToken(ctx, f.ana.ID, "t", "token")
	f.bobTok, _, _ = st.CreateAPIToken(ctx, f.bob.ID, "t", "token")
	libDir := filepath.Join(dir, "books")
	os.MkdirAll(libDir, 0o755)
	lib := &store.Library{Name: "Books", Kind: "books", Paths: []string{libDir}, Access: "all"}
	if err := st.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	data, err := epub.Minimal("Old title", "Someone", "Unknown", "Hello.\n\nWorld.")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(libDir, "Ivo Andric - Na Drini cuprija.epub")
	os.WriteFile(p, data, 0o644)
	f.book, err = st.CreateBook(ctx, lib.ID, "/ivo andric - na drini cuprija", store.BookMeta{Title: "Na Drini ćuprija", Authors: []string{"Ivo Andrić"}, Language: "sr", Series: "Bosnian trilogy", SeriesIndex: 1})
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	fl := &store.File{BookID: f.book, LibraryID: lib.ID, Path: p, Format: "epub", Size: fi.Size(), MTime: fi.ModTime().Unix()}
	if err := st.UpsertFile(ctx, fl); err != nil {
		t.Fatal(err)
	}
	f.file = fl.ID
	f.srv = httptest.NewServer(s.Router())
	t.Cleanup(f.srv.Close)
	return f
}

func (f *devFixture) do(tok, method, path string, body any) (int, []byte, http.Header) {
	f.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, f.srv.URL+path, rd)
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b, res.Header
}

func (f *devFixture) json(tok, method, path string, body any, want int, out any) {
	f.t.Helper()
	code, b, _ := f.do(tok, method, path, body)
	if code != want {
		f.t.Fatalf("%s %s = %d, want %d: %s", method, path, code, want, b)
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			f.t.Fatalf("%s %s: %v: %s", method, path, err, b)
		}
	}
}

func (f *devFixture) waitSend(tok string, id int64, want string) store.Send {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var list []store.Send
		f.json(tok, "GET", "/api/v1/sends", nil, 200, &list)
		for _, x := range list {
			if x.ID == id && x.Status == want {
				return x
			}
			if x.ID == id && time.Now().After(deadline) {
				f.t.Fatalf("send %d is %s (%s), want %s", id, x.Status, x.Error, want)
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestDevicesCRUDAndIsolation(t *testing.T) {
	f := newDevFixture(t)
	f.json(f.anaTok, "POST", "/api/v1/devices", map[string]any{"kind": "kindle", "email": "ana@gmail.com"}, 400, nil)
	f.json(f.anaTok, "POST", "/api/v1/devices", map[string]any{"kind": "kobo"}, 400, nil)
	var k struct {
		Device store.Device `json:"device"`
		Setup  DeviceSetup  `json:"setup"`
	}
	f.json(f.anaTok, "POST", "/api/v1/devices", map[string]any{"kind": "kindle", "email": "Ana_77@Kindle.com"}, 201, &k)
	if k.Device.Email != "ana_77@kindle.com" || !k.Device.IsDefault || len(k.Setup.Steps) == 0 {
		t.Fatalf("kindle %+v", k)
	}
	var cp struct {
		Device   store.Device `json:"device"`
		Password string       `json:"password"`
		Username string       `json:"username"`
		Setup    DeviceSetup  `json:"setup"`
	}
	f.json(f.anaTok, "POST", "/api/v1/devices", map[string]any{"kind": "crosspoint", "name": "X4", "settings": map[string]any{"folder": "Books/Pocket"}}, 201, &cp)
	if cp.Password == "" || cp.Username != "ana@example.com" || cp.Setup.OPDSURL != "https://library.example.com/opds" || cp.Device.Settings["folder"] != "/Books/Pocket" || cp.Device.TokenID == 0 {
		t.Fatalf("crosspoint %+v", cp)
	}
	// the app password signs in like a device
	if u, _, err := f.st.GetUserByLogin(context.Background(), "ana", cp.Password, false); err != nil || u.ID != f.ana.ID {
		t.Fatalf("login with app password: %v", err)
	}
	var again struct {
		Password string `json:"password"`
	}
	f.json(f.anaTok, "POST", "/api/v1/devices/"+itoa(cp.Device.ID)+"/password", nil, 200, &again)
	if _, _, err := f.st.GetUserByLogin(context.Background(), "ana@example.com", cp.Password, false); err == nil {
		t.Fatal("the old app password still works")
	}
	if _, _, err := f.st.GetUserByLogin(context.Background(), "ana@example.com", again.Password, false); err != nil {
		t.Fatal("the new app password does not work")
	}

	// Bob sees none of it
	var bobs []store.Device
	f.json(f.bobTok, "GET", "/api/v1/devices", nil, 200, &bobs)
	if len(bobs) != 0 {
		t.Fatalf("bob sees %+v", bobs)
	}
	id := itoa(k.Device.ID)
	f.json(f.bobTok, "GET", "/api/v1/devices/"+id+"/setup", nil, 404, nil)
	f.json(f.bobTok, "PUT", "/api/v1/devices/"+id, map[string]any{"name": "mine"}, 404, nil)
	f.json(f.bobTok, "POST", "/api/v1/devices/"+id+"/test", nil, 404, nil)
	f.json(f.bobTok, "DELETE", "/api/v1/devices/"+id, nil, 404, nil)
	f.json(f.bobTok, "POST", "/api/v1/books/"+itoa(f.book)+"/send", map[string]any{"device_id": k.Device.ID}, 404, nil)

	f.json(f.anaTok, "DELETE", "/api/v1/devices/"+itoa(cp.Device.ID), nil, 204, nil)
	if _, _, err := f.st.GetUserByLogin(context.Background(), "ana", again.Password, false); err == nil {
		t.Fatal("a removed device's password still works")
	}
}

func TestSendToKindle(t *testing.T) {
	f := newDevFixture(t)
	smtpd, err := mailtest.Start("relay", "pw")
	if err != nil {
		t.Fatal(err)
	}
	defer smtpd.Close()
	port, _ := strconv.Atoi(smtpd.Port())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.s.sendWorker(ctx)

	var dev struct {
		Device store.Device `json:"device"`
	}
	f.json(f.anaTok, "POST", "/api/v1/devices", map[string]any{"kind": "kindle", "email": "ana@kindle.com"}, 201, &dev)
	// no mail yet: refused up front
	code, body, _ := f.do(f.anaTok, "POST", "/api/v1/books/"+itoa(f.book)+"/send", map[string]any{})
	if code != 400 || !strings.Contains(string(body), "Settings → Mail") {
		t.Fatalf("send without mail = %d %s", code, body)
	}
	f.json(f.anaTok, "PUT", "/api/v1/settings", map[string]any{"smtp_host": "127.0.0.1", "smtp_port": port, "smtp_tls": "none", "smtp_user": "relay", "smtp_password": "pw",
		"smtp_from": "kindle@send.example.com", "smtp_from_name": "Library"}, 200, nil)
	var x store.Send
	f.json(f.anaTok, "POST", "/api/v1/books/"+itoa(f.book)+"/send", map[string]any{}, 201, &x)
	if x.Method != "email" || x.Format != "epub" || x.Status != "queued" {
		t.Fatalf("send %+v", x)
	}
	f.waitSend(f.anaTok, x.ID, "sent")
	msgs := smtpd.Messages()
	if len(msgs) != 1 || msgs[0].To[0] != "ana@kindle.com" {
		t.Fatalf("mail %+v", msgs)
	}
	d := msgs[0].Data
	if !strings.Contains(d, `filename="Ivo Andric - Na Drini cuprija.epub"`) {
		t.Fatalf("attachment name: %s", d[:min(2000, len(d))])
	}
	// the attachment carries the catalogue's metadata and Amazon's fixes
	att := attachment(t, d)
	zr, err := zip.NewReader(bytes.NewReader(att), int64(len(att)))
	if err != nil {
		t.Fatal(err)
	}
	opf := zipText(t, zr, "OEBPS/content.opf")
	for _, want := range []string{"Na Drini ćuprija", "Ivo Andrić", "<dc:language>sr</dc:language>", "Bosnian trilogy"} {
		if !strings.Contains(opf, want) {
			t.Errorf("sent EPUB lacks %q:\n%s", want, opf)
		}
	}
	// Bob cannot see or cancel Ana's sends
	var bobs []store.Send
	f.json(f.bobTok, "GET", "/api/v1/sends", nil, 200, &bobs)
	if len(bobs) != 0 {
		t.Fatal("bob sees ana's sends")
	}
	f.json(f.bobTok, "DELETE", "/api/v1/sends/"+itoa(x.ID), nil, 404, nil)
	f.json(f.bobTok, "POST", "/api/v1/sends/"+itoa(x.ID)+"/retry", nil, 404, nil)
}

func attachment(t *testing.T, msg string) []byte {
	t.Helper()
	i := strings.Index(msg, "Content-Type: application/epub+zip")
	if i < 0 {
		t.Fatal("no EPUB attachment")
	}
	rest := msg[i:]
	start := strings.Index(rest, "\r\n\r\n") + 4
	end := strings.Index(rest[start:], "--")
	data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(rest[start:start+end], "\r\n", ""))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func zipText(t *testing.T, zr *zip.Reader, name string) string {
	t.Helper()
	for _, f := range zr.File {
		if f.Name == name {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			return string(b)
		}
	}
	t.Fatalf("no %s", name)
	return ""
}

func TestSendToCrossPoint(t *testing.T) {
	f := newDevFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go f.s.sendWorker(ctx)
	var put []byte
	var putPath string
	reader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "MKCOL":
			w.WriteHeader(201)
		case http.MethodPut:
			putPath = r.URL.Path
			put, _ = io.ReadAll(r.Body)
			w.WriteHeader(201)
		default:
			w.Write([]byte(`{"version":"1.6.5","mode":"STA","device":"X4","ip":"10.0.0.9"}`))
		}
	}))
	defer reader.Close()
	var dev struct {
		Device store.Device `json:"device"`
	}
	f.json(f.anaTok, "POST", "/api/v1/devices", map[string]any{"kind": "crosspoint", "address": reader.URL, "settings": map[string]any{"push": true}}, 201, &dev)
	var test map[string]any
	f.json(f.anaTok, "POST", "/api/v1/devices/"+itoa(dev.Device.ID)+"/test", nil, 200, &test)
	if test["ok"] != true {
		t.Fatalf("test %v", test)
	}
	var x store.Send
	f.json(f.anaTok, "POST", "/api/v1/books/"+itoa(f.book)+"/send", map[string]any{"device_id": dev.Device.ID}, 201, &x)
	if x.Method != "push" {
		t.Fatalf("method %s", x.Method)
	}
	f.waitSend(f.anaTok, x.ID, "sent")
	if putPath != "/Books/Ivo Andrić - Na Drini ćuprija.epub" || len(put) < 100 {
		t.Fatalf("put %q (%d bytes)", putPath, len(put))
	}
	// an unreachable reader: the book waits in its catalogue instead
	reader.Close()
	var x2 store.Send
	f.json(f.anaTok, "POST", "/api/v1/books/"+itoa(f.book)+"/send", map[string]any{"device_id": dev.Device.ID}, 201, &x2)
	got := f.waitSend(f.anaTok, x2.ID, "queued")
	for got.Method != "pull" {
		time.Sleep(50 * time.Millisecond)
		got = f.waitSend(f.anaTok, x2.ID, "queued")
	}
	if !strings.Contains(got.Error, "To send") {
		t.Fatalf("fallback %+v", got)
	}
	var devs []store.Device
	f.json(f.anaTok, "GET", "/api/v1/devices", nil, 200, &devs)
	if devs[0].Pending != 1 {
		t.Fatalf("pending = %d", devs[0].Pending)
	}
	// a second send of the same book doesn't queue it twice
	var x3 store.Send
	f.json(f.anaTok, "PUT", "/api/v1/devices/"+itoa(dev.Device.ID), map[string]any{"settings": map[string]any{"push": false}}, 200, nil)
	f.json(f.anaTok, "POST", "/api/v1/books/"+itoa(f.book)+"/send", map[string]any{"device_id": dev.Device.ID}, 201, &x3)
	if x3.ID != x2.ID {
		t.Fatalf("queued twice: %d vs %d", x3.ID, x2.ID)
	}
	f.json(f.anaTok, "DELETE", "/api/v1/sends/"+itoa(x3.ID), nil, 204, nil)
	f.json(f.anaTok, "DELETE", "/api/v1/sends/"+itoa(x.ID), nil, 409, nil)
}

func TestDownload(t *testing.T) {
	f := newDevFixture(t)
	code, body, h := f.do(f.anaTok, "GET", "/api/v1/books/"+itoa(f.book)+"/download", nil)
	if code != 200 || !strings.Contains(h.Get("Content-Disposition"), `filename="Ivo Andric - Na Drini cuprija.epub"; filename*=UTF-8''Ivo%20Andri%C4%87`) || h.Get("Content-Type") != "application/epub+zip" {
		t.Fatalf("download %d %v", code, h)
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if opf := zipText(t, zr, "OEBPS/content.opf"); !strings.Contains(opf, "Na Drini ćuprija") || strings.Contains(opf, "Old title") {
		t.Fatalf("metadata not embedded:\n%s", opf)
	}
	// the original on disk is untouched
	b, _ := f.st.GetBookAny(context.Background(), f.book)
	orig, _ := os.ReadFile(b.Files[0].Path)
	oz, err := zip.NewReader(bytes.NewReader(orig), int64(len(orig)))
	if err != nil || !strings.Contains(zipText(t, oz, "OEBPS/content.opf"), "Old title") {
		t.Fatal("original changed")
	}
	// ranges
	req, _ := http.NewRequest("GET", f.srv.URL+"/api/v1/files/"+itoa(f.file)+"/download", nil)
	req.Header.Set("Authorization", "Bearer "+f.anaTok)
	req.Header.Set("Range", "bytes=0-9")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	part, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 206 || len(part) != 10 || string(part[:2]) != "PK" {
		t.Fatalf("range %d %q", res.StatusCode, part)
	}
	// converting without calibre says so
	code, body, _ = f.do(f.anaTok, "GET", "/api/v1/books/"+itoa(f.book)+"/download?format=azw3", nil)
	if code != 400 || !strings.Contains(string(body), "calibre") {
		t.Fatalf("azw3 = %d %s", code, body)
	}
	code, body, _ = f.do(f.anaTok, "GET", "/api/v1/books/"+itoa(f.book)+"/download?format=kepub", nil)
	if code != 200 || !bytes.Contains(body, []byte("PK")) {
		t.Fatalf("kepub = %d", code)
	}
}

func TestDownloadHiddenLibrary(t *testing.T) {
	f := newDevFixture(t)
	ctx := context.Background()
	b, _ := f.st.GetBookAny(ctx, f.book)
	lib, _ := f.st.GetLibrary(ctx, b.LibraryID)
	lib.Access, lib.People = "people", []int64{f.ana.ID}
	if err := f.st.UpdateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	f.json(f.bobTok, "GET", "/api/v1/books/"+itoa(f.book)+"/download", nil, 404, nil)
	f.json(f.bobTok, "GET", "/api/v1/files/"+itoa(f.file)+"/download", nil, 404, nil)
}

func TestImportClippings(t *testing.T) {
	f := newDevFixture(t)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "My Clippings.txt")
	fw.Write([]byte("Na Drini ćuprija (Andrić, Ivo)\r\n- Your Highlight on page 21 | Location 312-314 | Added on Sunday, 3 May 2021 21:14:02\r\n\r\nNa Drini je most.\r\n==========\r\n" +
		"Unknown Book (Nobody)\r\n- Your Highlight on page 1 | Location 1-2 | Added on Sunday, 3 May 2021 21:14:02\r\n\r\nText.\r\n==========\r\n"))
	mw.Close()
	post := func() map[string]any {
		req, _ := http.NewRequest("POST", f.srv.URL+"/api/v1/import/kindle-clippings", bytes.NewReader(body.Bytes()))
		req.Header.Set("Authorization", "Bearer "+f.anaTok)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != 200 {
			t.Fatalf("import %d %v", res.StatusCode, out)
		}
		return out
	}
	out := post()
	counts := out["counts"].(map[string]any)
	if counts["new"] != float64(1) || counts["unmatched"] != float64(1) {
		t.Fatalf("import %v", out)
	}
	out = post() // again: nothing new
	if c := out["counts"].(map[string]any); c["new"] != nil || c["updated"] != float64(1) {
		t.Fatalf("re-import %v", out)
	}
	list, _ := f.st.Annotations(context.Background(), f.ana.ID, store.AnnotationFilter{BookID: f.book})
	if len(list) != 1 || list[0].Source != "kindle" || list[0].Page != 21 {
		t.Fatalf("annotations %+v", list)
	}
	if bl, _ := f.st.Annotations(context.Background(), f.bob.ID, store.AnnotationFilter{}); len(bl) != 0 {
		t.Fatal("bob got ana's clippings")
	}
}
