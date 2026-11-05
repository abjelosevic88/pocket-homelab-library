package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// fakeBookshelf keeps links the way Pocket Bookshelf does: library book id →
// its book id, 0 for "unlinked".
type fakeBookshelf struct {
	mu      sync.Mutex
	links   map[string]int64
	applied []map[string]any
}

func (fb *fakeBookshelf) applies() []map[string]any {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	return append([]map[string]any(nil), fb.applied...)
}

func TestBookshelfLinking(t *testing.T) {
	f := newDevFixture(t)
	fb := &fakeBookshelf{links: map[string]int64{}}
	linkRE := regexp.MustCompile(`^/api/v1/library/links/(\d+)(/apply)?$`)
	shelf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fb.mu.Lock()
		defer fb.mu.Unlock()
		enc := json.NewEncoder(w)
		book := func(id int64) map[string]any {
			return map[string]any{"id": id, "title": "Na Drini ćuprija", "authors": "Ivo Andrić", "status": "want", "rating": 4.5, "url": fmt.Sprintf("https://shelf.example.com/books/%d", id)}
		}
		answer := func(lib string) {
			id, ok := fb.links[lib]
			switch {
			case !ok:
				_ = enc.Encode(map[string]any{"linked": false})
			case id == 0:
				_ = enc.Encode(map[string]any{"linked": false, "unlinked": true})
			default:
				_ = enc.Encode(map[string]any{"linked": true, "book": book(id)})
			}
		}
		if r.Method == "GET" && r.URL.Path == "/api/v1/books" {
			if r.URL.Query().Get("q") == "Drina" {
				_ = enc.Encode(map[string]any{"items": []any{book(7)}})
				return
			}
			_ = enc.Encode(map[string]any{"items": []any{}})
			return
		}
		m := linkRE.FindStringSubmatch(r.URL.Path)
		if m == nil {
			http.NotFound(w, r)
			return
		}
		lib := m[1]
		switch {
		case r.Method == "GET":
			answer(lib)
		case r.Method == "PUT":
			var in struct {
				BookID int64 `json:"book_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			fb.links[lib] = in.BookID
			answer(lib)
		case r.Method == "DELETE":
			fb.links[lib] = 0
			answer(lib)
		case r.Method == "POST" && m[2] != "":
			if id, ok := fb.links[lib]; ok && id == 0 {
				w.WriteHeader(http.StatusConflict)
				_ = enc.Encode(map[string]any{"error": "unlinked"})
				return
			}
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			fb.applied = append(fb.applied, in)
			if _, ok := fb.links[lib]; !ok {
				fb.links[lib] = 3
			}
			answer(lib)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(shelf.Close)
	fakeCore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			App    string   `json:"app"`
			Scopes []string `json:"scopes"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if r.URL.Path != "/api/v1/links/token" || in.App != "bookshelf" {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "no", "reason": "not_declared"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "pcx_bs", "expires_in": 300, "url": shelf.URL, "public_url": "https://shelf.example.com"})
	}))
	t.Cleanup(fakeCore.Close)
	f.s.core = core.New(fakeCore.URL, core.Options{DB: f.st.DB, TokenFile: f.dir + "/core.token"})
	f.s.coreMu.Lock()
	f.s.coreOK = true
	f.s.coreMu.Unlock()

	bookPath := fmt.Sprintf("/api/v1/books/%d", f.book)
	shelfOf := func() map[string]any {
		bsForget(f.ana.ID, f.book)
		var b struct {
			Bookshelf map[string]any `json:"bookshelf"`
		}
		f.json(f.anaTok, "GET", bookPath, nil, http.StatusOK, &b)
		return b.Bookshelf
	}
	waitApplies := func(n int) []map[string]any {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if a := fb.applies(); len(a) >= n {
				return a
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("expected %d applies, got %d", n, len(fb.applies()))
		return nil
	}

	if bs := shelfOf(); bs["available"] != false || bs["linked"] != false || bs["can_write"] != true {
		t.Fatalf("not on the shelves yet: %v", bs)
	}

	// A status set here goes there, with the book, and adds it.
	f.json(f.anaTok, "PUT", bookPath+"/state", map[string]any{"status": "want"}, http.StatusOK, nil)
	a := waitApplies(1)
	if a[0]["status"] != "want" || a[0]["rating"] != nil || a[0]["book"].(map[string]any)["title"] != "Na Drini ćuprija" {
		t.Fatalf("first apply: %v", a[0])
	}
	if bs := shelfOf(); bs["linked"] != true || bs["id"] != float64(3) || bs["rating"] != float64(9) || bs["url"] != "https://shelf.example.com/books/3" {
		t.Fatalf("linked: %v", bs)
	}

	// A rating on a linked book carries no status (it never moves the one kept there).
	f.json(f.anaTok, "PUT", bookPath+"/state", map[string]any{"rating": 8}, http.StatusOK, nil)
	a = waitApplies(2)
	if a[1]["rating"] != float64(4) || a[1]["status"] != nil {
		t.Fatalf("rating apply: %v", a[1])
	}

	// Unlinked: Bookshelf is left alone.
	var out map[string]any
	f.json(f.anaTok, "DELETE", bookPath+"/bookshelf", nil, http.StatusOK, &out)
	if out["unlinked"] != true || out["linked"] != false {
		t.Fatalf("unlink: %v", out)
	}
	f.json(f.anaTok, "PUT", bookPath+"/state", map[string]any{"rating": 6}, http.StatusOK, nil)
	time.Sleep(200 * time.Millisecond)
	if n := len(fb.applies()); n != 2 {
		t.Fatalf("an unlinked book was written to: %d applies", n)
	}

	// Linked by hand to a book found there.
	var hits []map[string]any
	f.json(f.anaTok, "GET", bookPath+"/bookshelf/search?q=Drina", nil, http.StatusOK, &hits)
	if len(hits) != 1 || hits[0]["id"] != float64(7) || hits[0]["rating"] != float64(9) {
		t.Fatalf("search: %v", hits)
	}
	f.json(f.anaTok, "PUT", bookPath+"/bookshelf", map[string]any{"bookshelf_id": 7}, http.StatusOK, &out)
	if out["linked"] != true || out["id"] != float64(7) {
		t.Fatalf("link: %v", out)
	}
	f.json(f.anaTok, "PUT", bookPath+"/bookshelf", map[string]any{}, http.StatusBadRequest, nil)

	if !strings.HasPrefix(bsStatus("dnf"), "abandoned") || bsStatus("") != "" {
		t.Fatal("status mapping")
	}
}
