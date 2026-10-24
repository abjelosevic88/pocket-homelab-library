package server

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

// The catalogue is shared, but only through the libraries a person may open;
// everything a person does with a book (status, rating, shelves) is theirs.
func TestCatalogPerPerson(t *testing.T) {
	f := newDevFixture(t)
	ctx := context.Background()
	// a library only Ana may open, with one book
	private := &store.Library{Name: "Ana's", Kind: "books", Paths: []string{f.dir + "/private"}, Access: "people", People: []int64{f.ana.ID}}
	if err := f.st.CreateLibrary(ctx, private); err != nil {
		t.Fatal(err)
	}
	secret, err := f.st.CreateBook(ctx, private.ID, "/secret", store.BookMeta{Title: "Secret diary", Authors: []string{"Ana"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.UpsertFile(ctx, &store.File{BookID: secret, LibraryID: private.ID, Path: f.dir + "/private/secret.epub", Format: "epub"}); err != nil {
		t.Fatal(err)
	}

	var page struct {
		Items []store.Book `json:"items"`
		Total int          `json:"total"`
	}
	f.json(f.bobTok, "GET", "/api/v1/books", nil, http.StatusOK, &page)
	if page.Total != 1 || page.Items[0].ID != f.book {
		t.Fatalf("bob sees %+v", page)
	}
	f.json(f.anaTok, "GET", "/api/v1/books?q=diary", nil, http.StatusOK, &page)
	if page.Total != 1 {
		t.Fatalf("ana's search: %+v", page)
	}
	f.json(f.bobTok, "GET", "/api/v1/books?q=diary", nil, http.StatusOK, &page)
	if page.Total != 0 {
		t.Fatalf("bob finds the private book: %+v", page)
	}
	for _, path := range []string{"/api/v1/books/%d", "/api/v1/books/%d/reads"} {
		if code, _, _ := f.do(f.bobTok, "GET", fmt.Sprintf(path, secret), nil); code != http.StatusNotFound {
			t.Fatalf("bob GET %s = %d", path, code)
		}
	}
	if code, _, _ := f.do(f.bobTok, "PUT", fmt.Sprintf("/api/v1/books/%d/state", secret), map[string]any{"status": "reading"}); code != http.StatusNotFound {
		t.Fatalf("bob set state on the private book: %d", code)
	}
	var libs []map[string]any
	f.json(f.bobTok, "GET", "/api/v1/libraries", nil, http.StatusOK, &libs)
	if len(libs) != 1 || len(libs[0]["paths"].([]any)) != 0 {
		t.Fatalf("bob's libraries: %v", libs)
	}

	// Ana finishes the shared book and rates it; Bob sees none of that
	var ub store.UserBook
	f.json(f.anaTok, "PUT", fmt.Sprintf("/api/v1/books/%d/state", f.book), map[string]any{"status": "read", "rating": 9}, http.StatusOK, &ub)
	if ub.Status != "read" || ub.Rating != 9 || ub.TimesRead != 1 {
		t.Fatalf("ana's state: %+v", ub)
	}
	var detail struct {
		State *store.UserBook `json:"state"`
		Reads []store.Read    `json:"reads"`
	}
	f.json(f.bobTok, "GET", fmt.Sprintf("/api/v1/books/%d", f.book), nil, http.StatusOK, &detail)
	if detail.State != nil || len(detail.Reads) != 0 {
		t.Fatalf("bob sees ana's state: %+v", detail)
	}
	var reads []store.Read
	f.json(f.anaTok, "GET", "/api/v1/reads", nil, http.StatusOK, &reads)
	if len(reads) != 1 {
		t.Fatalf("ana's reads: %v", reads)
	}
	if code, _, _ := f.do(f.bobTok, "DELETE", fmt.Sprintf("/api/v1/reads/%d", reads[0].ID), nil); code != http.StatusNotFound {
		t.Fatalf("bob deleted ana's read: %d", code)
	}

	// shelves are personal
	var sh store.Shelf
	f.json(f.anaTok, "POST", "/api/v1/shelves", map[string]any{"name": "Balkans"}, http.StatusCreated, &sh)
	f.json(f.anaTok, "POST", fmt.Sprintf("/api/v1/shelves/%d/books", sh.ID), map[string]any{"book_ids": []int64{f.book, secret}}, http.StatusOK, nil)
	for _, m := range []struct{ method, path string }{{"GET", "/api/v1/shelves/%d"}, {"PUT", "/api/v1/shelves/%d"}, {"DELETE", "/api/v1/shelves/%d"}} {
		code, _, _ := f.do(f.bobTok, m.method, fmt.Sprintf(m.path, sh.ID), map[string]any{"name": "x"})
		if code != http.StatusNotFound {
			t.Fatalf("bob %s shelf = %d", m.method, code)
		}
	}
	// Bob can't put the private book on his own shelf either
	var bobShelf store.Shelf
	f.json(f.bobTok, "POST", "/api/v1/shelves", map[string]any{"name": "Mine"}, http.StatusCreated, &bobShelf)
	var added map[string]int
	f.json(f.bobTok, "POST", fmt.Sprintf("/api/v1/shelves/%d/books", bobShelf.ID), map[string]any{"book_ids": []int64{secret, f.book}}, http.StatusOK, &added)
	if added["added"] != 1 {
		t.Fatalf("bob added %v", added)
	}

	// smart shelf: a saved filter
	var smart store.Shelf
	f.json(f.anaTok, "POST", "/api/v1/shelves", map[string]any{"name": "Finished", "kind": "smart", "filter": `{"status":"read"}`}, http.StatusCreated, &smart)
	if smart.Books != 1 {
		t.Fatalf("smart shelf counts %d", smart.Books)
	}

	// editing metadata needs an editor, and locks the fields edited
	if code, _, _ := f.do(f.bobTok, "PUT", fmt.Sprintf("/api/v1/books/%d", f.book), map[string]any{"title": "x"}); code != http.StatusForbidden {
		t.Fatalf("member edit = %d", code)
	}
	var edited struct {
		Title  string   `json:"title"`
		Locked []string `json:"locked"`
		Series string   `json:"series"`
	}
	f.json(f.anaTok, "PUT", fmt.Sprintf("/api/v1/books/%d", f.book), map[string]any{"title": "The Bridge on the Drina", "series": ""}, http.StatusOK, &edited)
	if edited.Title != "The Bridge on the Drina" || edited.Series != "" || len(edited.Locked) != 2 {
		t.Fatalf("edited: %+v", edited)
	}

	// home and search answer for both, each with their own view
	var home map[string]any
	f.json(f.bobTok, "GET", "/api/v1/home", nil, http.StatusOK, &home)
	if len(home["continue"].([]any)) != 0 {
		t.Fatalf("bob's home: %v", home["continue"])
	}
	var search struct {
		Books []store.Book `json:"books"`
	}
	f.json(f.bobTok, "GET", "/api/v1/search?q=secret", nil, http.StatusOK, &search)
	if len(search.Books) != 0 {
		t.Fatalf("bob's search finds %v", search.Books)
	}
}

// Sorts that run newest first by default keep that direction when asked
// for it explicitly (OPDS "Recently added" must be newest first).
func TestBookSortOrder(t *testing.T) {
	f := newDevFixture(t)
	ctx := context.Background()
	newer, err := f.st.CreateBook(ctx, 1, "/newer", store.BookMeta{Title: "Aaa newer"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.DB.ExecContext(ctx, `UPDATE books SET added_at = '2099-01-01T00:00:00Z' WHERE id = ?`, newer); err != nil {
		t.Fatal(err)
	}
	if err := f.st.UpsertFile(ctx, &store.File{BookID: newer, LibraryID: 1, Path: f.dir + "/books/newer.epub", Format: "epub"}); err != nil {
		t.Fatal(err)
	}
	first := func(q string) int64 {
		var page struct {
			Items []store.Book `json:"items"`
		}
		f.json(f.anaTok, "GET", "/api/v1/books?"+q, nil, http.StatusOK, &page)
		return page.Items[0].ID
	}
	for q, want := range map[string]int64{"sort=added": newer, "sort=added&order=desc": newer, "sort=added&order=asc": f.book, "sort=title": newer, "sort=title&order=desc": f.book} {
		if got := first(q); got != want {
			t.Errorf("%s: first = %d, want %d", q, got, want)
		}
	}
}
