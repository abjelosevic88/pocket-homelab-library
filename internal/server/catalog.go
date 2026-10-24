package server

// The catalogue as people browse it: libraries, books, authors, series,
// tags, shelves, home and search. Stats and the reading feed are in stats.go.

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

func (s *Server) routesCatalog(api chi.Router) {
	api.Get("/libraries", s.handleLibraries)
	api.Post("/libraries", s.handleCreateLibrary)
	api.Put("/libraries/{id}", s.handleUpdateLibrary)
	api.Delete("/libraries/{id}", s.handleDeleteLibrary)

	api.Get("/books", s.handleBooks)
	api.Post("/books/bulk", s.handleBulk)
	api.Get("/books/{id}", s.handleBook)
	api.Put("/books/{id}", s.handleEditBook)
	api.Put("/books/{id}/state", s.handleBookState)
	api.Get("/books/{id}/reads", s.handleBookReads)
	api.Get("/reads", s.handleReads)
	api.Delete("/reads/{id}", s.handleDeleteRead)

	api.Get("/authors", s.handleAuthors)
	api.Get("/authors/{id}", s.handleAuthor)
	api.Put("/authors/{id}", s.handleEditAuthor)
	api.Get("/series", s.handleSeriesList)
	api.Get("/series/{id}", s.handleSeries)
	api.Put("/series/{id}", s.handleEditSeries)
	api.Get("/tags", s.handleTags)
	api.Get("/facets", s.handleFacets)

	api.Get("/shelves", s.handleShelves)
	api.Post("/shelves", s.handleCreateShelf)
	api.Put("/shelves/order", s.handleReorderShelves)
	api.Get("/shelves/{id}", s.handleShelf)
	api.Put("/shelves/{id}", s.handleUpdateShelf)
	api.Delete("/shelves/{id}", s.handleDeleteShelf)
	api.Post("/shelves/{id}/books", s.handleShelfAdd)
	api.Post("/shelves/{id}/books/remove", s.handleShelfRemove)
	api.Put("/shelves/{id}/books/order", s.handleShelfOrder)

	api.Get("/home", s.handleHome)
	api.Get("/search", s.handleSearch)

	s.routesStats(api)
}

func (s *Server) rawCatalog(raw chi.Router) {}

// ---- access helpers (used by every area) ----

// allowedLibraries are the ids of the libraries the person may open.
func (s *Server) allowedLibraries(ctx context.Context, u *store.User) []int64 {
	ids, err := s.store.LibraryIDsFor(ctx, u)
	if err != nil || ids == nil {
		return []int64{}
	}
	return ids
}

// visibleLibraries are allowedLibraries minus the ones the person hid from
// Home and browsing.
func (s *Server) visibleLibraries(ctx context.Context, u *store.User) []int64 {
	ids := s.allowedLibraries(ctx, u)
	st, err := s.store.GetSettings(ctx, u.ID)
	if err != nil || strings.TrimSpace(st.HiddenLibraries) == "" {
		return ids
	}
	hidden := map[string]bool{}
	for _, h := range strings.Split(st.HiddenLibraries, ",") {
		hidden[strings.TrimSpace(h)] = true
	}
	out := []int64{}
	for _, id := range ids {
		if !hidden[itoa(id)] {
			out = append(out, id)
		}
	}
	return out
}

// bookFor loads a book the person may open (with files and their state),
// answering 404 when it is not in one of their libraries.
func (s *Server) bookFor(w http.ResponseWriter, r *http.Request, id int64) *store.Book {
	u := userFrom(r.Context())
	b, err := s.store.GetBook(r.Context(), u.ID, id, s.allowedLibraries(r.Context(), u))
	if err != nil {
		s.fail(w, err, "book")
		return nil
	}
	return b
}

// fileFor loads a file of a book the person may open (404 otherwise).
func (s *Server) fileFor(w http.ResponseWriter, r *http.Request, fileID int64) (*store.Book, *store.File) {
	f, err := s.store.GetFile(r.Context(), fileID)
	if err != nil {
		s.fail(w, err, "file")
		return nil, nil
	}
	b := s.bookFor(w, r, f.BookID)
	if b == nil {
		return nil, nil
	}
	return b, f
}

