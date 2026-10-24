package server

// The catalogue as people browse it: libraries, books, authors, series,
// tags, shelves, home, search and stats.

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

func (s *Server) routesCatalog(api chi.Router) {}

func (s *Server) rawCatalog(raw chi.Router) {}

func (s *Server) handleExportJSON(w http.ResponseWriter, r *http.Request) {}

func (s *Server) handleImportJSON(w http.ResponseWriter, r *http.Request) {}

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
