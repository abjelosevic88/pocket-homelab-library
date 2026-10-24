package server

// Covers, author photos, metadata lookups (search, apply, fill what's
// missing), author enrichment and deleting books with their files.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-library/internal/covers"
	"github.com/abjelosevic88/pocket-homelab-library/internal/formats"
	"github.com/abjelosevic88/pocket-homelab-library/internal/metadata"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

// ---- covers ----

func (s *Server) serveImage(w http.ResponseWriter, r *http.Request, dir, name string) {
	path, err := covers.Thumb(dir, name, queryInt(r, "w"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	w.Header().Set("Content-Type", "image/jpeg")
	if r.URL.Query().Get("v") != "" {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "private, max-age=300")
	}
	w.Header().Set("ETag", `"`+strings.TrimSuffix(filepath.Base(path), ".jpg")+`"`)
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

// handleCover serves a book's cover (?w= for a smaller one) to people who may open the book.
func (s *Server) handleCover(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name, lib, err := s.store.BookCoverFile(ctx, idParam(r, "id"))
	if err != nil || name == "" || !s.mayOpenLibrary(ctx, lib) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	s.serveImage(w, r, s.cfg.CoversDir, name)
}

func (s *Server) mayOpenLibrary(ctx context.Context, libraryID int64) bool {
	for _, id := range s.allowedLibraries(ctx, userFrom(ctx)) {
		if id == libraryID {
			return true
		}
	}
	return false
}

func (s *Server) handleAuthorPhoto(w http.ResponseWriter, r *http.Request) {
	name, err := s.store.AuthorPhotoFile(r.Context(), idParam(r, "id"))
	if err != nil || name == "" {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	s.serveImage(w, r, filepath.Join(s.cfg.CoversDir, "authors"), name)
}

// handleSetCover replaces a book's cover with an uploaded image (multipart
// "file") or one from a web address (JSON {"url"}); the cover is then locked.
func (s *Server) handleSetCover(w http.ResponseWriter, r *http.Request) {
	if !s.requireEditor(w, r) {
		return
	}
	b := s.bookFor(w, r, idParam(r, "id"))
	if b == nil {
		return
	}
	var img []byte
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		r.Body = http.MaxBytesReader(w, r.Body, 25<<20)
		f, _, err := r.FormFile("file")
		if err != nil {
			writeErr(w, http.StatusBadRequest, "expected an image in the field \"file\"")
			return
		}
		img, err = io.ReadAll(f)
		f.Close()
		if err != nil {
			writeErr(w, http.StatusRequestEntityTooLarge, "the image is too large")
			return
		}
	} else {
		var in struct {
			URL string `json:"url"`
		}
		if err := decode(r, &in); err != nil || in.URL == "" {
			writeErr(w, http.StatusBadRequest, "give an image file or a url")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()
		var err error
		if img, err = s.scan.meta.FetchImage(ctx, in.URL, 25<<20); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := s.saveCover(r.Context(), b, img); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if b = s.bookFor(w, r, b.ID); b != nil {
		writeJSON(w, http.StatusOK, b)
	}
}

func (s *Server) saveCover(ctx context.Context, b *store.Book, img []byte) error {
	name, color, err := covers.Save(s.cfg.CoversDir, fmt.Sprintf("book-%d", b.ID), img)
	if err != nil {
		return err
	}
	if err := s.store.SetBookCover(ctx, b.ID, name, color); err != nil {
		return err
	}
	return s.store.SetLocked(ctx, b.ID, addField(b.Locked, "cover"))
}

func (s *Server) handleDeleteCover(w http.ResponseWriter, r *http.Request) {
	if !s.requireEditor(w, r) {
		return
	}
	b := s.bookFor(w, r, idParam(r, "id"))
	if b == nil {
		return
	}
	covers.Remove(s.cfg.CoversDir, fmt.Sprintf("book-%d", b.ID), "")
	if err := s.store.SetBookCover(r.Context(), b.ID, "", ""); err != nil {
		s.fail(w, err, "cover")
		return
	}
	_ = s.store.SetLocked(r.Context(), b.ID, addField(b.Locked, "cover"))
	w.WriteHeader(http.StatusNoContent)
}

func addField(list []string, fields ...string) []string {
	out := append([]string{}, list...)
	for _, f := range fields {
		found := false
		for _, x := range out {
			found = found || x == f
		}
		if !found {
			out = append(out, f)
		}
	}
	return out
}

// ---- metadata lookups ----

func (s *Server) googleKey(ctx context.Context) string {
	st, err := s.store.GetSettings(ctx, 0)
	if err != nil || st.GoogleBooksKey == "" {
		return ""
	}
	k, err := s.open(st.GoogleBooksKey)
	if err != nil {
		return ""
	}
	return k
}

func (s *Server) handleMetadataSearch(w http.ResponseWriter, r *http.Request) {
	if !s.requireEditor(w, r) {
		return
	}
	b := s.bookFor(w, r, idParam(r, "id"))
	if b == nil {
		return
	}
	q, isbn := strings.TrimSpace(r.URL.Query().Get("q")), r.URL.Query().Get("isbn")
	if q == "" && isbn == "" {
		isbn = b.ISBN
		q = b.Title
		if len(b.Authors) > 0 {
			q += " " + b.Authors[0].Name
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	cands, warnings, err := s.scan.meta.Search(ctx, q, isbn, s.googleKey(ctx), 10)
	if err != nil && len(cands) == 0 {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if len(warnings) > 0 {
		w.Header().Set("X-Warnings", strings.Join(warnings, "; "))
	}
	writeJSON(w, http.StatusOK, cands)
}

// handleMetadataApply copies the chosen fields of a lookup result onto a
// book; they are locked so the next scan keeps them.
func (s *Server) handleMetadataApply(w http.ResponseWriter, r *http.Request) {
	if !s.requireEditor(w, r) {
		return
	}
	b := s.bookFor(w, r, idParam(r, "id"))
	if b == nil {
		return
	}
	var in struct {
		Candidate metadata.Candidate `json:"candidate"`
		Fields    []string           `json:"fields"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	fields, coverErr := s.applyCandidate(ctx, b, in.Candidate, in.Fields, false)
	if len(fields) == 0 && coverErr != nil {
		writeErr(w, http.StatusBadGateway, coverErr.Error())
		return
	}
	if b = s.bookFor(w, r, b.ID); b != nil {
		if coverErr != nil {
			w.Header().Set("X-Warnings", "cover: "+coverErr.Error())
		}
		writeJSON(w, http.StatusOK, b)
	}
}

// applyCandidate writes chosen fields (onlyEmpty: only those the book lacks)
// and locks them; it returns the fields written and a cover error, if any.
func (s *Server) applyCandidate(ctx context.Context, b *store.Book, c metadata.Candidate, fields []string, onlyEmpty bool) ([]string, error) {
	m := store.MetaOf(b)
	var done []string
	set := func(name string, empty bool, apply func()) {
		if onlyEmpty && !empty {
			return
		}
		apply()
		done = append(done, name)
	}
	var coverErr error
	for _, f := range fields {
		switch f {
		case "title":
			if c.Title != "" {
				set(f, m.Title == "", func() { m.Title, m.SortTitle = c.Title, "" })
			}
		case "subtitle":
			if c.Subtitle != "" {
				set(f, m.Subtitle == "", func() { m.Subtitle = c.Subtitle })
			}
		case "authors":
			if len(c.Authors) > 0 {
				set(f, len(m.Authors) == 0, func() { m.Authors = c.Authors })
			}
		case "description":
			if c.Description != "" {
				set(f, m.Description == "", func() { m.Description = c.Description })
			}
		case "publisher":
			if c.Publisher != "" {
				set(f, m.Publisher == "", func() { m.Publisher = c.Publisher })
			}
		case "published":
			if c.Published != "" {
				set(f, m.Published == "", func() { m.Published = formats.NormalizeDate(c.Published) })
			}
		case "isbn":
			if isbn := formats.NormalizeISBN(c.ISBN); isbn != "" {
				set(f, m.ISBN == "", func() { m.ISBN = isbn; m.Identifiers = withID(m.Identifiers, "isbn", isbn) })
			}
		case "language":
			if l := formats.Language(c.Language); l != "" {
				set(f, m.Language == "", func() { m.Language = l })
			}
		case "pages":
			if c.Pages > 0 {
				set(f, m.Pages == 0, func() { m.Pages = c.Pages })
			}
		case "series":
			if c.Series != "" {
				set(f, m.Series == "", func() { m.Series, m.SeriesIndex = c.Series, c.SeriesIndex })
			}
		case "tags":
			if len(c.Tags) > 0 {
				set(f, len(m.Tags) == 0, func() { m.Tags = c.Tags })
			}
		case "identifiers":
			if len(c.Identifiers) > 0 {
				set(f, true, func() {
					for k, v := range c.Identifiers {
						m.Identifiers = withID(m.Identifiers, k, v)
					}
				})
			}
		case "cover":
			if c.Cover != "" && (!onlyEmpty || b.Cover == "") {
				img, err := s.scan.meta.FetchImage(ctx, c.Cover, 25<<20)
				if err == nil {
					err = s.saveCover(ctx, b, img)
				}
				if err != nil {
					coverErr = err
				} else {
					done = append(done, "cover")
					b.Locked = addField(b.Locked, "cover")
				}
			}
		}
	}
	var metaFields []string
	for _, f := range done {
		if f != "cover" {
			metaFields = append(metaFields, f)
		}
	}
	if len(metaFields) > 0 {
		if c.Source != "" {
			m.MetaSource = c.Source
		}
		if err := s.store.SaveBookMeta(ctx, b.ID, m); err != nil {
			return nil, err
		}
	}
	if len(done) > 0 {
		_ = s.store.SetLocked(ctx, b.ID, addField(b.Locked, done...))
	}
	return done, coverErr
}

func withID(ids map[string]string, k, v string) map[string]string {
	if ids == nil {
		ids = map[string]string{}
	}
	if v != "" {
		ids[k] = v
	}
	return ids
}

// handleFetchMissing starts filling empty descriptions, covers, ISBNs,
// dates and publishers from Open Library / Google Books, only for books a
// lookup matches confidently (same title, a shared author).
func (s *Server) handleFetchMissing(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in struct {
		LibraryID int64 `json:"library_id"`
		Limit     int   `json:"limit"`
	}
	_ = decode(r, &in)
	if in.Limit <= 0 || in.Limit > 5000 {
		in.Limit = 500
	}
	s.scan.mu.Lock()
	if s.scan.fetch.Running {
		s.scan.mu.Unlock()
		writeErr(w, http.StatusConflict, "already running")
		return
	}
	s.scan.fetch = jobStatus{Running: true}
	s.scan.mu.Unlock()
	go s.fetchMissing(context.WithoutCancel(r.Context()), in.LibraryID, in.Limit)
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (s *Server) fetchMissing(ctx context.Context, libraryID int64, limit int) {
	ids, err := s.store.BooksMissingMeta(ctx, libraryID, limit)
	s.scan.mu.Lock()
	s.scan.fetch.Total = len(ids)
	if err != nil {
		s.scan.fetch.Error = err.Error()
	}
	s.scan.mu.Unlock()
	key := s.googleKey(ctx)
	for _, id := range ids {
		b, err := s.store.GetBookAny(ctx, id)
		filled := false
		if err == nil && b.Title != "" && len(b.Authors) > 0 {
			q := b.Title + " " + b.Authors[0].Name
			lctx, cancel := context.WithTimeout(ctx, 40*time.Second)
			cands, _, err := s.scan.meta.Search(lctx, q, b.ISBN, key, 5)
			if err == nil {
				names := []string{}
				for _, a := range b.Authors {
					names = append(names, a.Name)
				}
				for _, c := range cands {
					if metadata.Confident(c, b.Title, names) || (b.ISBN != "" && c.ISBN == b.ISBN) {
						done, _ := s.applyCandidate(lctx, b, c, []string{"description", "cover", "isbn", "published", "publisher"}, true)
						filled = len(done) > 0
						break
					}
				}
			}
			cancel()
			time.Sleep(time.Second)
		}
		s.scan.mu.Lock()
		s.scan.fetch.Done++
		if filled {
			s.scan.fetch.Filled++
		}
		s.scan.mu.Unlock()
	}
	s.scan.mu.Lock()
	s.scan.fetch.Running = false
	s.scan.mu.Unlock()
	s.log.Info("metadata: filled missing", "books", len(ids), "filled", s.scan.fetch.Filled)
}

// ---- authors ----

// enrichWorker looks authors up in Open Library one a second: a biography,
// a photo and links. An author not found is not asked again (see enrich endpoint).
func (s *Server) enrichWorker(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(3 * time.Minute):
	}
	for {
		authors, err := s.store.AuthorsToEnrich(ctx, 20)
		wait := 10 * time.Minute
		if err == nil && len(authors) > 0 {
			for _, a := range authors {
				if ctx.Err() != nil {
					return
				}
				s.enrichAuthor(ctx, a.ID, a.Name)
				time.Sleep(time.Second)
			}
			wait = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (s *Server) enrichAuthor(ctx context.Context, id int64, name string) error {
	lctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	info, err := s.scan.meta.Author(lctx, name)
	if err != nil {
		if lctx.Err() != nil || strings.Contains(err.Error(), "HTTP 5") {
			return err // try again later
		}
		return s.store.SetAuthorInfo(ctx, id, "", "", "", nil)
	}
	if info == nil {
		return s.store.SetAuthorInfo(ctx, id, "", "", "", nil)
	}
	photo := ""
	if info.Photo != "" {
		if img, err := s.scan.meta.FetchImage(lctx, info.Photo, 10<<20); err == nil {
			if n, _, err := covers.Save(filepath.Join(s.cfg.CoversDir, "authors"), fmt.Sprintf("author-%d", id), img); err == nil {
				photo = n
			}
		}
	}
	return s.store.SetAuthorInfo(ctx, id, info.Bio, photo, info.Key, info.Links)
}

func (s *Server) handleEnrichAuthor(w http.ResponseWriter, r *http.Request) {
	if !s.requireEditor(w, r) {
		return
	}
	id := idParam(r, "id")
	name, err := s.store.AuthorName(r.Context(), id)
	if err != nil {
		s.fail(w, err, "author")
		return
	}
	if err := s.enrichAuthor(r.Context(), id, name); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	a, err := s.store.GetAuthor(r.Context(), id, s.allowedLibraries(r.Context(), userFrom(r.Context())))
	if err != nil {
		s.fail(w, err, "author")
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// ---- deleting ----

// handleDeleteBook removes a book with its files (?files=1). Without
// deleting the files the next scan would only bring it back.
func (s *Server) handleDeleteBook(w http.ResponseWriter, r *http.Request) {
	if !s.requireEditor(w, r) {
		return
	}
	b := s.bookFor(w, r, idParam(r, "id"))
	if b == nil {
		return
	}
	ctx := r.Context()
	present := 0
	for _, f := range b.Files {
		if !f.Missing {
			present++
		}
	}
	if present > 0 && !queryBool(r, "files") {
		writeErr(w, http.StatusBadRequest, "a book stays while its files are in the library folder; delete it with its files or move them away")
		return
	}
	lib, err := s.store.GetLibrary(ctx, b.LibraryID)
	if err != nil {
		s.fail(w, err, "library")
		return
	}
	for _, f := range b.Files {
		if !insideAny(f.Path, lib.Paths) {
			writeErr(w, http.StatusBadRequest, "a file of this book is outside the library's folders: "+filepath.Base(f.Path))
			return
		}
	}
	s.scan.writeMu.Lock()
	defer s.scan.writeMu.Unlock()
	dirs := map[string]bool{}
	for _, f := range b.Files {
		if err := os.Remove(f.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.fail(w, err, "delete file")
			return
		}
		side := filepath.Join(filepath.Dir(f.Path), formats.BaseName(f.Path)+".opf")
		if _, err := os.Stat(side); err == nil {
			_ = os.Remove(side)
		}
		dirs[filepath.Dir(f.Path)] = true
		s.log.Info("book file deleted", "by", userFrom(ctx).Email, "path", f.Path)
	}
	for d := range dirs {
		if insideAny(d, lib.Paths) && !isRootPath(d, lib.Paths) {
			_ = os.Remove(d) // only when empty
		}
	}
	if err := s.store.DeleteBook(ctx, b.ID); err != nil {
		s.fail(w, err, "delete book")
		return
	}
	covers.Remove(s.cfg.CoversDir, fmt.Sprintf("book-%d", b.ID), "")
	_ = s.store.PruneOrphans(ctx)
	w.WriteHeader(http.StatusNoContent)
}

func insideAny(p string, roots []string) bool {
	for _, root := range roots {
		rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return true
		}
	}
	return false
}

func isRootPath(p string, roots []string) bool {
	for _, root := range roots {
		if filepath.Clean(root) == filepath.Clean(p) {
			return true
		}
	}
	return false
}