// requireEditor answers 403 unless the person may change the catalogue.
func (s *Server) requireEditor(w http.ResponseWriter, r *http.Request) bool {
	if !userFrom(r.Context()).Editor() {
		writeErr(w, http.StatusForbidden, "only an admin or an editor can do that")
		return false
	}
	return true
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// ---- libraries ----

func (s *Server) handleLibraries(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	libs, err := s.store.LibrariesFor(r.Context(), u)
	if err != nil {
		s.fail(w, err, "libraries")
		return
	}
	out := make([]map[string]any, 0, len(libs))
	for _, l := range libs {
		m := libraryJSON(l, u.Role == "admin")
		m["scanning"] = s.scanning(l.ID)
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, out)
}

// libraryJSON hides where the folders are from members.
func libraryJSON(l store.Library, admin bool) map[string]any {
	b, _ := json.Marshal(l)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if !admin {
		m["paths"], m["people"], m["upload_template"], m["last_scan_error"] = []string{}, []int64{}, "", ""
	}
	return m
}

func (s *Server) libraryInput(w http.ResponseWriter, r *http.Request, l *store.Library) bool {
	if err := decode(r, l); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return false
	}
	if strings.TrimSpace(l.Name) == "" {
		writeErr(w, http.StatusBadRequest, "a library needs a name")
		return false
	}
	if len(l.Paths) == 0 {
		writeErr(w, http.StatusBadRequest, "a library needs at least one folder")
		return false
	}
	return true
}

func (s *Server) handleCreateLibrary(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var l store.Library
	l.ScanEvery = 60
	if !s.libraryInput(w, r, &l) {
		return
	}
	if err := s.store.CreateLibrary(r.Context(), &l); err != nil {
		s.fail(w, err, "create library")
		return
	}
	s.kickScan(l.ID)
	got, _ := s.store.GetLibrary(r.Context(), l.ID)
	writeJSON(w, http.StatusCreated, libraryJSON(*got, true))
}

func (s *Server) handleUpdateLibrary(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	cur, err := s.store.GetLibrary(r.Context(), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "library")
		return
	}
	l := *cur
	if !s.libraryInput(w, r, &l) {
		return
	}
	l.ID = cur.ID
	if err := s.store.UpdateLibrary(r.Context(), &l); err != nil {
		s.fail(w, err, "update library")
		return
	}
	if strings.Join(l.Paths, "\n") != strings.Join(cur.Paths, "\n") || l.Kind != cur.Kind {
		s.kickScan(l.ID)
	}
	got, _ := s.store.GetLibrary(r.Context(), l.ID)
	writeJSON(w, http.StatusOK, libraryJSON(*got, true))
}

