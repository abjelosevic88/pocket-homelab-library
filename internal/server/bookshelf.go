package server

// Pocket Bookshelf through Pocket Core (link "bookshelf": books:read and
// books:write). Bookshelf is the person's reading log and keeps which of its
// books is which book here (keyed by this library's book id). A book page
// shows that entry (status, rating, a link); the person can add the book
// there, link it to a book already on their shelves, or unlink it; and the
// status and rating they set here are written there too.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

func (s *Server) routesBookshelf(api chi.Router) {
	api.Post("/books/{id}/bookshelf", s.handleBookshelfAdd)
	api.Put("/books/{id}/bookshelf", s.handleBookshelfLink)
	api.Delete("/books/{id}/bookshelf", s.handleBookshelfUnlink)
	api.Get("/books/{id}/bookshelf/search", s.handleBookshelfSearch)
}

type bsCacheEntry struct {
	at  time.Time
	val map[string]any
}

var (
	bsMu    sync.Mutex
	bsCache = map[string]bsCacheEntry{}
)

func bsKey(uid, bookID int64) string { return fmt.Sprintf("%d:%d", uid, bookID) }

func bsForget(uid, bookID int64) {
	bsMu.Lock()
	delete(bsCache, bsKey(uid, bookID))
	bsMu.Unlock()
}

// bsClient calls the person's Pocket Bookshelf with a fresh link token.
type bsClient struct {
	s                   *Server
	base, public, token string
}

func (s *Server) bookshelfFor(ctx context.Context, u *store.User, scope string) (*bsClient, error) {
	tok, err := s.core.ActAs(ctx, u.Email, "bookshelf", scope)
	if err != nil {
		return nil, err
	}
	if tok.URL == "" {
		return nil, errors.New("Pocket Bookshelf has no address in Pocket Core")
	}
	pub := strings.TrimRight(tok.PublicURL, "/")
	if pub == "" {
		pub = strings.TrimRight(tok.URL, "/")
	}
	return &bsClient{s: s, base: strings.TrimRight(tok.URL, "/"), public: pub, token: tok.AccessToken}, nil
}

// do sends one request; out may be nil. The status is returned with the error
// so callers can tell "not there" (404) from "not reachable".
func (c *bsClient) do(ctx context.Context, method, path string, body, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.s.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 300))
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(msg, &e) == nil && e.Error != "" {
			return res.StatusCode, errors.New(e.Error)
		}
		return res.StatusCode, fmt.Errorf("Pocket Bookshelf answered HTTP %d", res.StatusCode)
	}
	if out == nil {
		return res.StatusCode, nil
	}
	return res.StatusCode, json.NewDecoder(res.Body).Decode(out)
}

// bsBook is a book as Bookshelf describes it.
type bsBook struct {
	ID      int64   `json:"id"`
	Title   string  `json:"title"`
	Authors string  `json:"authors"`
	Status  string  `json:"status"`
	Rating  float64 `json:"rating"`
	URL     string  `json:"url"`
}

// bsLink is Bookshelf's answer for one of this library's books.
type bsLink struct {
	Linked   bool    `json:"linked"`
	Unlinked bool    `json:"unlinked"`
	Book     *bsBook `json:"book"`
}

func (c *bsClient) link(ctx context.Context, bookID int64) (*bsLink, error) {
	var l bsLink
	if _, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/library/links/%d", bookID), nil, &l); err != nil {
		return nil, err
	}
	return &l, nil
}

func (c *bsClient) search(ctx context.Context, q url.Values) []bsBook {
	var raw json.RawMessage
	if _, err := c.do(ctx, http.MethodGet, "/api/v1/books?"+q.Encode(), nil, &raw); err != nil {
		return nil
	}
	var list []bsBook
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var page struct {
		Items []bsBook `json:"items"`
		Books []bsBook `json:"books"`
	}
	if json.Unmarshal(raw, &page) == nil {
		if page.Items != nil {
			return page.Items
		}
		return page.Books
	}
	return nil
}

