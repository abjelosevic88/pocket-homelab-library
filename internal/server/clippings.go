package server

// CrossPoint's clippings (firmware 2.0, "Sync Clippings" in its CrossPoint
// Sync settings): the device talks to its sync server's crosspoint-sync
// extension at <kosync base>/api/v1/clippings/<document>. It first asks
// ?cursor=0&limit=1&format=reader and needs sync_version 2, uploads each new
// clipping and each deletion as {"items":[one]} wanting {"accepted":1}, then
// pages through the server's clippings one at a time by cursor.
//
// Every clipping is kept as the device sent it (it needs its layout hints
// back on a restore) and becomes a highlight of the book. A deleted id stays
// a tombstone, so a late upload never brings it back.

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
	"github.com/abjelosevic88/pocket-homelab-library/internal/xpointer"
)

// clippingItem is the fields of a CrossPoint clipping this server reads.
type clippingItem struct {
	ID          string  `json:"id"`
	Deleted     int     `json:"deleted"`
	Spine       *int    `json:"spine"`
	StartPage   int     `json:"start_page"`
	EndPage     int     `json:"end_page"`
	Pages       int     `json:"pages"`
	Chapter     string  `json:"chapter"`
	Text        string  `json:"text"`
	Note        *string `json:"note"`
	Color       *string `json:"color"`
	CreatedAt   int64   `json:"created_at"`
	StartOffset *int64  `json:"start_offset"`
	EndOffset   *int64  `json:"end_offset"`
	Para        *int    `json:"para"`
}

