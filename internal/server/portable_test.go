package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

// moveFixture puts Library behind a fake Core whose pcx_<ana|bob>_<export|
// import|read> tokens are Pocket Move links acting for that person.
func moveFixture(t *testing.T) *devFixture {
	t.Helper()
	f := newDevFixture(t)
	f.s.cfg.MaxUploadBytes = 50 << 20
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/introspect" {
			http.NotFound(w, r)
			return
		}
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		parts := strings.SplitN(strings.TrimPrefix(in["token"], "pcx_"), "_", 2)
		if len(parts) != 2 {
			_ = json.NewEncoder(w).Encode(map[string]any{"active": false})
			return
		}
		person := f.bob
		if parts[0] == "ana" {
			person = f.ana
		}
		scope := map[string]string{"export": core.ScopeMoveExport, "import": core.ScopeMoveImport, "read": "reading:read"}[parts[1]]
		_ = json.NewEncoder(w).Encode(map[string]any{"active": true, "kind": "delegated", "scope": scope, "audience": "library",
			"user": map[string]any{"id": person.ID, "email": person.Email, "name": person.Name, "role": "member"}, "app": map[string]any{"id": 7, "slug": "move"}})
	}))
	t.Cleanup(fake.Close)
	f.s.core = core.New(fake.URL, core.Options{DB: f.st.DB, Token: "synthetic-application-token"})
	return f
}