// bookshelfInfo is the person's own entry for this book in Pocket Bookshelf,
// when Core links the two for them; nil when there is no Core or the link is
// off. Fields: available (on their shelves), linked (Bookshelf keeps it as
// this book; else a guess by ISBN or title), unlinked (they unlinked it),
// rating (half stars), status, url, can_write (books:write is on).
func (s *Server) bookshelfInfo(ctx context.Context, u *store.User, b *store.Book) map[string]any {
	if !s.coreConnected() {
		return nil
	}
	key := bsKey(u.ID, b.ID)
	bsMu.Lock()
	if e, ok := bsCache[key]; ok && time.Since(e.at) < 10*time.Minute {
		bsMu.Unlock()
		return e.val
	}
	bsMu.Unlock()
	c, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	val := s.lookupBookshelf(c, u, b)
	bsMu.Lock()
	if len(bsCache) > 5000 {
		bsCache = map[string]bsCacheEntry{}
	}
	bsCache[key] = bsCacheEntry{at: time.Now(), val: val}
	bsMu.Unlock()
	return val
}

func (s *Server) lookupBookshelf(ctx context.Context, u *store.User, b *store.Book) map[string]any {
	c, err := s.bookshelfFor(ctx, u, "books:read")
	if err != nil {
		return nil
	}
	_, werr := s.core.ActAs(ctx, u.Email, "bookshelf", "books:write")
	canWrite := werr == nil
	found := func(h bsBook, linked bool) map[string]any {
		link := h.URL
		if link == "" {
			link = fmt.Sprintf("%s/books/%d", c.public, h.ID)
		}
		return map[string]any{"available": true, "linked": linked, "id": h.ID, "title": h.Title, "url": link,
			"rating": int(h.Rating*2 + 0.5), "status": h.Status, "can_write": canWrite}
	}
	if l, err := c.link(ctx, b.ID); err == nil {
		switch {
		case l.Linked && l.Book != nil:
			return found(*l.Book, true)
		case l.Unlinked:
			return map[string]any{"available": false, "linked": false, "unlinked": true, "url": c.public, "can_write": canWrite}
		}
	}
	// Not linked (or a Bookshelf from before links): a best guess by ISBN, then exact title.
	var hits []bsBook
	if b.ISBN != "" {
		hits = c.search(ctx, url.Values{"isbn": {b.ISBN}})
	}
	if len(hits) == 0 {
		for _, h := range c.search(ctx, url.Values{"q": {b.Title}}) {
			if strings.EqualFold(strings.TrimSpace(h.Title), strings.TrimSpace(b.Title)) {
				hits = append(hits, h)
			}
		}
	}
	if len(hits) == 0 {
		return map[string]any{"available": false, "linked": false, "url": c.public, "can_write": canWrite}
	}
	return found(hits[0], false)
}

// bsStatus maps this library's status to Bookshelf's ("" = none).
func bsStatus(st string) string {
	switch st {
	case "want", "reading", "read":
		return st
	case "dnf":
		return "abandoned"
	}
	return ""
}

// applyBookshelf sends a status and/or rating to the person's Bookshelf for
// this book, which adds the book there (linked) when it isn't yet.
func (s *Server) applyBookshelf(ctx context.Context, u *store.User, b *store.Book, status string, rating *int, day string) error {
	c, err := s.bookshelfFor(ctx, u, "books:write")
	if err != nil {
		return err
	}
	body := map[string]any{"book": feedBook(b)}
	if status != "" {
		body["status"] = status
	}
	if day != "" {
		body["day"] = day
	}
	if rating != nil {
		body["rating"] = float64(*rating) / 2
	}
	_, err = c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/library/links/%d/apply", b.ID), body, nil)
	bsForget(u.ID, b.ID)
	return err
}

// mirrorState writes what the person just set on a book here to their
// Bookshelf, in the background. A status goes along with a rating only for a
// book Bookshelf doesn't have yet (it needs one to add it), so a rating never
// moves the status kept there; a book they unlinked is left alone.
func (s *Server) mirrorState(u *store.User, b *store.Book, statusSet bool, ub *store.UserBook, rating *int, day string) {
	if !s.coreConnected() || ub == nil || (!statusSet && rating == nil) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		status := ""
		if statusSet {
			status = bsStatus(ub.Status)
			if status == "" && rating == nil {
				return // cleared here: nothing to say there
			}
		} else if info := s.bookshelfInfo(ctx, u, b); info == nil || info["unlinked"] == true {
			return
		} else if info["available"] != true {
			status = bsStatus(ub.Status) // not there yet: it goes in with the status it has here
		}
		if err := s.applyBookshelf(ctx, u, b, status, rating, day); err != nil && err.Error() != "unlinked" {
			s.log.Debug("bookshelf: mirror", "book", b.ID, "err", err)
		}
	}()
}