func (s *Server) handleClippingsGet(w http.ResponseWriter, r *http.Request) {
	doc := strings.ToLower(chi.URLParam(r, "document"))
	if !kosyncDocumentRE.MatchString(doc) {
		kosyncErr(w, http.StatusForbidden, 2004, "Field 'document' not provided.")
		return
	}
	ctx := r.Context()
	u := userFrom(ctx)
	q := r.URL.Query()
	limit := queryInt(r, "limit")
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 100)
	reader := q.Get("format") == "reader"
	var list []store.Clipping
	var more bool
	var err error
	cursor := int64(0)
	legacy := q.Get("cursor") == "" && q.Get("since") != ""
	if legacy {
		since, _ := strconv.ParseInt(q.Get("since"), 10, 64)
		list, more, err = s.store.ClippingsSince(ctx, u.ID, doc, since, limit)
	} else {
		cursor, _ = strconv.ParseInt(q.Get("cursor"), 10, 64)
		list, more, err = s.store.ClippingsAfter(ctx, u.ID, doc, max(cursor, 0), limit)
	}
	if err != nil {
		kosyncErr(w, http.StatusInternalServerError, 2000, "Unknown server error")
		return
	}
	items := make([]map[string]any, 0, len(list))
	until := time.Now().Unix()
	for _, c := range list {
		item := map[string]any{}
		_ = json.Unmarshal([]byte(c.Item), &item)
		item["id"] = c.ClipID
		item["revision"] = c.Seq
		item["updated_at"] = c.UpdatedUnix
		if c.Deleted {
			item = map[string]any{"id": c.ClipID, "deleted": 1, "revision": c.Seq, "updated_at": c.UpdatedUnix}
			// the firmware checks every field's type even on a tombstone
			for _, k := range []string{"spine", "start_page", "end_page", "pages", "start_word", "end_word", "words", "created_at", "layout_signature"} {
				item[k] = 0
			}
			item["text"], item["chapter"] = "", ""
		} else {
			item["deleted"] = 0
		}
		if reader {
			delete(item, "note")
			delete(item, "color")
		}
		items = append(items, item)
		cursor = c.Seq
		if legacy {
			until = c.UpdatedUnix
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sync_version": 2, "items": items, "cursor": cursor, "more": more, "until": until})
}

func (s *Server) handleClippingsPut(w http.ResponseWriter, r *http.Request) {
	doc := strings.ToLower(chi.URLParam(r, "document"))
	if !kosyncDocumentRE.MatchString(doc) {
		kosyncErr(w, http.StatusForbidden, 2004, "Field 'document' not provided.")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 512<<10))
	var in struct {
		Items []json.RawMessage `json:"items"`
	}
	if err != nil || json.Unmarshal(raw, &in) != nil || len(in.Items) > 50 {
		kosyncErr(w, http.StatusForbidden, 2003, "Invalid request")
		return
	}
	ctx := r.Context()
	u := userFrom(ctx)
	s.sync.mapMu.Lock()
	defer s.sync.mapMu.Unlock()
	prev, _ := s.store.GetKosync(ctx, u.ID, doc)
	b, f := s.mapDocument(ctx, u, doc, nil, prev)
	accepted, created := 0, 0
	for _, rawItem := range in.Items {
		var it clippingItem
		if json.Unmarshal(rawItem, &it) != nil || it.ID == "" || len(it.ID) > 64 {
			continue
		}
		old, _ := s.store.GetClipping(ctx, u.ID, doc, it.ID)
		if old != nil && old.Deleted {
			accepted++ // a deleted id is never revived
			continue
		}
		c := &store.Clipping{Document: doc, ClipID: it.ID}
		if old != nil {
			c.BookID, c.AnnotationID = old.BookID, old.AnnotationID
		}
		if b != nil {
			c.BookID = b.ID
		}
		if it.Deleted == 1 {
			c.Deleted, c.Item = true, `{}`
			if c.AnnotationID > 0 {
				_ = s.store.DeleteAnnotation(ctx, u.ID, c.AnnotationID)
				c.AnnotationID = 0
			}
			if err := s.store.PutClipping(ctx, u.ID, c); err == nil {
				accepted++
			}
			continue
		}
		// keep note and colour when an update leaves them out
		item := map[string]any{}
		_ = json.Unmarshal(rawItem, &item)
		if old != nil {
			var was map[string]any
			_ = json.Unmarshal([]byte(old.Item), &was)
			for _, k := range []string{"note", "color"} {
				if _, ok := item[k]; !ok && was[k] != nil {
					item[k] = was[k]
				}
			}
		}
		for _, k := range []string{"revision", "updated_at", "deleted"} {
			delete(item, k)
		}
		ib, _ := json.Marshal(item)
		c.Item = string(ib)
		if b != nil {
			if id, isNew := s.clippingAnnotation(r, u, b, f, c.AnnotationID, it, item); id > 0 {
				c.AnnotationID = id
				if isNew {
					created++
				}
			}
		}
		if err := s.store.PutClipping(ctx, u.ID, c); err == nil {
			accepted++
		}
	}
	if created > 0 && b != nil {
		s.publish(ctx, u, "library.highlighted", b.Title, map[string]any{"book_id": b.ID, "title": b.Title, "count": created, "device": "CrossPoint"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": accepted, "until": time.Now().Unix()})
}

// clippingAnnotation saves a clipping as a highlight of the book.
func (s *Server) clippingAnnotation(r *http.Request, u *store.User, b *store.Book, f *store.File, annID int64, it clippingItem, item map[string]any) (int64, bool) {
	loc := map[string]any{"type": "crosspoint", "document": f.PartialMD5}
	pct := 0.0
	if it.Spine != nil {
		loc["spine"] = *it.Spine
		frac := 0.0
		if it.Pages > 0 {
			frac = math.Min(1, float64(it.StartPage)/float64(it.Pages))
		}
		loc["fraction"] = frac
		if isEPUB(f) {
			if o, err := xpointer.Overall(f.Path, *it.Spine, frac); err == nil {
				pct = o
			}
		}
	}
	for _, k := range []string{"para", "start_offset", "end_offset", "start_page", "pages"} {
		if v, ok := item[k]; ok {
			loc[k] = v
		}
	}
	lb, _ := json.Marshal(loc)
	a := &store.Annotation{ID: annID, BookID: b.ID, FileID: f.ID, Kind: "highlight", Text: trim(it.Text, 8192), Chapter: trim(it.Chapter, 200),
		Locator: string(lb), Percentage: pct, Source: "crosspoint", ExternalID: it.ID}
	if it.Note != nil {
		a.Note = trim(*it.Note, 8192)
	}
	if it.Color != nil && *it.Color != "" {
		a.Color = *it.Color
	}
	if it.CreatedAt > 0 {
		a.CreatedAt = time.Unix(it.CreatedAt, 0).UTC().Format(time.RFC3339Nano)
	}
	created, err := s.store.SaveAnnotation(r.Context(), u.ID, a)
	if err != nil {
		// the highlight was removed in the web app: make it again
		a.ID = 0
		if created, err = s.store.SaveAnnotation(r.Context(), u.ID, a); err != nil {
			s.log.Warn("crosspoint clipping", "err", err)
			return 0, false
		}
	}
	return a.ID, created
}