func (s *Server) handleDeleteLibrary(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if err := s.store.DeleteLibrary(r.Context(), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete library")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- books ----

// queryFrom reads a BookQuery from the URL, scoped to the person.
func (s *Server) queryFrom(r *http.Request) store.BookQuery {
	v := r.URL.Query()
	u := userFrom(r.Context())
	q := store.BookQuery{
		Q: v.Get("q"), Library: queryInt64(r, "library"), Author: queryInt64(r, "author"), Series: queryInt64(r, "series"),
		Tag: v.Get("tag"), Format: v.Get("format"), Language: v.Get("language"), Publisher: v.Get("publisher"), Status: v.Get("status"),
		Favorite: queryBool(r, "favorite"), RatingMin: queryInt(r, "rating_min"), Kind: v.Get("kind"), AddedSince: v.Get("added_since"),
		Missing: v.Get("missing"), Sort: v.Get("sort"), Order: v.Get("order"), Offset: queryInt(r, "offset"), Limit: queryInt(r, "limit"),
		Seed: queryInt64(r, "seed"), UserID: u.ID, ShowHidden: queryBool(r, "hidden"),
	}
	return s.scopeQuery(r.Context(), u, q)
}

// scopeQuery limits a query to the person's libraries: the visible ones, or
// any library they may open when one is asked for by id.
func (s *Server) scopeQuery(ctx context.Context, u *store.User, q store.BookQuery) store.BookQuery {
	q.UserID = u.ID
	if q.Library > 0 || q.Author > 0 || q.Series > 0 || q.Shelf > 0 || len(q.IDs) > 0 {
		q.Libraries = s.allowedLibraries(ctx, u)
	} else {
		q.Libraries = s.visibleLibraries(ctx, u)
	}
	return q
}

func (s *Server) handleBooks(w http.ResponseWriter, r *http.Request) {
	q := s.queryFrom(r)
	books, total, err := s.store.ListBooks(r.Context(), q)
	if err != nil {
		s.fail(w, err, "books")
		return
	}
	if books == nil {
		books = []store.Book{}
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 60
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": books, "total": total, "offset": q.Offset, "limit": limit})
}

// readableFormats are the ones the web reader opens.
var readableFormats = map[string]bool{"epub": true, "kepub": true, "mobi": true, "azw": true, "azw3": true, "prc": true, "fb2": true, "fbz": true, "pdf": true, "cbz": true, "cbr": true, "cb7": true, "cbt": true}

func (s *Server) handleBook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	b := s.bookFor(w, r, idParam(r, "id"))
	if b == nil {
		return
	}
	writeJSON(w, http.StatusOK, s.bookDetail(ctx, u, b))
}

// bookDetail is a book with what its page shows around it.
func (s *Server) bookDetail(ctx context.Context, u *store.User, b *store.Book) map[string]any {
	libs := s.allowedLibraries(ctx, u)
	raw, _ := json.Marshal(b)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if b.Files == nil {
		m["files"] = []store.File{}
	}
	if lib, err := s.store.GetLibrary(ctx, b.LibraryID); err == nil {
		m["library"] = map[string]any{"id": lib.ID, "name": lib.Name, "kind": lib.Kind}
	}
	seriesBooks := []store.Book{}
	if b.SeriesID > 0 {
		seriesBooks, _, _ = s.store.ListBooks(ctx, store.BookQuery{UserID: u.ID, Libraries: libs, Series: b.SeriesID, Sort: "series", Limit: 200})
	}
	m["series_books"] = seriesBooks
	more := []store.Book{}
	if len(b.Authors) > 0 {
		list, _, _ := s.store.ListBooks(ctx, store.BookQuery{UserID: u.ID, Libraries: libs, Author: b.Authors[0].ID, Sort: "published", Order: "desc", Limit: 25})
		for _, x := range list {
			if x.ID != b.ID && (b.SeriesID == 0 || x.SeriesID != b.SeriesID) {
				more = append(more, x)
			}
		}
	}
	m["more_by_author"] = more
	m["reads"], _ = s.store.Reads(ctx, u.ID, b.ID, "", "")
	m["shelves"], _ = s.store.ShelvesOfBook(ctx, u.ID, b.ID)
	m["annotations"] = s.store.CountAnnotations(ctx, u.ID, b.ID)
	readable, sendable := false, false
	convert := s.cfg.ConvertPath() != ""
	for _, f := range b.Files {
		if f.Missing {
			continue
		}
		if readableFormats[f.Format] {
			readable = true
		}
		switch f.Format {
		case "epub", "kepub", "pdf", "docx", "txt", "rtf":
			sendable = true
		case "mobi", "azw", "azw3", "prc", "fb2", "fbz":
			sendable = sendable || convert
		}
	}
	m["readable"], m["sendable"] = readable, sendable
	if bs := s.bookshelfInfo(ctx, u, b); bs != nil {
		m["bookshelf"] = bs
	}
	return m
}

// handleEditBook saves metadata edited by hand; every field sent is locked
// so the next scan keeps it (unless "locked" is sent, which replaces the list).
func (s *Server) handleEditBook(w http.ResponseWriter, r *http.Request) {
	if !s.requireEditor(w, r) {
		return
	}
	ctx := r.Context()
	b := s.bookFor(w, r, idParam(r, "id"))
	if b == nil {
		return
	}
	var in struct {
		Title            string            `json:"title"`
		SortTitle        string            `json:"sort_title"`
		Subtitle         string            `json:"subtitle"`
		Authors          []string          `json:"authors"`
		Description      string            `json:"description"`
		Language         string            `json:"language"`
		Publisher        string            `json:"publisher"`
		Published        string            `json:"published"`
		ISBN             string            `json:"isbn"`
		Identifiers      map[string]string `json:"identifiers"`
		Series           string            `json:"series"`
		SeriesIndex      float64           `json:"series_index"`
		Tags             []string          `json:"tags"`
		Pages            int               `json:"pages"`
		Kind             string            `json:"kind"`
		ReadingDirection string            `json:"reading_direction"`
		AgeRating        string            `json:"age_rating"`
		Locked           []string          `json:"locked"`
	}
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	m := store.MetaOf(b)
	locked := map[string]bool{}
	for _, f := range b.Locked {
		locked[f] = true
	}
	set := func(key string, apply func()) {
		if keys[key] {
			apply()
			locked[key] = true
		}
	}
	set("title", func() { m.Title = strings.TrimSpace(in.Title) })
	set("sort_title", func() { m.SortTitle = strings.TrimSpace(in.SortTitle) })
	set("subtitle", func() { m.Subtitle = strings.TrimSpace(in.Subtitle) })
	set("authors", func() { m.Authors = in.Authors })
	set("description", func() { m.Description = strings.TrimSpace(in.Description) })
	set("language", func() { m.Language = strings.ToLower(strings.TrimSpace(in.Language)) })
	set("publisher", func() { m.Publisher = strings.TrimSpace(in.Publisher) })
	set("published", func() { m.Published = strings.TrimSpace(in.Published) })
	set("isbn", func() { m.ISBN = strings.ReplaceAll(strings.TrimSpace(in.ISBN), "-", "") })
	set("identifiers", func() { m.Identifiers = in.Identifiers })
	set("series", func() { m.Series = strings.TrimSpace(in.Series) })
	set("series_index", func() { m.SeriesIndex = in.SeriesIndex })
	set("tags", func() { m.Tags = in.Tags })
	set("pages", func() { m.Pages = max(in.Pages, 0) })
	set("kind", func() { m.Kind = in.Kind })
	set("reading_direction", func() { m.ReadingDirection = in.ReadingDirection })
	set("age_rating", func() { m.AgeRating = strings.TrimSpace(in.AgeRating) })
	if keys["title"] && m.Title == "" {
		writeErr(w, http.StatusBadRequest, "a book needs a title")
		return
	}
	if keys["title"] && !keys["sort_title"] {
		m.SortTitle = ""
	}
	m.MetaSource = "edited"
	if err := s.store.SaveBookMeta(ctx, b.ID, m); err != nil {
		s.fail(w, err, "save book")
		return
	}
	var lock []string
	if keys["locked"] {
		lock = in.Locked
	} else {
		for f := range locked {
			lock = append(lock, f)
		}
	}
	if err := s.store.SetLocked(ctx, b.ID, lock); err != nil {
		s.fail(w, err, "save book")
		return
	}
	_ = s.store.PruneOrphans(ctx)
	if nb := s.bookFor(w, r, b.ID); nb != nil {
		writeJSON(w, http.StatusOK, s.bookDetail(ctx, userFrom(ctx), nb))
	}
}

// handleBookState changes the person's status, rating, favourite or hidden flag.
func (s *Server) handleBookState(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	b := s.bookFor(w, r, idParam(r, "id"))
	if b == nil {
		return
	}
	var in struct {
		Status   *string `json:"status"`
		Rating   *int    `json:"rating"`
		Favorite *bool   `json:"favorite"`
		Hidden   *bool   `json:"hidden"`
		Day      string  `json:"day"`
	}
	if err := decode(r, &in); err != nil || !validDate(in.Day) {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Status != nil {
		ub, ch, err := s.store.SetStatus(ctx, u.ID, b.ID, *in.Status, in.Day)
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusBadRequest, "status must be one of want, reading, read, dnf or empty")
			return
		}
		if err != nil {
			s.fail(w, err, "status")
			return
		}
		s.statusEvents(ctx, u, b, ch, ub)
	}
	ub, err := s.store.UpdateUserBook(ctx, u.ID, b.ID, in.Rating, in.Favorite, in.Hidden)
	if err != nil {
		s.fail(w, err, "state")
		return
	}
	writeJSON(w, http.StatusOK, ub)
}

// statusEvents tells Core (and the person's pushes) that a book was started or finished.
func (s *Server) statusEvents(ctx context.Context, u *store.User, b *store.Book, ch store.StatusChange, ub *store.UserBook) {
	authors := make([]string, 0, len(b.Authors))
	for _, a := range b.Authors {
		authors = append(authors, a.Name)
	}
	payload := map[string]any{"book_id": b.ID, "title": b.Title, "authors": authors, "isbn": b.ISBN, "series": b.Series, "series_index": b.SeriesIndex, "pages": b.Pages}
	if ch.Started {
		s.publish(ctx, u, "library.started", b.Title, payload)
	}
	if ch.Finished && ub != nil {
		payload["status"], payload["finished_at"] = ub.Status, ub.FinishedAt
		s.publish(ctx, u, "library.finished", b.Title, payload)
		if ub.Status == "read" {
			s.notifyFinished(ctx, u, b)
		}
	}
}

func (s *Server) handleBulk(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	var in struct {
		IDs         []int64  `json:"ids"`
		Status      *string  `json:"status"`
		Favorite    *bool    `json:"favorite"`
		Hidden      *bool    `json:"hidden"`
		AddTags     []string `json:"add_tags"`
		RemoveTags  []string `json:"remove_tags"`
		Series      *string  `json:"series"`
		Language    *string  `json:"language"`
		ShelfAdd    int64    `json:"shelf_add"`
		ShelfRemove int64    `json:"shelf_remove"`
	}
	if err := decode(r, &in); err != nil || len(in.IDs) == 0 || len(in.IDs) > 2000 {
		writeErr(w, http.StatusBadRequest, "send the ids of up to 2000 books")
		return
	}
	meta := len(in.AddTags) > 0 || len(in.RemoveTags) > 0 || in.Series != nil || in.Language != nil
	if meta && !s.requireEditor(w, r) {
		return
	}
	libs := s.allowedLibraries(ctx, u)
	n := 0
	var ok []int64
	for _, id := range in.IDs {
		b, err := s.store.GetBook(ctx, u.ID, id, libs)
		if err != nil {
			continue
		}
		ok = append(ok, id)
		if in.Status != nil {
			ub, ch, err := s.store.SetStatus(ctx, u.ID, id, *in.Status, "")
			if err == nil {
				s.statusEvents(ctx, u, b, ch, ub)
			}
		}
		if in.Favorite != nil || in.Hidden != nil {
			_, _ = s.store.UpdateUserBook(ctx, u.ID, id, nil, in.Favorite, in.Hidden)
		}
		if meta {
			m := store.MetaOf(b)
			lock := append([]string{}, b.Locked...)
			if len(in.AddTags) > 0 || len(in.RemoveTags) > 0 {
				drop := map[string]bool{}
				for _, t := range in.RemoveTags {
					drop[strings.ToLower(strings.TrimSpace(t))] = true
				}
				var tags []string
				for _, t := range append(m.Tags, in.AddTags...) {
					if !drop[strings.ToLower(strings.TrimSpace(t))] {
						tags = append(tags, t)
					}
				}
				m.Tags = tags
				lock = append(lock, "tags")
			}
			if in.Series != nil {
				m.Series = *in.Series
				lock = append(lock, "series")
			}
			if in.Language != nil {
				m.Language = strings.ToLower(strings.TrimSpace(*in.Language))
				lock = append(lock, "language")
			}
			m.MetaSource = "edited"
			if err := s.store.SaveBookMeta(ctx, id, m); err == nil {
				_ = s.store.SetLocked(ctx, id, lock)
			}
		}
		n++
	}
	if in.ShelfAdd > 0 && len(ok) > 0 {
		if err := s.store.AddToShelf(ctx, u.ID, in.ShelfAdd, ok); err != nil {
			s.fail(w, err, "shelf")
			return
		}
	}
	if in.ShelfRemove > 0 && len(ok) > 0 {
		if err := s.store.RemoveFromShelf(ctx, u.ID, in.ShelfRemove, ok); err != nil {
			s.fail(w, err, "shelf")
			return
		}
	}
	if meta {
		_ = s.store.PruneOrphans(ctx)
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated": n})
}

func (s *Server) handleBookReads(w http.ResponseWriter, r *http.Request) {
	b := s.bookFor(w, r, idParam(r, "id"))
	if b == nil {
		return
	}
	reads, err := s.store.Reads(r.Context(), actorID(r.Context()), b.ID, "", "")
	if err != nil {
		s.fail(w, err, "reads")
		return
	}
	writeJSON(w, http.StatusOK, reads)
}

func (s *Server) handleReads(w http.ResponseWriter, r *http.Request) {
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	if !validDate(from) || !validDate(to) {
		writeErr(w, http.StatusBadRequest, "from and to are YYYY-MM-DD")
		return
	}
	reads, err := s.store.Reads(r.Context(), actorID(r.Context()), 0, from, to)
	if err != nil {
		s.fail(w, err, "reads")
		return
	}
	writeJSON(w, http.StatusOK, reads)
}

func (s *Server) handleDeleteRead(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteRead(r.Context(), actorID(r.Context()), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete read")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- authors, series, tags ----

func (s *Server) handleAuthors(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	list, total, err := s.store.ListAuthors(r.Context(), s.visibleLibraries(r.Context(), u), r.URL.Query().Get("q"), queryInt(r, "offset"), queryInt(r, "limit"))
	if err != nil {
		s.fail(w, err, "authors")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "total": total, "offset": queryInt(r, "offset"), "limit": queryInt(r, "limit")})
}

func (s *Server) handleAuthor(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	libs := s.allowedLibraries(ctx, u)
	a, err := s.store.GetAuthor(ctx, idParam(r, "id"), libs)
	if err != nil || a.Books == 0 {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	books, _, err := s.store.ListBooks(ctx, store.BookQuery{UserID: u.ID, Libraries: libs, Author: a.ID, Sort: "series", Limit: 500})
	if err != nil {
		s.fail(w, err, "author books")
		return
	}
	seen := map[int64]bool{}
	series := []store.Series{}
	for _, b := range books {
		if b.SeriesID > 0 && !seen[b.SeriesID] {
			seen[b.SeriesID] = true
			if se, err := s.store.GetSeries(ctx, u.ID, b.SeriesID, libs); err == nil {
				series = append(series, *se)
			}
		}
	}
	if books == nil {
		books = []store.Book{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"author": a, "books": books, "series": series})
}

func (s *Server) handleEditAuthor(w http.ResponseWriter, r *http.Request) {
	if !s.requireEditor(w, r) {
		return
	}
	ctx := r.Context()
	id := idParam(r, "id")
	libs := s.allowedLibraries(ctx, userFrom(ctx))
	a, err := s.store.GetAuthor(ctx, id, libs)
	if err != nil {
		s.fail(w, err, "author")
		return
	}
	var in struct {
		Name *string `json:"name"`
		Bio  *string `json:"bio"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Bio != nil {
		photo, _ := s.store.AuthorPhotoFile(ctx, id)
		if err := s.store.SetAuthorInfo(ctx, id, strings.TrimSpace(*in.Bio), photo, "", a.Links); err != nil {
			s.fail(w, err, "author")
			return
		}
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) != a.Name {
		if err := s.store.RenameAuthor(ctx, id, *in.Name); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		// a merge moves the books to the other author
		var other int64
		_ = s.store.DB.QueryRowContext(ctx, `SELECT id FROM authors WHERE name = ?`, strings.TrimSpace(*in.Name)).Scan(&other)
		if other > 0 {
			id = other
		}
	}
	got, err := s.store.GetAuthor(ctx, id, libs)
	if err != nil {
		s.fail(w, err, "author")
		return
	}
	writeJSON(w, http.StatusOK, got)
}

func (s *Server) handleSeriesList(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	list, total, err := s.store.ListSeries(r.Context(), u.ID, s.visibleLibraries(r.Context(), u), r.URL.Query().Get("q"), queryInt(r, "offset"), queryInt(r, "limit"))
	if err != nil {
		s.fail(w, err, "series")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "total": total, "offset": queryInt(r, "offset"), "limit": queryInt(r, "limit")})
}

func (s *Server) handleSeries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	libs := s.allowedLibraries(ctx, u)
	se, err := s.store.GetSeries(ctx, u.ID, idParam(r, "id"), libs)
	if err != nil || se.Books == 0 {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	books, _, err := s.store.ListBooks(ctx, store.BookQuery{UserID: u.ID, Libraries: libs, Series: se.ID, Sort: "series", Limit: 500})
	if err != nil {
		s.fail(w, err, "series books")
		return
	}
	if books == nil {
		books = []store.Book{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"series": se, "books": books})
}

func (s *Server) handleEditSeries(w http.ResponseWriter, r *http.Request) {
	if !s.requireEditor(w, r) {
		return
	}
	ctx := r.Context()
	u := userFrom(ctx)
	libs := s.allowedLibraries(ctx, u)
	se, err := s.store.GetSeries(ctx, u.ID, idParam(r, "id"), libs)
	if err != nil {
		s.fail(w, err, "series")
		return
	}
	var in struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Description != nil {
		_, _ = s.store.DB.ExecContext(ctx, `UPDATE series SET description = ? WHERE id = ?`, strings.TrimSpace(*in.Description), se.ID)
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) != "" && strings.TrimSpace(*in.Name) != se.Name {
		// renaming = moving every book (locked) to the series of that name
		books, _, _ := s.store.ListBooks(ctx, store.BookQuery{UserID: u.ID, Libraries: libs, Series: se.ID, Limit: 500})
		for _, b := range books {
			m := store.MetaOf(&b)
			m.Series, m.MetaSource = strings.TrimSpace(*in.Name), "edited"
			if err := s.store.SaveBookMeta(ctx, b.ID, m); err == nil {
				_ = s.store.SetLocked(ctx, b.ID, append(b.Locked, "series"))
			}
		}
		_ = s.store.PruneOrphans(ctx)
		var nid int64
		_ = s.store.DB.QueryRowContext(ctx, `SELECT id FROM series WHERE name = ?`, strings.TrimSpace(*in.Name)).Scan(&nid)
		if nid > 0 {
			se.ID = nid
		}
	}
	got, err := s.store.GetSeries(ctx, u.ID, se.ID, libs)
	if err != nil {
		s.fail(w, err, "series")
		return
	}
	writeJSON(w, http.StatusOK, got)
}

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	tags, err := s.store.ListTags(r.Context(), s.visibleLibraries(r.Context(), u), r.URL.Query().Get("q"), queryInt(r, "limit"))
	if err != nil {
		s.fail(w, err, "tags")
		return
	}
	writeJSON(w, http.StatusOK, tags)
}

func (s *Server) handleFacets(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	f, err := s.store.Facets(r.Context(), s.visibleLibraries(r.Context(), u))
	if err != nil {
		s.fail(w, err, "facets")
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// ---- shelves ----

func (s *Server) handleShelves(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	list, err := s.store.ListShelves(ctx, u.ID)
	if err != nil {
		s.fail(w, err, "shelves")
		return
	}
	for i := range list {
		s.fillSmartShelf(ctx, u, &list[i])
	}
	writeJSON(w, http.StatusOK, list)
}

// smartQuery is a smart shelf's saved filter as a query for the person.
func (s *Server) smartQuery(ctx context.Context, u *store.User, sh *store.Shelf) store.BookQuery {
	var q store.BookQuery
	_ = json.Unmarshal([]byte(sh.Filter), &q)
	q.Shelf, q.IDs = 0, nil
	return s.scopeQuery(ctx, u, q)
}

// fillSmartShelf counts a smart shelf's books and picks its covers.
func (s *Server) fillSmartShelf(ctx context.Context, u *store.User, sh *store.Shelf) {
	if sh.Kind != "smart" {
		return
	}
	q := s.smartQuery(ctx, u, sh)
	q.Limit, q.Offset = 3, 0
	books, total, err := s.store.ListBooks(ctx, q)
	if err != nil {
		return
	}
	sh.Books = total
	sh.Covers = []string{}
	for _, b := range books {
		if b.Cover != "" {
			sh.Covers = append(sh.Covers, b.Cover)
		}
	}
}

func (s *Server) shelfInput(w http.ResponseWriter, r *http.Request, sh *store.Shelf) bool {
	if err := decode(r, sh); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return false
	}
	if strings.TrimSpace(sh.Name) == "" {
		writeErr(w, http.StatusBadRequest, "a shelf needs a name")
		return false
	}
	if sh.Kind == "smart" {
		var q store.BookQuery
		if json.Unmarshal([]byte(sh.Filter), &q) != nil {
			writeErr(w, http.StatusBadRequest, "the filter of a smart shelf must be JSON")
			return false
		}
	}
	return true
}

func (s *Server) handleCreateShelf(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	sh := store.Shelf{OPDS: true}
	if !s.shelfInput(w, r, &sh) {
		return
	}
	if err := s.store.CreateShelf(ctx, u.ID, &sh); err != nil {
		s.fail(w, err, "create shelf")
		return
	}
	got, _ := s.store.GetShelf(ctx, u.ID, sh.ID)
	s.fillSmartShelf(ctx, u, got)
	writeJSON(w, http.StatusCreated, got)
}

func (s *Server) handleUpdateShelf(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	cur, err := s.store.GetShelf(ctx, u.ID, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "shelf")
		return
	}
	sh := *cur
	if !s.shelfInput(w, r, &sh) {
		return
	}
	sh.ID, sh.Kind = cur.ID, cur.Kind
	if err := s.store.UpdateShelf(ctx, u.ID, &sh); err != nil {
		s.fail(w, err, "update shelf")
		return
	}
	got, _ := s.store.GetShelf(ctx, u.ID, sh.ID)
	s.fillSmartShelf(ctx, u, got)
	writeJSON(w, http.StatusOK, got)
}

func (s *Server) handleDeleteShelf(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteShelf(r.Context(), actorID(r.Context()), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete shelf")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleReorderShelves(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.store.ReorderShelves(r.Context(), actorID(r.Context()), in.IDs); err != nil {
		s.fail(w, err, "reorder")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// shelfBooks lists a shelf's books for the person: a hand-picked shelf's in
// its order (or another sort), a smart shelf's by its filter.
func (s *Server) shelfBooks(ctx context.Context, u *store.User, sh *store.Shelf, sort, order string, offset, limit int) ([]store.Book, int, error) {
	var q store.BookQuery
	if sh.Kind == "smart" {
		q = s.smartQuery(ctx, u, sh)
		if sort != "" {
			q.Sort, q.Order = sort, order
		}
	} else {
		q = s.scopeQuery(ctx, u, store.BookQuery{Shelf: sh.ID, Sort: sort, Order: order})
		if q.Sort == "" {
			q.Sort = "shelf"
			if !sh.Ordered {
				q.Order = "desc"
			}
		}
		q.ShowHidden = true
	}
	q.Offset, q.Limit = offset, limit
	books, total, err := s.store.ListBooks(ctx, q)
	if books == nil {
		books = []store.Book{}
	}
	return books, total, err
}

func (s *Server) handleShelf(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	sh, err := s.store.GetShelf(ctx, u.ID, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "shelf")
		return
	}
	s.fillSmartShelf(ctx, u, sh)
	books, total, err := s.shelfBooks(ctx, u, sh, r.URL.Query().Get("sort"), r.URL.Query().Get("order"), queryInt(r, "offset"), queryInt(r, "limit"))
	if err != nil {
		s.fail(w, err, "shelf books")
		return
	}
	limit := queryInt(r, "limit")
	if limit <= 0 {
		limit = 60
	}
	writeJSON(w, http.StatusOK, map[string]any{"shelf": sh, "books": map[string]any{"items": books, "total": total, "offset": queryInt(r, "offset"), "limit": limit}})
}

func (s *Server) shelfIDs(w http.ResponseWriter, r *http.Request) []int64 {
	var in struct {
		BookIDs []int64 `json:"book_ids"`
	}
	if err := decode(r, &in); err != nil || len(in.BookIDs) == 0 {
		writeErr(w, http.StatusBadRequest, "send book_ids")
		return nil
	}
	return in.BookIDs
}

func (s *Server) handleShelfAdd(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	ids := s.shelfIDs(w, r)
	if ids == nil {
		return
	}
	libs := s.allowedLibraries(ctx, u)
	var ok []int64
	for _, id := range ids {
		if _, err := s.store.GetBook(ctx, u.ID, id, libs); err == nil {
			ok = append(ok, id)
		}
	}
	if err := s.store.AddToShelf(ctx, u.ID, idParam(r, "id"), ok); err != nil {
		s.fail(w, err, "shelf")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"added": len(ok)})
}

func (s *Server) handleShelfRemove(w http.ResponseWriter, r *http.Request) {
	ids := s.shelfIDs(w, r)
	if ids == nil {
		return
	}
	if err := s.store.RemoveFromShelf(r.Context(), actorID(r.Context()), idParam(r, "id"), ids); err != nil {
		s.fail(w, err, "shelf")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleShelfOrder(w http.ResponseWriter, r *http.Request) {
	ids := s.shelfIDs(w, r)
	if ids == nil {
		return
	}
	if err := s.store.ReorderShelf(r.Context(), actorID(r.Context()), idParam(r, "id"), ids); err != nil {
		s.fail(w, err, "shelf")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- home and search ----

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	base := s.scopeQuery(ctx, u, store.BookQuery{})
	list := func(q store.BookQuery) []store.Book {
		q.UserID, q.Libraries = base.UserID, base.Libraries
		books, _, err := s.store.ListBooks(ctx, q)
		if err != nil || books == nil {
			return []store.Book{}
		}
		return books
	}
	out := map[string]any{
		"continue":       list(store.BookQuery{Status: "reading", Sort: "last_read", Limit: 20}),
		"recently_added": list(store.BookQuery{Sort: "added", Limit: 24}),
		"want":           list(store.BookQuery{Status: "want", Sort: "added", Limit: 24}),
	}
	out["on_deck"] = s.onDeck(ctx, u, base.Libraries)
	out["discover"] = s.discover(ctx, u, base.Libraries)
	libs, _ := s.store.LibrariesFor(ctx, u)
	lj := []map[string]any{}
	for _, l := range libs {
		lj = append(lj, libraryJSON(l, u.Role == "admin"))
	}
	out["libraries"] = lj
	out["stats"] = s.homeStats(ctx, u)
	var pending int
	_ = s.store.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM sends WHERE user_id = ? AND status IN ('queued', 'sending')`, u.ID).Scan(&pending)
	out["pending_sends"] = pending
	writeJSON(w, http.StatusOK, out)
}

// onDeck is the next unread book of every series the person is in (reading
// or finished a book of it), newest activity first.
func (s *Server) onDeck(ctx context.Context, u *store.User, libs []int64) []store.Book {
	rows, err := s.store.DB.QueryContext(ctx, `SELECT b.series_id, MAX(ub.updated_at) FROM user_books ub JOIN books b ON b.id = ub.book_id
		WHERE ub.user_id = ? AND ub.status IN ('reading', 'read') AND b.series_id IS NOT NULL AND b.library_id IN (`+joinIDs(libs)+`)
		GROUP BY b.series_id ORDER BY MAX(ub.updated_at) DESC LIMIT 20`, u.ID)
	if err != nil {
		return []store.Book{}
	}
	var series []int64
	for rows.Next() {
		var id int64
		var at string
		if rows.Scan(&id, &at) == nil {
			series = append(series, id)
		}
	}
	rows.Close()
	out := []store.Book{}
	for _, sid := range series {
		books, _, err := s.store.ListBooks(ctx, store.BookQuery{UserID: u.ID, Libraries: libs, Series: sid, Sort: "series", Limit: 200})
		if err != nil {
			continue
		}
		reading := false
		for _, b := range books {
			if b.State != nil && b.State.Status == "reading" {
				reading = true
			}
		}
		if reading {
			continue // it is on Continue reading already
		}
		for _, b := range books {
			if b.State == nil || (b.State.Status == "" || b.State.Status == "want") && b.State.TimesRead == 0 {
				out = append(out, b)
				break
			}
		}
	}
	return out
}

// discover picks unread books at random (a new pick every visit).
func (s *Server) discover(ctx context.Context, u *store.User, libs []int64) []store.Book {
	seed := time.Now().UnixNano()%1000003 + rand.Int63n(1000)
	books, _, err := s.store.ListBooks(ctx, store.BookQuery{UserID: u.ID, Libraries: libs, Status: "unread", Sort: "random", Seed: seed, Limit: 18})
	if err != nil || books == nil {
		return []store.Book{}
	}
	return books
}

func joinIDs(ids []int64) string {
	if len(ids) == 0 {
		return "NULL"
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = itoa(id)
	}
	return strings.Join(parts, ",")
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	out := map[string]any{"books": []store.Book{}, "authors": []store.Author{}, "series": []store.Series{}, "tags": []store.Tag{}, "shelves": []store.Shelf{}}
	if q == "" {
		writeJSON(w, http.StatusOK, out)
		return
	}
	libs := s.visibleLibraries(ctx, u)
	if books, _, err := s.store.ListBooks(ctx, store.BookQuery{UserID: u.ID, Libraries: libs, Q: q, Limit: 12}); err == nil && books != nil {
		out["books"] = books
	}
	if a, _, err := s.store.ListAuthors(ctx, libs, q, 0, 6); err == nil {
		out["authors"] = a
	}
	if se, _, err := s.store.ListSeries(ctx, u.ID, libs, q, 0, 6); err == nil {
		out["series"] = se
	}
	if t, err := s.store.ListTags(ctx, libs, q, 6); err == nil {
		out["tags"] = t
	}
	if shelves, err := s.store.ListShelves(ctx, u.ID); err == nil {
		var hit []store.Shelf
		for _, sh := range shelves {
			if strings.Contains(strings.ToLower(sh.Name), strings.ToLower(q)) {
				hit = append(hit, sh)
			}
		}
		if hit != nil {
			out["shelves"] = hit
		}
	}
	writeJSON(w, http.StatusOK, out)
}
