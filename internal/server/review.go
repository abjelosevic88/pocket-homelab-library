package server

// Review: books that look wrong (a file name or a scan as the title, an
// uploader's nickname as the author, the author's name as the title, no
// author, no cover, the same book twice), for an editor to fix, delete or
// keep off e-readers. A book kept off e-readers stays in the library and the
// web reader; no device's catalogue lists it.

import (
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

func (s *Server) routesReview(api chi.Router) {
	api.Get("/review", s.handleReview)
	api.Put("/books/ereader", s.handleEReaderOff)
	api.Post("/books/move", s.handleMoveBooks)
	api.Get("/review/cleanup", s.handleCleanupPlan)
	api.Post("/review/cleanup", s.handleCleanupRun)
}

// Review flags, in the order the page shows them.
var reviewFlags = []string{"junk_title", "title_is_author", "uploader_author", "no_author", "duplicate", "no_cover"}

var (
	junkTitleRE = regexp.MustCompile(`(?i)^\(|microsoft word|\.(docx?|pdf|rtf|txt|epub|mobi)\s*\)?$|^(untitled|unknown|bez naslova|nepoznato?)$|faksimil|\\[0-9]{3}|^[0-9]{6,}$`)
	uploaderRE  = regexp.MustCompile(`^[a-z0-9._-]{1,24}$`)
)

// surnameKey is an author's last word, folded: "Dž. D. Selindžer" → "selindzer".
func surnameKey(name string) string {
	f := strings.Fields(fold(name))
	if len(f) == 0 {
		return ""
	}
	return f[len(f)-1]
}

type reviewItem struct {
	store.ReviewBook
	Library string   `json:"library"`
	Flags   []string `json:"flags"`
	Dupes   []int64  `json:"dupes,omitempty"` // the other books it looks like
}

// GET /review?flag=&library=&off=hide|only&offset=&limit= — editors only.
func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	if !s.requireEditor(w, r) {
		return
	}
	ctx := r.Context()
	u := userFrom(ctx)
	libs := s.allowedLibraries(ctx, u)
	books, err := s.store.ReviewBooks(ctx, libs)
	if err != nil {
		s.fail(w, err, "review")
		return
	}
	libNames := map[int64]string{}
	if list, err := s.store.LibrariesFor(ctx, u); err == nil {
		for _, l := range list {
			libNames[l.ID] = l.Name
		}
	}
	people := authorBooks(books)
	// the same title by the same author (by surname) in the same library
	groups := map[string][]int64{}
	keyOf := func(b store.ReviewBook) string {
		t := fold(b.Title)
		if t == "" || len(b.Authors) == 0 {
			return ""
		}
		sn := []string{}
		for _, a := range b.Authors {
			if k := surnameKey(a); k != "" {
				sn = append(sn, k)
			}
		}
		sort.Strings(sn)
		return strconv.FormatInt(b.LibraryID, 10) + "|" + t + "|" + strings.Join(sn, ",")
	}
	for _, b := range books {
		if k := keyOf(b); k != "" {
			groups[k] = append(groups[k], b.ID)
		}
	}
	q := r.URL.Query()
	flag, off := q.Get("flag"), q.Get("off")
	lib, _ := strconv.ParseInt(q.Get("library"), 10, 64)
	counts := map[string]int{}
	items := []reviewItem{}
	for _, b := range books {
		flags := badFlags(b, people)
		var dupes []int64
		if k := keyOf(b); k != "" && len(groups[k]) > 1 {
			for _, id := range groups[k] {
				if id != b.ID {
					dupes = append(dupes, id)
				}
			}
			flags = append(flags, "duplicate")
		}
		if b.Cover == "" {
			flags = append(flags, "no_cover")
		}
		if len(flags) == 0 || (lib > 0 && b.LibraryID != lib) {
			continue
		}
		if (off == "hide" && b.EReaderOff) || (off == "only" && !b.EReaderOff) {
			continue
		}
		for _, f := range flags {
			counts[f]++
		}
		if flag != "" && !contains(flags, flag) {
			continue
		}
		items = append(items, reviewItem{ReviewBook: b, Library: libNames[b.LibraryID], Flags: flags, Dupes: dupes})
	}
	total := len(items)
	offset, _ := strconv.Atoi(q.Get("offset"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	offset = min(max(offset, 0), total)
	items = items[offset:min(offset+limit, total)]
	writeJSON(w, http.StatusOK, map[string]any{"flags": reviewFlags, "counts": counts, "total": total, "items": items})
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// PUT /books/ereader {"ids": [...], "off": bool} — keep books off (or let them
// back onto) e-readers' catalogues. Editors only.
func (s *Server) handleEReaderOff(w http.ResponseWriter, r *http.Request) {
	if !s.requireEditor(w, r) {
		return
	}
	var in struct {
		IDs []int64 `json:"ids"`
		Off bool    `json:"off"`
	}
	if err := decode(r, &in); err != nil || len(in.IDs) == 0 || len(in.IDs) > 5000 {
		writeErr(w, http.StatusBadRequest, "ids (1–5000 books) and off are required")
		return
	}
	ctx := r.Context()
	u := userFrom(ctx)
	libs := s.allowedLibraries(ctx, u)
	ids := []int64{}
	for _, id := range in.IDs {
		if _, err := s.store.GetBook(ctx, u.ID, id, libs); err == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		writeErr(w, http.StatusNotFound, "no such books")
		return
	}
	if err := s.store.SetEReaderOff(ctx, ids, in.Off); err != nil {
		s.fail(w, err, "ereader")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": len(ids), "off": in.Off})
}

// authorBooks counts each author's books (folded names).
func authorBooks(books []store.ReviewBook) map[string]int {
	n := map[string]int{}
	for _, b := range books {
		for _, a := range b.Authors {
			n[fold(a)]++
		}
	}
	return n
}

// badFlags are what is wrong with a book's details, if anything: a junk
// title, the author's name as the title (its own author's, or a two-word name
// of someone with three books or more: title and author swapped), an
// uploader's nickname as the author, no author.
func badFlags(b store.ReviewBook, people map[string]int) []string {
	var flags []string
	t := strings.TrimSpace(b.Title)
	if junkTitleRE.MatchString(t) {
		flags = append(flags, "junk_title")
	}
	if ft := fold(t); ft != "" {
		own := false
		for _, a := range b.Authors {
			own = own || fold(a) == ft
		}
		if own || (people[ft] >= 3 && len(strings.Fields(ft)) >= 2) {
			flags = append(flags, "title_is_author")
		}
	}
	uploader := len(b.Authors) > 0
	for _, a := range b.Authors {
		uploader = uploader && uploaderRE.MatchString(strings.TrimSpace(a))
	}
	if uploader {
		flags = append(flags, "uploader_author")
	}
	if len(b.Authors) == 0 {
		flags = append(flags, "no_author")
	}
	return flags
}
