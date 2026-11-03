package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

// "Get it": from a book in the library a person can ask Pocket Requests
// (through Pocket Core, link requests:read + requests:write) for its
// audiobook or for the book in another language they read, and from an
// author's page for the author's other books. Library talks to Requests on
// the person's behalf; the browser never sees the link token. Without the
// link the buttons stay, greyed out, with the reason.

var requestsUse = core.Use{App: "requests", Scopes: []string{"requests:read", "requests:write"},
	Reason: "Ask for the audiobook of a book, or the book in another language, and more by its author (Get buttons), and show whether it is on its way"}

// rqClient reaches the person's Pocket Requests with a link token.
type rqClient struct {
	base, public, token string
	http                *http.Client
}

// rqError is Requests answering with an error.
type rqError struct {
	status    int
	msg       string
	requestID int64
	openURL   string
}

func (e *rqError) Error() string { return e.msg }

func (c *rqClient) do(ctx context.Context, method, path string, q url.Values, body, out any) error {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("Pocket Requests didn't answer: %w", err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		var e struct {
			Error     string `json:"error"`
			RequestID int64  `json:"request_id"`
			OpenURL   string `json:"open_url"`
		}
		_ = json.Unmarshal(b, &e)
		msg := strings.TrimSpace(e.Error)
		if msg == "" {
			msg = fmt.Sprintf("Pocket Requests answered HTTP %d", res.StatusCode)
		}
		return &rqError{status: res.StatusCode, msg: msg, requestID: e.RequestID, openURL: e.OpenURL}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(b, out)
}

func (c *rqClient) requestURL(id int64) string {
	if c.public == "" || id == 0 {
		return ""
	}
	return c.public + "/requests/" + strconv.FormatInt(id, 10)
}

func (c *rqClient) bookURL(key string) string {
	if c.public == "" || key == "" {
		return ""
	}
	return c.public + "/title/book/" + url.PathEscape(key)
}

// rqFor is the person's Requests, or why it can't be reached (greyed out).
func (s *Server) rqFor(ctx context.Context, email string) (*rqClient, string) {
	if !s.coreConnected() {
		return nil, "Needs Pocket Core: books are asked for in Pocket Requests, through Pocket Core."
	}
	lt, err := s.core.ActAs(ctx, email, "requests", "requests:read", "requests:write")
	if err != nil {
		var le *core.LinkError
		if errors.As(err, &le) {
			return nil, rqLinkReason(le)
		}
		return nil, "Pocket Core: " + err.Error()
	}
	return &rqClient{base: strings.TrimRight(lt.URL, "/"), public: strings.TrimRight(lt.PublicURL, "/"), token: lt.AccessToken, http: s.http}, ""
}

// rqLinkReason says why Library can't reach Pocket Requests for the person.
func rqLinkReason(le *core.LinkError) string {
	switch le.Reason {
	case "target_missing":
		return "Pocket Requests is not on this box."
	case "off_person":
		return "Switched off for you: turn on “Pocket Library → Pocket Requests” in Pocket Core, Settings → Apps acting for me."
	case "off_admin":
		return "Switched off for this box: an admin turns on “Pocket Library → Pocket Requests” in Pocket Core, Links."
	case "not_in_audience":
		return "You have no Pocket Requests: an admin can give you access in Pocket Core."
	case "not_offered", "not_declared":
		return "This Pocket Requests or Library is too old to link the two; update both."
	case "unknown_user":
		return "Your account is not in Pocket Core."
	}
	return "Pocket Requests: " + le.Message
}

// ---- what Requests says ----

// rqAsk is one of the person's requests for a book.
type rqAsk struct {
	ID       int64   `json:"id"`
	Kind     string  `json:"kind"`
	Language string  `json:"language"`
	Status   string  `json:"status"`
	State    string  `json:"state"`
	Progress float64 `json:"progress"`
	URL      string  `json:"url,omitempty"` // filled in by Library: the request's page
}

type rqHave struct {
	URL string `json:"url"`
}

// rqTitle is a book as Requests describes it.
type rqTitle struct {
	Kind     string `json:"kind"`
	BookKey  string `json:"book_key"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	Year     int    `json:"year"`
	Poster   string `json:"poster"`
	Book     struct {
		Have struct {
			Ebook     *rqHave `json:"ebook"`
			Audiobook *rqHave `json:"audiobook"`
		} `json:"have"`
		Asked       []rqAsk           `json:"asked"`
		HaveUnknown map[string]string `json:"have_unknown"`
	} `json:"book"`
}

type rqCanGet struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason"`
}

// getIt is what the book page's "Get it" card shows.
type getIt struct {
	Available bool      `json:"available"`        // Requests can be reached
	Reason    string    `json:"reason,omitempty"` // why not (greyed out with this)
	Found     bool      `json:"found"`            // Requests knows the book
	BookKey   string    `json:"book_key,omitempty"`
	BookURL   string    `json:"book_url,omitempty"` // the book's page in Requests
	AppURL    string    `json:"app_url,omitempty"`
	CanGet    *rqCanGet `json:"can_get,omitempty"` // Requests can fetch books at all
	// The audiobook: here (open it), asked for, or neither.
	Audiobook struct {
		Have    *rqHave `json:"have"`
		Request *rqAsk  `json:"request"`
		Unknown string  `json:"unknown,omitempty"`
	} `json:"audiobook"`
	// The ebook in the other languages the box reads (not this book's own).
	Languages []getItLang `json:"languages"`
	Language  string      `json:"language"` // this book's language, two letters ("" = unknown)
}

type getItLang struct {
	Code    string `json:"code"`
	Name    string `json:"name"`
	Request *rqAsk `json:"request"`
}

var langNames = map[string]string{"en": "English", "sr": "Serbian", "hr": "Croatian", "bs": "Bosnian", "de": "German", "fr": "French", "es": "Spanish",
	"it": "Italian", "ru": "Russian", "sl": "Slovenian", "mk": "Macedonian", "pl": "Polish", "pt": "Portuguese", "nl": "Dutch", "hu": "Hungarian"}

// langCode reads a book's language as two letters (eng → en, srp → sr, "Serbian" → sr).
func langCode(l string) string {
	l = strings.ToLower(strings.TrimSpace(l))
	switch l {
	case "":
		return ""
	case "eng", "english":
		return "en"
	case "srp", "scc", "serbian", "srpski", "sr-latn", "sr-cyrl":
		return "sr"
	case "hrv", "scr", "croatian", "hrvatski":
		return "hr"
	case "bos", "bosnian", "bosanski":
		return "bs"
	case "ger", "deu", "german":
		return "de"
	case "fre", "fra", "french":
		return "fr"
	case "spa", "spanish":
		return "es"
	case "ita", "italian":
		return "it"
	case "rus", "russian":
		return "ru"
	}
	if len(l) == 2 || (len(l) > 2 && (l[2] == '-' || l[2] == '_')) {
		return l[:2]
	}
	return ""
}

func langName(code string) string {
	if n := langNames[code]; n != "" {
		return n
	}
	return strings.ToUpper(code)
}

// live drops requests that no longer count (declined, withdrawn, removed).
func live(a *rqAsk) bool {
	return a != nil && a.Status != "declined" && a.Status != "cancelled" && a.State != "removed"
}

var (
	getItMu    sync.Mutex
	getItCache = map[string]getItCached{}
)

type getItCached struct {
	v  getIt
	at time.Time
}

const getItTTL = 45 * time.Second

func getItKey(uid, bookID int64) string {
	return strconv.FormatInt(uid, 10) + ":" + strconv.FormatInt(bookID, 10)
}

func forgetGetIt(uid int64) {
	getItMu.Lock()
	defer getItMu.Unlock()
	pre := strconv.FormatInt(uid, 10) + ":"
	for k := range getItCache {
		if strings.HasPrefix(k, pre) {
			delete(getItCache, k)
		}
	}
}

func firstAuthorName(b *store.Book) string {
	for _, a := range b.Authors {
		if n := strings.TrimSpace(a.Name); n != "" {
			return n
		}
	}
	return ""
}

func bookISBN(b *store.Book) string {
	return firstNonEmpty(b.ISBN, b.Identifiers["isbn"])
}

// lookupGetIt asks Requests about one book for the person (cached briefly).
func (s *Server) lookupGetIt(ctx context.Context, u *store.User, b *store.Book, fresh bool) getIt {
	key := getItKey(u.ID, b.ID)
	if !fresh {
		getItMu.Lock()
		c, ok := getItCache[key]
		getItMu.Unlock()
		if ok && time.Since(c.at) < getItTTL {
			return c.v
		}
	}
	v := s.fetchGetIt(ctx, u, b)
	getItMu.Lock()
	if len(getItCache) > 5000 {
		getItCache = map[string]getItCached{}
	}
	getItCache[key] = getItCached{v, time.Now()}
	getItMu.Unlock()
	return v
}

func (s *Server) fetchGetIt(ctx context.Context, u *store.User, b *store.Book) getIt {
	out := getIt{Languages: []getItLang{}, Language: langCode(b.Language)}
	c, why := s.rqFor(ctx, u.Email)
	if c == nil {
		out.Reason = why
		return out
	}
	out.Available, out.AppURL = true, c.public
	q := url.Values{"title": {b.Title}}
	if isbn := bookISBN(b); isbn != "" {
		q.Set("isbn", isbn)
	}
	if a := firstAuthorName(b); a != "" {
		q.Set("author", a)
	}
	var lk struct {
		Title           *rqTitle  `json:"title"`
		LanguagesWanted []string  `json:"languages_wanted"`
		CanGet          *rqCanGet `json:"can_get"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/books/lookup", q, nil, &lk); err != nil {
		var re *rqError
		if errors.As(err, &re) && re.status == http.StatusNotFound {
			out.Available, out.Reason = false, "This Pocket Requests can't find books yet; update it."
			return out
		}
		out.Available, out.Reason = false, err.Error()
		return out
	}
	out.CanGet = lk.CanGet
	if lk.Title == nil {
		return out
	}
	t := lk.Title
	out.Found, out.BookKey, out.BookURL = true, t.BookKey, c.bookURL(t.BookKey)
	out.Audiobook.Have = t.Book.Have.Audiobook
	out.Audiobook.Unknown = t.Book.HaveUnknown["audiobook"]
	for i := range t.Book.Asked {
		a := &t.Book.Asked[i]
		a.URL = c.requestURL(a.ID)
		if !live(a) {
			continue
		}
		if a.Kind == "audiobook" && out.Audiobook.Request == nil {
			out.Audiobook.Request = a
		}
	}
	for _, l := range lk.LanguagesWanted {
		l = langCode(l)
		if l == "" || l == out.Language {
			continue
		}
		row := getItLang{Code: l, Name: langName(l)}
		for i := range t.Book.Asked {
			a := &t.Book.Asked[i]
			if a.Kind == "ebook" && a.Language == l && live(a) {
				row.Request = a
				break
			}
		}
		out.Languages = append(out.Languages, row)
	}
	return out
}

// ---- API ----

func (s *Server) routesRequests(api chi.Router) {
	api.Get("/books/{id}/requests", s.handleBookRequests)
	api.Post("/books/{id}/requests", s.handleCreateBookRequest)
	api.Get("/authors/{id}/requests-more", s.handleAuthorMore)
	api.Post("/authors/{id}/requests", s.handleAuthorRequest)
}

// GET /api/v1/books/{id}/requests — the book page's "Get it" card.
func (s *Server) handleBookRequests(w http.ResponseWriter, r *http.Request) {
	b := s.bookFor(w, r, idParam(r, "id"))
	if b == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, s.lookupGetIt(ctx, userFrom(r.Context()), b, r.URL.Query().Get("fresh") == "1"))
}

// POST /api/v1/books/{id}/requests {"kind": "ebook"|"audiobook", "language", "note"}
// — ask for the audiobook, or the ebook in another language.
func (s *Server) handleCreateBookRequest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind     string `json:"kind"`
		Language string `json:"language"`
		Note     string `json:"note"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Kind != "ebook" && in.Kind != "audiobook" {
		writeErr(w, http.StatusBadRequest, "kind is ebook or audiobook")
		return
	}
	b := s.bookFor(w, r, idParam(r, "id"))
	if b == nil {
		return
	}
	u := userFrom(r.Context())
	body := map[string]any{"kind": in.Kind, "title": b.Title}
	// the key Requests found for the book page beats a fresh lookup
	getItMu.Lock()
	if cached, ok := getItCache[getItKey(u.ID, b.ID)]; ok && cached.v.BookKey != "" {
		body["book_key"] = cached.v.BookKey
	}
	getItMu.Unlock()
	if isbn := bookISBN(b); isbn != "" {
		body["isbn"] = isbn
	}
	if a := firstAuthorName(b); a != "" {
		body["author"] = a
	}
	if l := langCode(in.Language); l != "" {
		body["language"] = l
	}
	s.askRequests(w, r, u, body, in.Note)
}

// askRequests posts a request to Requests as the person and passes its answer on.
func (s *Server) askRequests(w http.ResponseWriter, r *http.Request, u *store.User, body map[string]any, note string) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	c, why := s.rqFor(ctx, u.Email)
	if c == nil {
		writeErr(w, http.StatusConflict, why)
		return
	}
	if note = strings.TrimSpace(note); note != "" {
		if len(note) > 1000 {
			note = note[:1000]
		}
		body["note"] = note
	}
	var rq rqAsk
	err := c.do(ctx, http.MethodPost, "/api/v1/requests", nil, body, &rq)
	forgetGetIt(u.ID)
	forgetAuthorMore(u.ID)
	if err != nil {
		var re *rqError
		if errors.As(err, &re) {
			status := re.status
			if status < 400 || status > 599 {
				status = http.StatusBadGateway
			}
			out := map[string]any{"error": re.msg}
			if re.requestID > 0 {
				out["request_id"] = re.requestID
				out["url"] = c.requestURL(re.requestID)
			}
			if re.openURL != "" {
				out["open_url"] = re.openURL
			}
			writeJSON(w, status, out)
			return
		}
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	rq.URL = c.requestURL(rq.ID)
	writeJSON(w, http.StatusCreated, rq)
}

// ---- more by the author ----

// moreBook is one of an author's books as the author page shows it.
type moreBook struct {
	BookKey string  `json:"book_key"`
	Title   string  `json:"title"`
	Year    int     `json:"year"`
	Cover   string  `json:"cover"`
	URL     string  `json:"url"`      // its page in Requests
	HereURL string  `json:"here_url"` // the ebook in this library, when it is here
	Ebook   *rqHave `json:"ebook"`    // have (Requests' view, any library)
	Audio   *rqHave `json:"audiobook"`
	Asked   []rqAsk `json:"asked"`
	InHere  bool    `json:"in_library"` // this library has a book by that title
	LocalID int64   `json:"local_id,omitempty"`
}

type authorMore struct {
	Available bool       `json:"available"`
	Reason    string     `json:"reason,omitempty"`
	AppURL    string     `json:"app_url,omitempty"`
	CanGet    *rqCanGet  `json:"can_get,omitempty"`
	Items     []moreBook `json:"items"`
}

var (
	moreMu    sync.Mutex
	moreCache = map[string]getAuthorCached{}
)

type getAuthorCached struct {
	v  authorMore
	at time.Time
}

func forgetAuthorMore(uid int64) {
	moreMu.Lock()
	defer moreMu.Unlock()
	pre := strconv.FormatInt(uid, 10) + ":"
	for k := range moreCache {
		if strings.HasPrefix(k, pre) {
			delete(moreCache, k)
		}
	}
}

// GET /api/v1/authors/{id}/requests-more — the author's other books, from Requests.
func (s *Server) handleAuthorMore(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	libs := s.allowedLibraries(ctx, u)
	a, err := s.store.GetAuthor(ctx, idParam(r, "id"), libs)
	if err != nil || a.Books == 0 {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	key := strconv.FormatInt(u.ID, 10) + ":" + strconv.FormatInt(a.ID, 10)
	if r.URL.Query().Get("fresh") != "1" {
		moreMu.Lock()
		c, ok := moreCache[key]
		moreMu.Unlock()
		if ok && time.Since(c.at) < getItTTL {
			writeJSON(w, http.StatusOK, c.v)
			return
		}
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out := authorMore{Items: []moreBook{}}
	c, why := s.rqFor(cctx, u.Email)
	if c == nil {
		out.Reason = why
		writeJSON(w, http.StatusOK, out)
		return
	}
	out.Available, out.AppURL = true, c.public
	var res struct {
		Items  []rqTitle `json:"items"`
		CanGet *rqCanGet `json:"can_get"`
	}
	if err := c.do(cctx, http.MethodGet, "/api/v1/books/author", url.Values{"name": {a.Name}}, nil, &res); err != nil {
		var re *rqError
		if errors.As(err, &re) && re.status == http.StatusNotFound {
			out.Available, out.Reason = false, "This Pocket Requests can't list an author's books yet; update it."
		} else {
			out.Available, out.Reason = false, err.Error()
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	out.CanGet = res.CanGet
	// what this library has by the author, by folded title
	local := map[string]int64{}
	if books, _, err := s.store.ListBooks(ctx, store.BookQuery{UserID: u.ID, Libraries: libs, Author: a.ID, Limit: 500}); err == nil {
		for _, b := range books {
			local[foldTitle(b.Title)] = b.ID
		}
	}
	for _, t := range res.Items {
		m := moreBook{BookKey: t.BookKey, Title: t.Title, Year: t.Year, Cover: t.Poster, URL: c.bookURL(t.BookKey),
			Ebook: t.Book.Have.Ebook, Audio: t.Book.Have.Audiobook, Asked: []rqAsk{}}
		for _, x := range t.Book.Asked {
			if live(&x) {
				x.URL = c.requestURL(x.ID)
				m.Asked = append(m.Asked, x)
			}
		}
		if id, ok := local[foldTitle(t.Title)]; ok {
			m.InHere, m.LocalID = true, id
		}
		out.Items = append(out.Items, m)
	}
	moreMu.Lock()
	moreCache[key] = getAuthorCached{out, time.Now()}
	moreMu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

// POST /api/v1/authors/{id}/requests {"book_key", "title", "kind", "language"}
// — ask for one of the author's books.
func (s *Server) handleAuthorRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	a, err := s.store.GetAuthor(ctx, idParam(r, "id"), s.allowedLibraries(ctx, u))
	if err != nil || a.Books == 0 {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var in struct {
		BookKey  string `json:"book_key"`
		Title    string `json:"title"`
		Kind     string `json:"kind"`
		Language string `json:"language"`
		Note     string `json:"note"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Kind != "ebook" && in.Kind != "audiobook" {
		writeErr(w, http.StatusBadRequest, "kind is ebook or audiobook")
		return
	}
	if strings.TrimSpace(in.BookKey) == "" && strings.TrimSpace(in.Title) == "" {
		writeErr(w, http.StatusBadRequest, "say which book (book_key or title)")
		return
	}
	body := map[string]any{"kind": in.Kind, "author": a.Name}
	if k := strings.TrimSpace(in.BookKey); k != "" {
		body["book_key"] = k
	}
	if t := strings.TrimSpace(in.Title); t != "" {
		body["title"] = t
	}
	if l := langCode(in.Language); l != "" {
		body["language"] = l
	}
	s.askRequests(w, r, u, body, in.Note)
}