// POST /books/{id}/bookshelf — put the book on the person's Bookshelf
// (linked), with its status and rating here.
func (s *Server) handleBookshelfAdd(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	b := s.bookFor(w, r, idParam(r, "id"))
	if b == nil {
		return
	}
	status, rating := "want", (*int)(nil)
	if ub, err := s.store.GetUserBook(ctx, u.ID, b.ID); err == nil && ub != nil {
		if st := bsStatus(ub.Status); st != "" {
			status = st
		}
		if ub.Rating > 0 {
			rating = &ub.Rating
		}
	}
	if err := s.applyBookshelf(ctx, u, b, status, rating, ""); err != nil {
		writeErr(w, http.StatusBadGateway, bsErr(err))
		return
	}
	writeJSON(w, http.StatusOK, s.bookshelfInfo(ctx, u, b))
}

// PUT /books/{id}/bookshelf {"bookshelf_id"} — this book is that one on the shelves.
func (s *Server) handleBookshelfLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	b := s.bookFor(w, r, idParam(r, "id"))
	if b == nil {
		return
	}
	var in struct {
		BookshelfID int64 `json:"bookshelf_id"`
	}
	if err := decode(r, &in); err != nil || in.BookshelfID <= 0 {
		writeErr(w, http.StatusBadRequest, "bookshelf_id is required")
		return
	}
	c, err := s.bookshelfFor(ctx, u, "books:write")
	if err != nil {
		writeErr(w, http.StatusBadGateway, bsErr(err))
		return
	}
	if _, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/api/v1/library/links/%d", b.ID), map[string]any{"book_id": in.BookshelfID}, nil); err != nil {
		writeErr(w, http.StatusBadGateway, bsErr(err))
		return
	}
	bsForget(u.ID, b.ID)
	writeJSON(w, http.StatusOK, s.bookshelfInfo(ctx, u, b))
}

// DELETE /books/{id}/bookshelf — not the same book: Bookshelf stops taking
// this one in until it is linked again.
func (s *Server) handleBookshelfUnlink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	b := s.bookFor(w, r, idParam(r, "id"))
	if b == nil {
		return
	}
	c, err := s.bookshelfFor(ctx, u, "books:write")
	if err != nil {
		writeErr(w, http.StatusBadGateway, bsErr(err))
		return
	}
	if _, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/library/links/%d", b.ID), nil, nil); err != nil {
		writeErr(w, http.StatusBadGateway, bsErr(err))
		return
	}
	bsForget(u.ID, b.ID)
	writeJSON(w, http.StatusOK, s.bookshelfInfo(ctx, u, b))
}

// GET /books/{id}/bookshelf/search?q= — books on the person's shelves to link to
// (the book's own title when q is empty).
func (s *Server) handleBookshelfSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	b := s.bookFor(w, r, idParam(r, "id"))
	if b == nil {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		q = b.Title
	}
	c, err := s.bookshelfFor(ctx, u, "books:read")
	if err != nil {
		writeErr(w, http.StatusBadGateway, bsErr(err))
		return
	}
	out := []map[string]any{}
	for _, h := range c.search(ctx, url.Values{"q": {q}}) {
		if len(out) == 20 {
			break
		}
		out = append(out, map[string]any{"id": h.ID, "title": h.Title, "authors": h.Authors, "status": h.Status, "rating": int(h.Rating*2 + 0.5)})
	}
	writeJSON(w, http.StatusOK, out)
}

// bsErr words a failure to reach Bookshelf for the person.
func bsErr(err error) string {
	var le *core.LinkError
	if errors.As(err, &le) {
		switch le.Reason {
		case "off_admin", "off_person", "not_declared", "not_offered":
			return "Pocket Bookshelf: this needs the link “Pocket Library → Pocket Bookshelf” (update your books) switched on in Pocket Core."
		}
		return "Pocket Bookshelf: " + le.Message
	}
	if err.Error() == "unlinked" {
		return "You unlinked this book from Pocket Bookshelf; link it to a book there first."
	}
	return "Pocket Bookshelf: " + err.Error()
}
