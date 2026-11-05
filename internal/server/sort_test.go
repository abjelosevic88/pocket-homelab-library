package server

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

// fakeSortAI answers like Pocket AI: programming to tech, a nickname author to review, the rest to books.
type fakeSortAI struct{ prompts []string }

func (f *fakeSortAI) Complete(_ context.Context, system, prompt string, jsonMode bool) (string, error) {
	f.prompts = append(f.prompts, prompt)
	var in struct {
		Books []struct {
			ID      int64    `json:"id"`
			Title   string   `json:"title"`
			Authors []string `json:"authors"`
			Checks  []string `json:"checks"`
		} `json:"books"`
	}
	_ = json.Unmarshal([]byte(prompt), &in)
	out := []map[string]any{}
	for _, b := range in.Books {
		t, why := "books", "a novel"
		switch {
		case strings.Contains(b.Title, "Python"):
			t, why = "tech", "programming"
		case len(b.Checks) > 0:
			t, why = "review", b.Checks[0]
		}
		out = append(out, map[string]any{"id": b.ID, "target": t, "confidence": 0.9, "reason": why})
	}
	j, _ := json.Marshal(map[string]any{"books": out})
	return "Here you go: " + string(j), nil
}

func TestSortNewBooks(t *testing.T) {
	f := newScanFixture(t)
	ctx := context.Background()
	ai := &fakeSortAI{}
	f.srv.sorting.model = func(context.Context, string) (aiModel, error) { return ai, nil }
	dir := filepath.Dir(f.lib.Paths[0])
	unsorted := &store.Library{Name: "Unsorted", Kind: "books", Paths: []string{filepath.Join(dir, "unsorted")}}
	tech := &store.Library{Name: "Tech books", Kind: "books", Paths: []string{filepath.Join(dir, "tech")}}
	for _, l := range []*store.Library{unsorted, tech} {
		os.MkdirAll(l.Paths[0], 0o755)
		if err := f.st.CreateLibrary(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	mk := func(lib *store.Library, rel, title, author string) int64 {
		p := filepath.Join(lib.Paths[0], rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, testEPUB(title, author), 0o644)
		id, _ := f.st.CreateBook(ctx, lib.ID, p, store.BookMeta{Title: title, Authors: []string{author}})
		f.st.UpsertFile(ctx, &store.File{BookID: id, LibraryID: lib.ID, Path: p, Format: "epub", Size: 10})
		return id
	}
	novel := mk(unsorted, "in/witches.epub", "A Discovery of Witches", "Deborah Harkness")
	py := mk(unsorted, "in/py.epub", "Learning Python", "Mark Lutz")
	nick := mk(f.lib, "t/Poezija.epub", "Poezija", "t")
	f.srv.queueSort(ctx, []int64{novel, py, nick}, "upload", 0)
	for f.srv.sortRound(ctx) {
	}
	if len(ai.prompts) != 1 || !strings.Contains(ai.prompts[0], `"uploader_author"`) {
		t.Fatalf("prompts: %v", ai.prompts)
	}

	call := func(method, path, tok string, body any) map[string]any {
		t.Helper()
		var rd *bytes.Reader
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
		res := f.do(t, method, path, tok, rd, "application/json")
		out := map[string]any{}
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != 200 {
			t.Fatalf("%s %s: %d %v", method, path, res.StatusCode, out)
		}
		return out
	}
	list := call("GET", "/api/v1/review/imports", f.admin, nil)
	items := list["items"].([]any)
	got := map[float64]string{}
	for _, it := range items {
		m := it.(map[string]any)
		got[m["book_id"].(float64)] = m["to"].(string)
	}
	if len(items) != 3 || got[float64(novel)] != "Books" || got[float64(py)] != "Tech books" || got[float64(nick)] != "Unsorted" {
		t.Fatalf("suggestions: %v", got)
	}
	// keep one where it is, move the rest
	call("POST", "/api/v1/review/imports/keep", f.editor, map[string]any{"ids": []int64{nick}})
	res := call("POST", "/api/v1/review/imports/apply", f.admin, map[string]any{"ids": []int64{novel, py, nick}})
	if res["moved"] != float64(2) {
		t.Fatalf("apply: %v", res)
	}
	at := func(id int64) int64 { b, _ := f.st.GetBookAny(ctx, id); return b.LibraryID }
	if at(novel) != f.lib.ID || at(py) != tech.ID || at(nick) != f.lib.ID {
		t.Fatalf("libraries: %d %d %d", at(novel), at(py), at(nick))
	}
	if b, _ := f.st.GetBookAny(ctx, novel); !strings.HasSuffix(b.Files[0].Path, filepath.Join("Deborah Harkness", "Deborah Harkness - A Discovery of Witches.epub")) {
		t.Fatalf("filed as %s", b.Files[0].Path)
	}
	done := call("GET", "/api/v1/review/imports?status=done", f.admin, nil)
	if done["total"] != float64(3) {
		t.Fatalf("done: %v", done)
	}
	// asking again puts a book back in the queue; members can't
	call("POST", "/api/v1/review/imports/ask", f.editor, map[string]any{"ids": []int64{nick}})
	if p, _ := f.st.PendingSort(ctx, 10); len(p) != 1 || p[0].BookID != nick {
		t.Fatalf("asked: %+v", p)
	}
	if r := f.do(t, "POST", "/api/v1/review/imports/ask", f.member, strings.NewReader(`{"ids":[1]}`), "application/json"); r.StatusCode != 403 {
		t.Fatalf("member asked: %d", r.StatusCode)
	}
}
