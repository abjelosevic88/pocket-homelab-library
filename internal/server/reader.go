package server

// The web reader: file contents, comic pages, progress, annotations and
// reading sessions.

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/abjelosevic88/pocket-homelab-library/internal/formats"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

// readerState keeps comic archives open between page requests: a CBR is
// unpacked once and a CBZ's directory read once, then closed when idle.
type readerState struct {
	mu     sync.Mutex
	comics map[int64]*openComic // by file id
}

type openComic struct {
	mu    sync.Mutex
	c     *formats.Comic
	mtime int64
	used  time.Time
}

const (
	comicIdle    = 2 * time.Minute
	comicsOpened = 8
)

func newReaderState() *readerState { return &readerState{comics: map[int64]*openComic{}} }

// forget has nothing per person to drop: open archives are shared by file.
func (r *readerState) forget(uid int64) {}

func (s *Server) startReader(ctx context.Context) {
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				s.reader.closeIdle(0)
				return
			case <-t.C:
				s.reader.closeIdle(comicIdle)
			}
		}
	}()
}

// closeIdle closes archives not used for longer than idle (0 = all).
func (r *readerState) closeIdle(idle time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, oc := range r.comics {
		if idle == 0 || time.Since(oc.used) > idle {
			oc.mu.Lock()
			_ = oc.c.Close()
			oc.mu.Unlock()
			delete(r.comics, id)
		}
	}
}

// comic returns the open archive of a file, opening it (and closing the
// least recently used one when too many are open) as needed.
func (s *Server) comic(f *store.File) (*openComic, error) {
	r := s.reader
	r.mu.Lock()
	if oc, ok := r.comics[f.ID]; ok && oc.mtime == f.MTime {
		oc.used = time.Now()
		r.mu.Unlock()
		return oc, nil
	}
	r.mu.Unlock()
	c, err := formats.OpenComic(f.Path, filepath.Join(s.cfg.CacheDir, "comics"))
	if err != nil {
		return nil, err
	}
	oc := &openComic{c: c, mtime: f.MTime, used: time.Now()}
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.comics[f.ID]; ok {
		_ = old.c.Close()
	}
	if len(r.comics) >= comicsOpened {
		var oldest int64
		var at time.Time
		for id, x := range r.comics {
			if at.IsZero() || x.used.Before(at) {
				oldest, at = id, x.used
			}
		}
		_ = r.comics[oldest].c.Close()
		delete(r.comics, oldest)
	}
	r.comics[f.ID] = oc
	return oc, nil
}

func (s *Server) routesReader(api chi.Router) {
	api.Get("/books/{id}/reader", s.handleReaderOpen)
	api.Post("/books/{id}/progress", s.handleReaderProgress)
	api.Get("/books/{id}/annotations", s.handleBookAnnotations)
	api.Post("/books/{id}/annotations", s.handleAddAnnotation)
	api.Get("/annotations", s.handleAnnotations)
	api.Put("/annotations/{id}", s.handleEditAnnotation)
	api.Delete("/annotations/{id}", s.handleDeleteAnnotation)
	api.Get("/sessions", s.handleSessions)
	api.Delete("/sessions/{id}", s.handleDeleteSession)
}

func (s *Server) rawReader(raw chi.Router) {
	raw.Get("/files/{id}/content", s.handleFileContent)
	raw.Head("/files/{id}/content", s.handleFileContent)
	raw.Get("/files/{id}/pages", s.handleComicPages)
	raw.Get("/files/{id}/pages/{n}", s.handleComicPage)
	raw.Get("/annotations/export", s.handleExportAnnotations)
}

// ---- engines ----

// readerEngine says which part of the web reader opens a format.
func readerEngine(format string) string {
	switch format {
	case "epub", "kepub", "mobi", "azw", "azw3", "prc", "fb2", "fbz":
		return "foliate"
	case "pdf":
		return "pdf"
	case "cbz", "cbr", "cb7", "cbt":
		return "comic"
	}
	return "none"
}

// readerRank orders files for reading: the most faithful engine first.
func readerRank(format string) int {
	for i, f := range []string{"epub", "kepub", "azw3", "mobi", "azw", "prc", "fb2", "fbz", "pdf", "cbz", "cbr", "cb7", "cbt"} {
		if f == format {
			return i
		}
	}
	return 99
}