func (f *devFixture) raw(tok, method, path, ctype string, body []byte) (int, []byte) {
	f.t.Helper()
	req, _ := http.NewRequest(method, f.srv.URL+path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

func (f *devFixture) moveImport(tok, path string, data []byte, multi bool) map[string]any {
	f.t.Helper()
	ctype, body := "application/json", data
	if multi {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile("file", "My Clippings.txt")
		_, _ = fw.Write(data)
		_ = mw.Close()
		ctype, body = mw.FormDataContentType(), buf.Bytes()
	}
	code, out := f.raw(tok, "POST", path, ctype, body)
	if code != 200 {
		f.t.Fatalf("POST %s → %d %s", path, code, out)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	return m
}

func numOf(m map[string]any, k string) float64 { v, _ := m[k].(float64); return v }

func TestMoveLinkScopes(t *testing.T) {
	f := moveFixture(t)
	exp, imp, read := "pcx_bob_export", "pcx_bob_import", "pcx_bob_read"
	for _, c := range []struct {
		tok, method, path string
		want              int
	}{
		{exp, "GET", core.PortablePath, 200},
		{exp, "GET", "/api/v1/export.json", 200},
		{exp, "GET", "/api/v1/annotations/export", 200},
		{exp, "POST", "/api/v1/import", 403},
		{exp, "POST", "/api/v1/import/kindle-clippings", 403},
		{exp, "GET", "/api/v1/settings", 403},
		{exp, "GET", "/api/v1/books", 403},
		{exp, "GET", "/api/v1/backup", 403},
		{imp, "GET", core.PortablePath, 200},
		{imp, "GET", "/api/v1/export.json", 403},
		{imp, "POST", "/api/v1/import/kindle-clippings", 400}, // reachable; no file sent
		{imp, "POST", "/api/v1/import/calibre-web", 403},
		{imp, "PUT", "/api/v1/settings", 403},
		{imp, "POST", "/api/v1/shelves", 403},
		{read, "GET", core.PortablePath, 403},
		{read, "GET", "/api/v1/export.json", 403},
	} {
		if code, body := f.raw(c.tok, c.method, c.path, "application/json", []byte("{}")); code != c.want {
			t.Errorf("%s %s %s → %d, want %d (%s)", c.tok, c.method, c.path, code, c.want, body)
		}
	}
	_, body := f.raw(exp, "GET", core.PortablePath, "", nil)
	var p core.Portable
	if err := json.Unmarshal(body, &p); err != nil || len(p.Exports) != 2 || len(p.Imports) != 2 || p.Exports[0].Kind != core.KindPocket || p.Imports[1].Form != "file" {
		t.Fatalf("portable: %s", body)
	}
}

// Move brings Ana's reading to Bob: each import adds once, and nothing of
// one person shows up for the other.
func TestMoveImportsAddOnce(t *testing.T) {
	f := moveFixture(t)
	ctx := context.Background()
	if _, _, err := f.st.SetStatus(ctx, f.ana.ID, f.book, "read", "2026-01-02"); err != nil {
		t.Fatal(err)
	}
	rating := 8
	_, _ = f.st.UpdateUserBook(ctx, f.ana.ID, f.book, &rating, nil, nil)
	_, _ = f.st.SaveAnnotation(ctx, f.ana.ID, &store.Annotation{BookID: f.book, Kind: "highlight", Text: "The bridge stood", Source: "web"})
	sh := store.Shelf{Name: "Bosnia", Kind: "manual", OPDS: true}
	if err := f.st.CreateShelf(ctx, f.ana.ID, &sh); err != nil {
		t.Fatal(err)
	}
	_ = f.st.AddToShelf(ctx, f.ana.ID, sh.ID, []int64{f.book})

	code, export := f.raw("pcx_ana_export", "GET", "/api/v1/export.json", "", nil)
	if code != 200 || !strings.Contains(string(export), "The bridge stood") || !strings.Contains(string(export), "Bosnia") {
		t.Fatalf("ana's export: %d %s", code, export)
	}
	if code, md := f.raw("pcx_ana_export", "GET", "/api/v1/annotations/export", "", nil); code != 200 || !strings.Contains(string(md), "The bridge stood") {
		t.Fatalf("ana's highlights: %d %s", code, md)
	}
	if _, bob := f.raw("pcx_bob_export", "GET", "/api/v1/export.json", "", nil); strings.Contains(string(bob), "The bridge stood") || strings.Contains(string(bob), "Bosnia") {
		t.Fatalf("bob's export holds ana's reading: %s", bob)
	}

	first := f.moveImport("pcx_bob_import", "/api/v1/import", export, false)
	if numOf(first, "books") != 1 || numOf(first, "annotations") != 1 || numOf(first, "shelves") != 1 {
		t.Fatalf("first import: %v", first)
	}
	second := f.moveImport("pcx_bob_import", "/api/v1/import", export, false)
	if numOf(second, "books")+numOf(second, "annotations")+numOf(second, "shelves") != 0 {
		t.Fatalf("second import added: %v", second)
	}
	ub, _ := f.st.GetUserBook(ctx, f.bob.ID, f.book)
	reads, _ := f.st.Reads(ctx, f.bob.ID, f.book, "", "")
	if ub.Status != "read" || ub.Rating != 8 || len(reads) != 1 || reads[0].Day != "2026-01-02" {
		t.Fatalf("bob's state: %+v, reads %+v", ub, reads)
	}
	bobAnns, _ := f.st.Annotations(ctx, f.bob.ID, store.AnnotationFilter{})
	anaAnns, _ := f.st.Annotations(ctx, f.ana.ID, store.AnnotationFilter{})
	if len(bobAnns) != 1 || len(anaAnns) != 1 {
		t.Fatalf("annotations: bob %d, ana %d", len(bobAnns), len(anaAnns))
	}
	if anaShelves, _ := f.st.ListShelves(ctx, f.ana.ID); len(anaShelves) != 1 {
		t.Fatalf("ana's shelves changed: %d", len(anaShelves))
	}

	clips := []byte("Na Drini ćuprija (Andrić, Ivo)\r\n- Your Highlight on page 21 | Location 312-314 | Added on Sunday, 3 May 2021 21:14:02\r\n\r\nA bridge over the Drina.\r\n==========\r\n")
	c1 := f.moveImport("pcx_bob_import", "/api/v1/import/kindle-clippings", clips, true)
	c2 := f.moveImport("pcx_bob_import", "/api/v1/import/kindle-clippings", clips, true)
	if numOf(c1, "imported") != 1 || numOf(c2, "imported") != 0 {
		t.Fatalf("clippings: %v then %v", c1, c2)
	}
	if bobAnns, _ := f.st.Annotations(ctx, f.bob.ID, store.AnnotationFilter{}); len(bobAnns) != 2 {
		t.Fatalf("bob's annotations after clippings twice: %d", len(bobAnns))
	}
	if anaAnns, _ := f.st.Annotations(ctx, f.ana.ID, store.AnnotationFilter{}); len(anaAnns) != 1 {
		t.Fatalf("ana's annotations changed: %d", len(anaAnns))
	}
}
