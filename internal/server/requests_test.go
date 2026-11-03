package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

// "Get it": the book page asks Pocket Requests (through Core, as the person)
// about the audiobook and the book in the box's other languages, and asks for
// them; the author page lists the author's other books.
func TestGetItThroughRequests(t *testing.T) {
	f := newDevFixture(t)
	var mu sync.Mutex
	var posted []map[string]any
	asked := []map[string]any{}
	rq := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer pcx_rq_ana" {
			http.Error(w, `{"error":"no"}`, http.StatusUnauthorized)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		enc := json.NewEncoder(w)
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1/books/lookup":
			q := r.URL.Query()
			if q.Get("title") != "Na Drini ćuprija" || q.Get("author") != "Ivo Andrić" {
				_ = enc.Encode(map[string]any{"title": nil, "languages_wanted": []string{"en", "sr"}})
				return
			}
			_ = enc.Encode(map[string]any{"languages_wanted": []string{"en", "sr", "hr"}, "can_get": map[string]any{"ok": true},
				"title": map[string]any{"kind": "book", "book_key": "ol:OL258416W", "title": "Na Drini ćuprija",
					"book": map[string]any{"have": map[string]any{"ebook": map[string]any{"url": "https://library.example.com/book/1"}, "audiobook": nil},
						"asked": asked}}})
		case r.Method == "GET" && r.URL.Path == "/api/v1/books/author":
			if r.URL.Query().Get("name") != "Ivo Andrić" {
				http.NotFound(w, r)
				return
			}
			_ = enc.Encode(map[string]any{"can_get": map[string]any{"ok": true}, "items": []any{
				map[string]any{"kind": "book", "book_key": "ol:OL258416W", "title": "Na Drini ćuprija", "year": 1945, "poster": "c1",
					"book": map[string]any{"have": map[string]any{}, "asked": []any{}}},
				map[string]any{"kind": "book", "book_key": "ol:OL1W", "title": "Travnička hronika", "year": 1945, "poster": "c2",
					"book": map[string]any{"have": map[string]any{}, "asked": asked}}}})
		case r.Method == "POST" && r.URL.Path == "/api/v1/requests":
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			posted = append(posted, in)
			for _, a := range asked {
				if a["kind"] == in["kind"] && a["language"] == in["language"] {
					w.WriteHeader(http.StatusConflict)
					_ = enc.Encode(map[string]any{"error": "You already asked for it.", "request_id": a["id"]})
					return
				}
			}
			id := 50 + len(asked)
			lang, _ := in["language"].(string)
			asked = append(asked, map[string]any{"id": id, "kind": in["kind"], "language": lang, "status": "approved", "state": "searching"})
			w.WriteHeader(http.StatusCreated)
			_ = enc.Encode(map[string]any{"id": id, "kind": in["kind"], "status": "approved", "state": "sent"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(rq.Close)
	fakeCore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/links/token" {
			http.NotFound(w, r)
			return
		}
		var in struct {
			User   string   `json:"user"`
			App    string   `json:"app"`
			Scopes []string `json:"scopes"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.App != "requests" || strings.Join(in.Scopes, " ") != "requests:read requests:write" || in.User != "ana@example.com" {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "switched off", "reason": "off_person"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "pcx_rq_ana", "expires_in": 300, "url": rq.URL, "public_url": "https://get.example.com"})
	}))
	t.Cleanup(fakeCore.Close)
	f.s.core = core.New(fakeCore.URL, core.Options{DB: f.st.DB, TokenFile: f.dir + "/core.token"})
	f.s.coreMu.Lock()
	f.s.coreOK = true
	f.s.coreMu.Unlock()

	path := fmt.Sprintf("/api/v1/books/%d/requests", f.book)
	var g getIt
	f.json(f.anaTok, "GET", path, nil, http.StatusOK, &g)
	// the book is Serbian: other languages are English and Croatian
	if !g.Available || !g.Found || g.BookKey != "ol:OL258416W" || g.Language != "sr" || len(g.Languages) != 2 ||
		g.Languages[0].Code != "en" || g.Languages[1].Code != "hr" || g.Audiobook.Have != nil || g.Audiobook.Request != nil {
		t.Fatalf("first look: %+v", g)
	}
	var made rqAsk
	f.json(f.anaTok, "POST", path, map[string]any{"kind": "ebook", "language": "eng"}, http.StatusCreated, &made)
	if made.URL != "https://get.example.com/requests/50" {
		t.Fatalf("made %+v", made)
	}
	if p := posted[0]; p["book_key"] != "ol:OL258416W" || p["language"] != "en" || p["author"] != "Ivo Andrić" {
		t.Fatalf("posted %v", p)
	}
	f.json(f.anaTok, "POST", path, map[string]any{"kind": "audiobook"}, http.StatusCreated, nil)
	// the cache was dropped: English and the audiobook now show as asked for
	f.json(f.anaTok, "GET", path, nil, http.StatusOK, &g)
	if g.Languages[0].Request == nil || g.Languages[0].Request.URL != "https://get.example.com/requests/50" || g.Languages[1].Request != nil || g.Audiobook.Request == nil {
		t.Fatalf("after asking: %+v", g)
	}
	// twice: the 409 comes through with the request's page
	code, body, _ := f.do(f.anaTok, "POST", path, map[string]any{"kind": "ebook", "language": "en"})
	if code != http.StatusConflict || !strings.Contains(string(body), "/requests/50") {
		t.Fatalf("twice: %d %s", code, body)
	}
	if code, _, _ := f.do(f.anaTok, "POST", path, map[string]any{"kind": "film"}); code != http.StatusBadRequest {
		t.Fatalf("bad kind: %d", code)
	}

	// the author page: what the library has is marked
	var authors struct {
		Items []store.Author `json:"items"`
	}
	f.json(f.anaTok, "GET", "/api/v1/authors", nil, http.StatusOK, &authors)
	var aid int64
	for _, a := range authors.Items {
		if a.Name == "Ivo Andrić" {
			aid = a.ID
		}
	}
	var more authorMore
	f.json(f.anaTok, "GET", fmt.Sprintf("/api/v1/authors/%d/requests-more", aid), nil, http.StatusOK, &more)
	if !more.Available || len(more.Items) != 2 || !more.Items[0].InHere || more.Items[0].LocalID != f.book || more.Items[1].InHere ||
		more.Items[1].URL != "https://get.example.com/title/book/ol:OL1W" {
		t.Fatalf("more: %+v", more)
	}
	f.json(f.anaTok, "POST", fmt.Sprintf("/api/v1/authors/%d/requests", aid), map[string]any{"book_key": "ol:OL1W", "title": "Travnička hronika", "kind": "ebook", "language": "sr"}, http.StatusCreated, nil)
	if p := posted[len(posted)-1]; p["book_key"] != "ol:OL1W" || p["author"] != "Ivo Andrić" || p["language"] != "sr" {
		t.Fatalf("author posted %v", p)
	}

	// Bob's link is off: greyed out with the reason, not an error
	f.json(f.bobTok, "GET", path, nil, http.StatusOK, &g)
	if g.Available || !strings.Contains(g.Reason, "Apps acting for me") {
		t.Fatalf("bob: %+v", g)
	}
	f.json(f.bobTok, "GET", fmt.Sprintf("/api/v1/authors/%d/requests-more", aid), nil, http.StatusOK, &more)
	if more.Available || more.Reason == "" {
		t.Fatalf("bob more: %+v", more)
	}
	_ = context.Background
}

func TestLangCode(t *testing.T) {
	for in, want := range map[string]string{"eng": "en", "srp": "sr", "sr-Latn": "sr", "Serbian": "sr", "hr": "hr", "en-US": "en", "": "", "klingon": ""} {
		if got := langCode(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
