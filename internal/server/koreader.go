package server

// The KOReader plugin's API at /koreader/v1 (koreader/pocketlibrary.koplugin):
// books sent to the device, highlights of the open book, and the reading
// time KOReader's statistics measured. /koreader/plugin.zip is the plugin
// itself. The plugin signs in like KOReader sync: the account's email and a
// device's app password.

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
	"github.com/abjelosevic88/pocket-homelab-library/internal/xpointer"
	"github.com/abjelosevic88/pocket-homelab-library/koreader"
)

func (s *Server) routesKOReader(r chi.Router) {
	r.Get("/plugin.zip", s.handlePluginZip)
	r.Route("/v1", func(r chi.Router) {
		r.Use(middleware.Compress(5))
		r.Use(s.deviceAuth(authKOReader))
		r.Get("/me", s.handleKOReaderMe)
		r.Get("/queue", s.handleKOReaderQueue)
		r.Post("/queue/{id}/done", s.handleKOReaderQueueDone)
		r.Post("/annotations", s.handleKOReaderAnnotations)
		r.Post("/stats", s.handleKOReaderStats)
	})
}

var (
	pluginZipOnce sync.Once
	pluginZip     []byte
	pluginZipErr  error
)

// handlePluginZip serves the plugin as a zip with pocketlibrary.koplugin/ at
// the top, ready to unpack into KOReader's plugins folder.
func (s *Server) handlePluginZip(w http.ResponseWriter, r *http.Request) {
	pluginZipOnce.Do(func() { pluginZip, pluginZipErr = buildPluginZip() })
	if pluginZipErr != nil {
		s.fail(w, pluginZipErr, "plugin")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="pocketlibrary.koplugin.zip"`)
	http.ServeContent(w, r, "pocketlibrary.koplugin.zip", time.Time{}, bytes.NewReader(pluginZip))
}

func buildPluginZip() ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := fs.WalkDir(koreader.Plugin, koreader.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := fs.ReadFile(koreader.Plugin, p)
		if err != nil {
			return err
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: p, Method: zip.Deflate, Modified: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
		if err != nil {
			return err
		}
		_, err = w.Write(body)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (s *Server) handleKOReaderMe(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	out := map[string]any{"name": u.Name, "email": u.Email, "device": nil, "server_version": Version}
	if d := loginFrom(r.Context()).device; d != nil {
		out["device"] = map[string]any{"id": d.ID, "name": d.Name, "kind": d.Kind}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleKOReaderQueue lists the books waiting for this device (or, when the
// plugin signed in with something else, for any of the person's KOReader devices).
func (s *Server) handleKOReaderQueue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	l := loginFrom(ctx)
	pending, err := s.store.SyncPendingSends(ctx, u.ID, l.deviceID(), "koreader")
	if err != nil {
		s.fail(w, err, "queue")
		return
	}
	libs := s.allowedLibraries(ctx, u)
	out := []map[string]any{}
	for _, p := range pending {
		b, err := s.store.GetBook(ctx, u.ID, p.BookID, libs)
		if err != nil {
			continue
		}
		var f *store.File
		for i := range b.Files {
			if b.Files[i].ID == p.FileID {
				f = &b.Files[i]
			}
		}
		if f == nil {
			continue
		}
		format := p.Format
		if format == "" {
			format = f.Format
		}
		ext := format
		if ext == "kepub" {
			ext = "kepub.epub"
		}
		name := opdsFileName(b) + "." + ext
		href := fmt.Sprintf("/opds/download/%d/%s", f.ID, url.PathEscape(name))
		if format != f.Format {
			href += "?format=" + url.QueryEscape(format)
		}
		authors := make([]string, 0, len(b.Authors))
		for _, a := range b.Authors {
			authors = append(authors, a.Name)
		}
		out = append(out, map[string]any{"id": p.ID, "book_id": b.ID, "file_id": f.ID, "title": b.Title, "authors": strings.Join(authors, ", "),
			"format": format, "size": f.Size, "filename": name, "url": href})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleKOReaderQueueDone(w http.ResponseWriter, r *http.Request) {
	if err := s.store.SyncMarkSendFetched(r.Context(), userFrom(r.Context()).ID, idParam(r, "id")); err != nil {
		s.fail(w, err, "queue")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// koAnnotation is one of KOReader's annotations as the plugin sends it.
type koAnnotation struct {
	Datetime        string          `json:"datetime"`
	DatetimeUpdated string          `json:"datetime_updated"`
	Text            string          `json:"text"`
	Note            string          `json:"note"`
	Chapter         string          `json:"chapter"`
	Pageno          int             `json:"pageno"`
	Pos0            json.RawMessage `json:"pos0"`
	Pos1            json.RawMessage `json:"pos1"`
	Page            json.RawMessage `json:"page"`
	Color           string          `json:"color"`
	Drawer          string          `json:"drawer"`
}

// pointer reads a KOReader position: an XPointer string for EPUBs, a table for PDFs.
func pointer(raw json.RawMessage) string {
	var v string
	if json.Unmarshal(raw, &v) == nil {
		return v
	}
	return ""
}

// koColor maps KOReader's highlight colours onto the web reader's.
func koColor(color, drawer string) string {
	if drawer == "underscore" || drawer == "strikeout" {
		return "underline"
	}
	switch strings.ToLower(color) {
	case "green", "olive":
		return "green"
	case "blue", "cyan":
		return "blue"
	case "red", "pink":
		return "pink"
	case "purple":
		return "purple"
	}
	return "yellow"
}

// koTime turns KOReader's local "2024-01-02 15:04:05" into UTC RFC 3339.
func koTime(v string) string {
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(v), time.Local); err == nil {
		return t.UTC().Format(time.RFC3339Nano)
	}
	return ""
}

func (s *Server) handleKOReaderAnnotations(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Document    string         `json:"document"`
		Title       string         `json:"title"`
		Authors     string         `json:"authors"`
		Filename    string         `json:"filename"`
		Pages       int            `json:"pages"`
		Complete    bool           `json:"complete"`
		Annotations []koAnnotation `json:"annotations"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	doc := strings.ToLower(strings.TrimSpace(in.Document))
	if !kosyncDocumentRE.MatchString(doc) {
		writeErr(w, http.StatusBadRequest, "document (the book's partial MD5) is required")
		return
	}
	ctx := r.Context()
	u := userFrom(ctx)
	prev, _ := s.store.GetKosync(ctx, u.ID, doc)
	b, f := s.mapDocument(ctx, u, doc, &docMeta{Title: in.Title, Authors: in.Authors, Filename: in.Filename}, prev)
	if b == nil {
		writeJSON(w, http.StatusOK, map[string]any{"matched": false})
		return
	}
	pages := in.Pages
	if pages == 0 {
		pages = f.Pages
	}
	keep := map[string]bool{}
	created, updated := 0, 0
	for _, a := range in.Annotations {
		pos0 := pointer(a.Pos0)
		if pos0 == "" {
			pos0 = pointer(a.Page)
		}
		if a.Datetime == "" {
			continue
		}
		sum := sha1.Sum([]byte(a.Datetime + "|" + pos0))
		ext := hex.EncodeToString(sum[:])
		keep[ext] = true
		loc := map[string]any{"type": "kosync", "document": doc, "page": a.Pageno}
		if pos0 != "" {
			loc["pos0"] = pos0
		}
		if p1 := pointer(a.Pos1); p1 != "" {
			loc["pos1"] = p1
		}
		pct := 0.0
		if isEPUB(f) && strings.HasPrefix(pos0, "/") {
			if spine, frac, err := xpointer.Resolve(f.Path, pos0); err == nil {
				loc["spine"], loc["fraction"] = spine, frac
				pct, _ = xpointer.Overall(f.Path, spine, frac)
			}
		} else if pages > 0 && a.Pageno > 0 {
			pct = min(1, float64(a.Pageno)/float64(pages))
		}
		kind := "highlight"
		if strings.TrimSpace(a.Text) == "" {
			kind = "bookmark"
			if strings.TrimSpace(a.Note) != "" {
				kind = "note"
			}
		}
		lb, _ := json.Marshal(loc)
		ann := &store.Annotation{BookID: b.ID, FileID: f.ID, Kind: kind, Text: trim(a.Text, 8192), Note: trim(a.Note, 8192), Color: koColor(a.Color, a.Drawer),
			Locator: string(lb), Chapter: trim(a.Chapter, 200), Percentage: pct, Page: a.Pageno, Source: "koreader", ExternalID: ext, CreatedAt: koTime(a.Datetime)}
		isNew, err := s.store.SaveAnnotation(ctx, u.ID, ann)
		if err != nil {
			s.log.Warn("koreader annotation", "err", err)
			continue
		}
		if isNew {
			created++
		} else {
			updated++
		}
	}
	deleted := 0
	if in.Complete {
		list, err := s.store.Annotations(ctx, u.ID, store.AnnotationFilter{BookID: b.ID})
		if err == nil {
			for _, a := range list {
				if a.Source == "koreader" && !keep[a.ExternalID] {
					if s.store.DeleteAnnotation(ctx, u.ID, a.ID) == nil {
						deleted++
					}
				}
			}
		}
	}
	if created > 0 {
		s.publish(ctx, u, "library.highlighted", b.Title, map[string]any{"book_id": b.ID, "title": b.Title, "count": created, "device": "KOReader"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"matched": true, "book_id": b.ID, "title": b.Title, "created": created, "updated": updated, "deleted": deleted})
}

// koPage is one row of KOReader's statistics (page_stat_data).
type koPage struct {
	Page       int   `json:"page"`
	StartTime  int64 `json:"start_time"`
	Duration   int   `json:"duration"`
	TotalPages int   `json:"total_pages"`
}

// handleKOReaderStats turns the page times KOReader measured into reading
// sessions (a pause of more than 30 minutes starts a new one). The guesses
// made from sync positions over the same stretch are replaced.
func (s *Server) handleKOReaderStats(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Document string   `json:"document"`
		Title    string   `json:"title"`
		Authors  string   `json:"authors"`
		Device   string   `json:"device"`
		Pages    []koPage `json:"pages"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	doc := strings.ToLower(strings.TrimSpace(in.Document))
	if !kosyncDocumentRE.MatchString(doc) {
		writeErr(w, http.StatusBadRequest, "document (the book's partial MD5) is required")
		return
	}
	ctx := r.Context()
	u := userFrom(ctx)
	prev, _ := s.store.GetKosync(ctx, u.ID, doc)
	b, f := s.mapDocument(ctx, u, doc, &docMeta{Title: in.Title, Authors: in.Authors}, prev)
	if b == nil {
		writeJSON(w, http.StatusOK, map[string]any{"matched": false})
		return
	}
	device := strings.TrimSpace(in.Device)
	if device == "" {
		device = "KOReader"
		if d := loginFrom(ctx).device; d != nil {
			device = d.Name
		}
	}
	n := s.importKOReaderStats(ctx, u, b, f, doc, device, in.Pages)
	writeJSON(w, http.StatusOK, map[string]any{"matched": true, "book_id": b.ID, "sessions": n})
}

func (s *Server) importKOReaderStats(ctx context.Context, u *store.User, b *store.Book, f *store.File, doc, device string, pages []koPage) int {
	key := fmt.Sprintf("kostats:%d:%s", u.ID, doc)
	var last int64
	if v, _, ok := s.store.CacheGet(ctx, key); ok {
		last, _ = strconv.ParseInt(v, 10, 64)
	}
	var rows []koPage
	for _, p := range pages {
		if p.StartTime > last && p.Duration > 0 && p.Duration < 6*3600 {
			rows = append(rows, p)
		}
	}
	if len(rows) == 0 {
		return 0
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].StartTime < rows[j].StartTime })
	type group struct {
		start, end  int64
		seconds     int
		first, last koPage
		pages       map[int]bool
	}
	var groups []*group
	for _, p := range rows {
		g := (*group)(nil)
		if len(groups) > 0 {
			g = groups[len(groups)-1]
		}
		if g == nil || p.StartTime-g.end > int64(store.SessionGap/time.Second) {
			g = &group{start: p.StartTime, first: p, pages: map[int]bool{}}
			groups = append(groups, g)
		}
		g.seconds += p.Duration
		g.end = max(g.end, p.StartTime+int64(p.Duration))
		g.last = p
		g.pages[p.Page] = true
	}
	pct := func(p koPage) float64 {
		if p.TotalPages <= 0 {
			return 0
		}
		return min(1, float64(p.Page)/float64(p.TotalPages))
	}
	n := 0
	for _, g := range groups {
		from, to := time.Unix(g.start, 0), time.Unix(g.end, 0)
		_ = s.store.SyncDeleteEstimatedSessions(ctx, u.ID, b.ID, from, to)
		if _, err := s.store.RecordSession(ctx, u.ID, store.SessionReport{BookID: b.ID, FileID: f.ID, Device: device, Source: "koreader",
			At: to, Pct: pct(g.last), PrevPct: max(0, pct(g.first)-1/float64(max(1, g.first.TotalPages))), Seconds: g.seconds, Pages: len(g.pages)}); err != nil {
			s.log.Warn("koreader stats", "err", err)
			continue
		}
		n++
	}
	_ = s.store.CachePut(ctx, key, strconv.FormatInt(rows[len(rows)-1].StartTime, 10))
	return n
}