// pickReaderFile chooses the file to open: the one asked for, else the one
// the person read last, else the best readable one.
func pickReaderFile(files []store.File, want, last int64) *store.File {
	for _, id := range []int64{want, last} {
		if id == 0 {
			continue
		}
		for i := range files {
			if files[i].ID == id && !files[i].Missing {
				return &files[i]
			}
		}
	}
	var best *store.File
	for i := range files {
		f := &files[i]
		if f.Missing {
			continue
		}
		if best == nil || readerRank(f.Format) < readerRank(best.Format) {
			best = f
		}
	}
	return best
}

func (s *Server) handleReaderOpen(w http.ResponseWriter, r *http.Request) {
	b := s.bookFor(w, r, idParam(r, "id"))
	if b == nil {
		return
	}
	var last int64
	if b.State != nil {
		last = b.State.FileID
	}
	f := pickReaderFile(b.Files, queryInt64(r, "file"), last)
	engine := "none"
	if f != nil {
		engine = readerEngine(f.Format)
	}
	anns, err := s.store.Annotations(r.Context(), actorID(r.Context()), store.AnnotationFilter{BookID: b.ID})
	if err != nil {
		s.fail(w, err, "annotations")
		return
	}
	out := map[string]any{"book": b, "file": f, "engine": engine, "annotations": anns, "locator": "", "percentage": 0.0, "state": b.State, "toc_hint": nil}
	if b.State != nil {
		out["locator"], out["percentage"] = b.State.Locator, b.State.Percentage
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- file contents and comic pages ----

func (s *Server) handleFileContent(w http.ResponseWriter, r *http.Request) {
	_, f := s.fileFor(w, r, idParam(r, "id"))
	if f == nil {
		return
	}
	fh, err := os.Open(f.Path)
	if err != nil {
		writeErr(w, http.StatusNotFound, "the file is not on disk any more")
		return
	}
	defer fh.Close()
	fi, err := fh.Stat()
	if err != nil {
		s.fail(w, err, "file")
		return
	}
	w.Header().Set("Content-Type", formats.MIME(f.Format))
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", fi.ModTime(), fh)
}

func (s *Server) handleComicPages(w http.ResponseWriter, r *http.Request) {
	_, f := s.fileFor(w, r, idParam(r, "id"))
	if f == nil {
		return
	}
	if !formats.IsComic(f.Format) {
		writeErr(w, http.StatusBadRequest, "not a comic")
		return
	}
	oc, err := s.comic(f)
	if err != nil {
		s.fail(w, err, "open comic")
		return
	}
	type pageOut struct {
		N    int    `json:"n"`
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	oc.mu.Lock()
	pages := oc.c.Pages()
	oc.mu.Unlock()
	out := make([]pageOut, len(pages))
	for i, p := range pages {
		out[i] = pageOut{N: i + 1, Name: filepath.Base(p.Name), Size: p.Size}
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(out), "pages": out})
}

// pageWidths are the sizes a page may be scaled to (anything else snaps up).
var pageWidths = []int{480, 720, 960, 1280, 1600}

func snapWidth(w int) int {
	if w <= 0 {
		return 0
	}
	for _, p := range pageWidths {
		if w <= p {
			return p
		}
	}
	return 0 // wider than the largest: the original
}

func (s *Server) handleComicPage(w http.ResponseWriter, r *http.Request) {
	_, f := s.fileFor(w, r, idParam(r, "id"))
	if f == nil {
		return
	}
	n, _ := strconv.Atoi(chi.URLParam(r, "n"))
	if !formats.IsComic(f.Format) || n < 1 {
		writeErr(w, http.StatusBadRequest, "no such page")
		return
	}
	width := snapWidth(queryInt(r, "w"))
	w.Header().Set("Cache-Control", "private, max-age=86400")
	cache := ""
	if width > 0 {
		cache = filepath.Join(s.cfg.CacheDir, "pages", fmt.Sprintf("%d-%d", f.ID, f.MTime), fmt.Sprintf("%d-%d.jpg", n, width))
		if b, err := os.ReadFile(cache); err == nil {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(b)
			return
		}
	}
	oc, err := s.comic(f)
	if err != nil {
		s.fail(w, err, "open comic")
		return
	}
	oc.mu.Lock()
	if n > len(oc.c.Pages()) {
		oc.mu.Unlock()
		writeErr(w, http.StatusNotFound, "no such page")
		return
	}
	rc, mime, err := oc.c.Page(n - 1)
	var data []byte
	if err == nil {
		data, err = io.ReadAll(io.LimitReader(rc, 64<<20))
		rc.Close()
	}
	oc.mu.Unlock()
	if err != nil {
		s.fail(w, err, "page")
		return
	}
	if width > 0 {
		if small, ok := scaleToWidth(data, width); ok {
			if err := os.MkdirAll(filepath.Dir(cache), 0o755); err == nil {
				_ = os.WriteFile(cache, small, 0o644)
			}
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(small)
			return
		}
	}
	if mime == "" {
		mime = http.DetectContentType(data)
	}
	w.Header().Set("Content-Type", mime)
	_, _ = w.Write(data)
}

// scaleToWidth makes a JPEG of an image no wider than width; false when the
// image is already that narrow or can't be decoded.
func scaleToWidth(data []byte, width int) ([]byte, bool) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= width {
		return nil, false
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}
	h := src.Bounds().Dy() * width / src.Bounds().Dx()
	dst := image.NewRGBA(image.Rect(0, 0, width, max(h, 1)))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

// ---- progress ----

type progressIn struct {
	FileID     int64           `json:"file_id"`
	Percentage float64         `json:"percentage"`
	Locator    json.RawMessage `json:"locator"`
	Seconds    int             `json:"seconds"`
	Pages      int             `json:"pages"`
	Device     string          `json:"device"`
}

func rawString(m json.RawMessage) string {
	if len(m) == 0 || string(m) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(m, &s) == nil {
		return s
	}
	return string(m)
}

// handleReaderProgress takes the web reader's position: from fetch as JSON,
// or from sendBeacon (text/plain) when the page is closing.
func (s *Server) handleReaderProgress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	b := s.bookFor(w, r, idParam(r, "id"))
	if b == nil {
		return
	}
	var in progressIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.FileID != 0 && !hasFile(b, in.FileID) {
		writeErr(w, http.StatusBadRequest, "that file is not this book's")
		return
	}
	// a locator may arrive as an object or already as a JSON string
	loc := rawString(in.Locator)
	if len(loc) > 8<<10 {
		writeErr(w, http.StatusBadRequest, "locator too long")
		return
	}
	device := "Web reader"
	if d := strings.TrimSpace(in.Device); d != "" {
		device = trim("Web reader · "+d, 100)
	}
	prevPct := 0.0
	if b.State != nil {
		prevPct = b.State.Percentage
	}
	st, ch, stale, err := s.store.SetProgress(ctx, u.ID, b.ID, store.Progress{FileID: in.FileID, Percentage: in.Percentage, Locator: loc, Device: device, AutoFinish: true})
	if err != nil {
		s.fail(w, err, "progress")
		return
	}
	secs := min(max(in.Seconds, 0), 120)
	if !stale && (secs > 0 || in.Pages > 0) {
		if _, err := s.store.RecordSession(ctx, u.ID, store.SessionReport{BookID: b.ID, FileID: in.FileID, Device: device, Source: "web",
			Pct: in.Percentage, PrevPct: prevPct, Seconds: max(secs, 1), Pages: max(in.Pages, 0)}); err != nil {
			s.log.Warn("reading session", "err", err)
		}
	}
	s.publishProgress(ctx, u, b, ch)
	writeJSON(w, http.StatusOK, map[string]any{"state": st, "stale": stale})
}

func hasFile(b *store.Book, id int64) bool {
	for _, f := range b.Files {
		if f.ID == id {
			return true
		}
	}
	return false
}

// publishProgress tells the Core bus a book was started or finished.
func (s *Server) publishProgress(ctx context.Context, u *store.User, b *store.Book, ch store.StatusChange) {
	if !ch.Started && !ch.Finished {
		return
	}
	authors := make([]string, 0, len(b.Authors))
	for _, a := range b.Authors {
		authors = append(authors, a.Name)
	}
	payload := map[string]any{"book_id": b.ID, "title": b.Title, "authors": authors, "isbn": b.ISBN}
	subject := fmt.Sprintf("book:%d", b.ID)
	if ch.Started {
		s.publish(ctx, u, "library.started", subject, payload)
	}
	if ch.Finished {
		s.publish(ctx, u, "library.finished", subject, payload)
	}
}

// ---- annotations ----

func (s *Server) handleBookAnnotations(w http.ResponseWriter, r *http.Request) {
	b := s.bookFor(w, r, idParam(r, "id"))
	if b == nil {
		return
	}
	list, err := s.store.Annotations(r.Context(), actorID(r.Context()), store.AnnotationFilter{BookID: b.ID, Kind: r.URL.Query().Get("kind")})
	if err != nil {
		s.fail(w, err, "annotations")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type annotationIn struct {
	Kind       string          `json:"kind"`
	Text       string          `json:"text"`
	Note       string          `json:"note"`
	Color      string          `json:"color"`
	Locator    json.RawMessage `json:"locator"`
	Chapter    string          `json:"chapter"`
	Percentage float64         `json:"percentage"`
	Page       int             `json:"page"`
	FileID     int64           `json:"file_id"`
}

var annotationColors = map[string]bool{"yellow": true, "green": true, "blue": true, "pink": true, "purple": true, "underline": true}

func (s *Server) handleAddAnnotation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	b := s.bookFor(w, r, idParam(r, "id"))
	if b == nil {
		return
	}
	var in annotationIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.FileID != 0 && !hasFile(b, in.FileID) {
		in.FileID = 0
	}
	if in.Color != "" && !annotationColors[in.Color] {
		in.Color = "yellow"
	}
	a := &store.Annotation{BookID: b.ID, FileID: in.FileID, Kind: in.Kind, Text: trim(in.Text, 20000), Note: trim(in.Note, 20000), Color: in.Color,
		Locator: trim(rawString(in.Locator), 8<<10), Chapter: trim(in.Chapter, 300), Percentage: in.Percentage, Page: in.Page, Source: "web"}
	if _, err := s.store.SaveAnnotation(ctx, u.ID, a); err != nil {
		s.fail(w, err, "save annotation")
		return
	}
	if a.Kind != "bookmark" && (a.Text != "" || a.Note != "") {
		s.publish(ctx, u, "library.highlighted", fmt.Sprintf("annotation:%d", a.ID), map[string]any{
			"book_id": b.ID, "title": b.Title, "kind": a.Kind, "text": trim(a.Text, 1000), "note": trim(a.Note, 1000), "chapter": a.Chapter})
	}
	saved, err := s.store.GetAnnotation(ctx, u.ID, a.ID)
	if err != nil {
		s.fail(w, err, "annotation")
		return
	}
	writeJSON(w, http.StatusCreated, saved)
}

func (s *Server) handleEditAnnotation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	a, err := s.store.GetAnnotation(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "annotation")
		return
	}
	var in struct {
		Note  *string `json:"note"`
		Color *string `json:"color"`
		Kind  *string `json:"kind"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Note != nil {
		a.Note = trim(*in.Note, 20000)
	}
	if in.Color != nil && annotationColors[*in.Color] {
		a.Color = *in.Color
	}
	if in.Kind != nil {
		a.Kind = *in.Kind
	}
	if _, err := s.store.SaveAnnotation(ctx, uid, a); err != nil {
		s.fail(w, err, "save annotation")
		return
	}
	saved, err := s.store.GetAnnotation(ctx, uid, a.ID)
	if err != nil {
		s.fail(w, err, "annotation")
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleDeleteAnnotation(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteAnnotation(r.Context(), actorID(r.Context()), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete annotation")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAnnotations lists the person's annotations across the books they can
// still open (Highlights page), newest first.
func (s *Server) handleAnnotations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	q := r.URL.Query()
	limit := queryInt(r, "limit")
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	list, err := s.store.Annotations(ctx, u.ID, store.AnnotationFilter{BookID: queryInt64(r, "book"), Kind: q.Get("kind"), Q: q.Get("q"), Since: q.Get("since"), Limit: limit, Offset: queryInt(r, "offset")})
	if err != nil {
		s.fail(w, err, "annotations")
		return
	}
	writeJSON(w, http.StatusOK, s.inLibraries(ctx, u, list))
}

// inLibraries keeps annotations of books in libraries the person may still open.
func (s *Server) inLibraries(ctx context.Context, u *store.User, list []store.Annotation) []store.Annotation {
	ok, err := s.store.BooksInLibraries(ctx, annotationBooks(list), s.allowedLibraries(ctx, u))
	if err != nil {
		return list
	}
	out := []store.Annotation{}
	for _, a := range list {
		if ok[a.BookID] {
			out = append(out, a)
		}
	}
	return out
}

func annotationBooks(list []store.Annotation) []int64 {
	seen := map[int64]bool{}
	var ids []int64
	for _, a := range list {
		if !seen[a.BookID] {
			seen[a.BookID] = true
			ids = append(ids, a.BookID)
		}
	}
	return ids
}

// handleExportAnnotations downloads the person's highlights and notes, of one
// book or all, as Markdown, JSON or CSV.
func (s *Server) handleExportAnnotations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	bookID := queryInt64(r, "book")
	var book *store.Book
	if bookID > 0 {
		if book = s.bookFor(w, r, bookID); book == nil {
			return
		}
	}
	list, err := s.store.Annotations(ctx, u.ID, store.AnnotationFilter{BookID: bookID})
	if err != nil {
		s.fail(w, err, "annotations")
		return
	}
	list = s.inLibraries(ctx, u, list)
	name := "highlights"
	if book != nil {
		name = safeFileName(book.Title) + " - highlights"
	}
	switch r.URL.Query().Get("format") {
	case "json":
		w.Header().Set("Content-Disposition", attachmentName(name+".json"))
		writeJSON(w, http.StatusOK, list)
	case "csv":
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", attachmentName(name+".csv"))
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"book", "kind", "text", "note", "chapter", "percentage", "page", "color", "source", "created_at"})
		for _, a := range list {
			_ = cw.Write([]string{a.BookTitle, a.Kind, a.Text, a.Note, a.Chapter, strconv.FormatFloat(a.Percentage*100, 'f', 1, 64), strconv.Itoa(a.Page), a.Color, a.Source, a.CreatedAt})
		}
		cw.Flush()
	default:
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", attachmentName(name+".md"))
		_, _ = io.WriteString(w, annotationsMarkdown(book, list))
	}
}

// attachmentName is a Content-Disposition for a download with any name.
func attachmentName(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 32 || r > 126 || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	var enc strings.Builder
	for _, c := range []byte(name) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-._~", c) >= 0 {
			enc.WriteByte(c)
		} else {
			fmt.Fprintf(&enc, "%%%02X", c)
		}
	}
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, ascii, enc.String())
}

func safeFileName(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || r < 32 {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "book"
	}
	return trim(s, 120)
}

// annotationsMarkdown writes highlights the way people paste them into notes:
// a heading per book, each highlight a quote with where it is and the note.
func annotationsMarkdown(book *store.Book, list []store.Annotation) string {
	var b strings.Builder
	if book != nil {
		names := make([]string, 0, len(book.Authors))
		for _, a := range book.Authors {
			names = append(names, a.Name)
		}
		fmt.Fprintf(&b, "# %s", book.Title)
		if len(names) > 0 {
			fmt.Fprintf(&b, " — %s", strings.Join(names, ", "))
		}
		b.WriteString("\n\n")
	} else {
		b.WriteString("# Highlights\n\n")
	}
	current := int64(-1)
	for _, a := range list {
		if book == nil && a.BookID != current {
			current = a.BookID
			fmt.Fprintf(&b, "## %s\n\n", a.BookTitle)
		}
		where := []string{}
		if a.Chapter != "" {
			where = append(where, a.Chapter)
		}
		if a.Page > 0 {
			where = append(where, fmt.Sprintf("page %d", a.Page))
		} else if a.Percentage > 0 {
			where = append(where, fmt.Sprintf("%.0f%%", a.Percentage*100))
		}
		if len(a.CreatedAt) >= 10 {
			where = append(where, a.CreatedAt[:10])
		}
		switch {
		case a.Kind == "bookmark":
			b.WriteString("- Bookmark\n")
		case a.Text != "":
			for _, line := range strings.Split(strings.TrimSpace(a.Text), "\n") {
				fmt.Fprintf(&b, "> %s\n", line)
			}
		}
		if len(where) > 0 {
			fmt.Fprintf(&b, "\n*%s*\n", strings.Join(where, " · "))
		}
		if a.Note != "" {
			fmt.Fprintf(&b, "\n**Note:** %s\n", strings.TrimSpace(a.Note))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// ---- reading sessions ----

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	q := r.URL.Query()
	from, to := q.Get("from"), q.Get("to")
	if !validDate(from) || !validDate(to) {
		writeErr(w, http.StatusBadRequest, "from and to are YYYY-MM-DD")
		return
	}
	limit := queryInt(r, "limit")
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	list, err := s.store.SessionsWithBooks(ctx, u.ID, store.SessionFilter{BookID: queryInt64(r, "book"), From: from, To: to, Limit: limit, Offset: queryInt(r, "offset")})
	if err != nil {
		s.fail(w, err, "sessions")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteReadingSession(r.Context(), actorID(r.Context()), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete session")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
