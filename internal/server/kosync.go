package server

// E-readers at the root: OPDS (opds.go), the KOReader plugin API
// (koreader.go), CrossPoint's clippings (clippings.go) and, here, KOReader
// sync (KOSync), the protocol sync.koreader.rocks speaks: KOReader and
// CrossPoint only need https://<this server>/kosync and a device's app
// password (Devices in the web app).
//
// A device knows a book by a hash: KOReader by the partial MD5 of the file,
// CrossPoint by default by the MD5 of its file name. Both are kept for every
// file, so a position lands on the exact file and book. The exact string a
// device sent is kept per document, and the book's place moves with it; when
// the book was read further somewhere else (the web reader, another device or
// another format), a pull answers that place, written for this document.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
	"github.com/abjelosevic88/pocket-homelab-library/internal/xpointer"
)

var (
	kosyncDocumentRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	kosyncKeyRE      = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)
)

const (
	syncFailWindow = 10 * time.Minute
	syncMaxFails   = 20
)

// kosyncState counts failed e-reader sign-ins per address.
type kosyncState struct {
	mu    sync.Mutex
	fails map[string][]time.Time
	mapMu sync.Mutex // one document mapped at a time (progress from two devices at once)
}

func newKosyncState() *kosyncState { return &kosyncState{fails: map[string][]time.Time{}} }

func (k *kosyncState) recent(ip string, now time.Time) []time.Time {
	var keep []time.Time
	for _, t := range k.fails[ip] {
		if now.Sub(t) < syncFailWindow {
			keep = append(keep, t)
		}
	}
	return keep
}

func (k *kosyncState) limited(ip string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.recent(ip, time.Now())) >= syncMaxFails
}

func (k *kosyncState) fail(ip string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	now := time.Now()
	if len(k.fails) > 10000 {
		k.fails = map[string][]time.Time{}
	}
	k.fails[ip] = append(k.recent(ip, now), now)
}

func (s *Server) startKosync(ctx context.Context) {}

// routesSync registers everything e-readers talk to.
func (s *Server) routesSync(r chi.Router) {
	r.Route("/kosync", func(r chi.Router) {
		r.Use(s.syncEnabled("kosync"))
		r.Use(middleware.Compress(5))
		ok := func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{"state": "OK"})
		}
		r.Get("/healthcheck", ok)
		r.Get("/healthz", ok)
		r.Post("/users/create", s.handleKosyncCreate)
		r.Group(func(r chi.Router) {
			r.Use(s.deviceAuth(authKosync))
			r.Get("/users/auth", func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, map[string]string{"authorized": "OK"})
			})
			r.Put("/syncs/progress", s.handleKosyncPut)
			r.Get("/syncs/progress/{document}", s.handleKosyncGet)
			// crosspoint-sync's extended API, as far as CrossPoint uses it
			r.Put("/api/v1/progress", s.handleKosyncPut)
			r.Get("/api/v1/clippings/{document}", s.handleClippingsGet)
			r.Put("/api/v1/clippings/{document}", s.handleClippingsPut)
		})
	})
	r.Route("/opds", s.routesOPDS)
	r.Route("/koreader", s.routesKOReader)
}

// syncEnabled answers 404 while the box has that door closed (Settings → E-readers).
func (s *Server) syncEnabled(which string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			st, err := s.store.GetSettings(r.Context(), 0)
			if err == nil && ((which == "kosync" && !st.KosyncEnabled) || (which == "opds" && !st.OPDSEnabled)) {
				http.NotFound(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ---- signing in e-readers ----

type authStyle int

const (
	authKosync   authStyle = iota // {"code":2001} like the reference server
	authOPDS                      // 401 with a Basic challenge
	authKOReader                  // {"error": …}
)

type syncCtxKey int

const syncLoginKey syncCtxKey = 1

// syncLogin is who signed in from an e-reader, and with what.
type syncLogin struct {
	token  *store.APIToken // nil for a session or a Core token
	device *store.Device   // the device the app password belongs to, if any
}

func loginFrom(ctx context.Context) *syncLogin {
	l, _ := ctx.Value(syncLoginKey).(*syncLogin)
	if l == nil {
		return &syncLogin{}
	}
	return l
}

// deviceID is the id of the device that signed in (0 = not a device's app password).
func (l *syncLogin) deviceID() int64 {
	if l.device == nil {
		return 0
	}
	return l.device.ID
}

func (l *syncLogin) deviceKind() string {
	if l.device == nil {
		return ""
	}
	return l.device.Kind
}

// clientIP is the address failures are counted against: Cloudflare's view of
// the client when the request came through a tunnel, else the remote address.
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("Cf-Connecting-Ip"); ip != "" && net.ParseIP(ip) != nil {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// syncUser resolves an e-reader's sign-in: KOReader's x-auth-user/x-auth-key
// (the MD5 of the app password), HTTP Basic (the app password, or a personal
// token, with the account's email or the part before the @), or a bearer
// token / web session for the plugin API and browsers.
func (s *Server) syncUser(r *http.Request, style authStyle) (*store.User, *syncLogin, bool, error) {
	ctx := r.Context()
	if user, key := r.Header.Get("X-Auth-User"), strings.ToLower(strings.TrimSpace(r.Header.Get("X-Auth-Key"))); user != "" || key != "" {
		if !kosyncKeyRE.MatchString(key) || user == "" {
			return nil, nil, true, errors.New("bad credentials")
		}
		u, t, err := s.store.GetUserByLogin(ctx, user, key, true)
		if err == nil {
			return u, &syncLogin{token: t}, true, nil
		}
		// CrossPoint sends Basic as well: let it decide
		if _, _, ok := r.BasicAuth(); !ok {
			return nil, nil, true, err
		}
	}
	if user, pass, ok := r.BasicAuth(); ok {
		u, t, err := s.store.GetUserByLogin(ctx, user, pass, false)
		if err != nil && style == authKosync && kosyncKeyRE.MatchString(pass) {
			// a client that put the MD5 into Basic
			u, t, err = s.store.GetUserByLogin(ctx, user, pass, true)
		}
		if err != nil {
			return nil, nil, true, err
		}
		return u, &syncLogin{token: t}, true, nil
	}
	if style == authKosync {
		return nil, nil, false, http.ErrNoCookie
	}
	u, _, err := s.resolveAuth(r)
	if err != nil || u == nil {
		return nil, nil, false, http.ErrNoCookie
	}
	l := &syncLogin{}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(h), "bearer ") {
		if t, err := s.tokenByPlain(ctx, strings.TrimSpace(h[7:])); err == nil {
			l.token = t
		}
	}
	return u, l, true, nil
}

// tokenByPlain finds the token row of a plaintext personal token or app password.
func (s *Server) tokenByPlain(ctx context.Context, plain string) (*store.APIToken, error) {
	var t store.APIToken
	err := s.store.DB.QueryRowContext(ctx, `SELECT id, user_id, name, kind FROM api_tokens WHERE token_hash = ?`, store.HashToken(plain)).Scan(&t.ID, &t.UserID, &t.Name, &t.Kind)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// deviceAuth signs e-readers in and remembers which device it was.
func (s *Server) deviceAuth(style authStyle) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			if s.sync.limited(ip) {
				switch style {
				case authKosync:
					kosyncErr(w, http.StatusTooManyRequests, 2001, "Too many failed logins, try again later.")
				default:
					writeErr(w, http.StatusTooManyRequests, "too many failed sign-ins, try again later")
				}
				return
			}
			u, login, tried, err := s.syncUser(r, style)
			if err != nil || u == nil {
				if tried {
					s.sync.fail(ip)
				}
				switch style {
				case authKosync:
					kosyncErr(w, http.StatusUnauthorized, 2001, "Unauthorized")
				case authOPDS:
					w.Header().Set("WWW-Authenticate", `Basic realm="Pocket Library", charset="UTF-8"`)
					http.Error(w, "Sign in with your email and the app password from Pocket Library → Devices.", http.StatusUnauthorized)
				default:
					writeErr(w, http.StatusUnauthorized, "unauthorized")
				}
				return
			}
			ctx := r.Context()
			if login.token != nil {
				if d, err := s.store.SyncDeviceByToken(ctx, u.ID, login.token.ID); err == nil {
					login.device = d
					_ = s.store.SyncTouchDevice(ctx, d.ID)
				}
			}
			ctx = context.WithValue(ctx, userKey, u)
			ctx = context.WithValue(ctx, syncLoginKey, login)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// basicHeader is the Authorization value of a Basic login (tests, docs).
func basicHeader(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

// ---- KOSync ----

// KOSync answers errors as {"code": n, "message": "..."} with the codes of the
// reference server, which clients show or act on.
func kosyncErr(w http.ResponseWriter, status, code int, msg string) {
	writeJSON(w, status, map[string]any{"code": code, "message": msg})
}

func (s *Server) handleKosyncCreate(w http.ResponseWriter, r *http.Request) {
	kosyncErr(w, http.StatusPaymentRequired, 2005, "Registration is closed: add this device in Pocket Library → Devices and use its app password.")
}

type kosyncPut struct {
	Document   string   `json:"document"`
	Progress   *string  `json:"progress"`
	Percentage *float64 `json:"percentage"`
	Device     string   `json:"device"`
	DeviceID   string   `json:"device_id"`
	Metadata   *docMeta `json:"metadata"`
	Position   *struct {
		Spine *int    `json:"spine"`
		XPath string  `json:"xpath"`
		PctQ  float64 `json:"pctQ"`
	} `json:"position"`
}

// docMeta is what a device may say about a document besides its hash.
type docMeta struct {
	Title    string `json:"title"`
	Authors  string `json:"authors"`
	Filename string `json:"filename"`
}

func (s *Server) handleKosyncPut(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	var in kosyncPut
	if err != nil || json.Unmarshal(raw, &in) != nil {
		kosyncErr(w, http.StatusForbidden, 2003, "Invalid request")
		return
	}
	if in.Document == "" {
		kosyncErr(w, http.StatusForbidden, 2004, "Field 'document' not provided.")
		return
	}
	if !kosyncDocumentRE.MatchString(in.Document) {
		kosyncErr(w, http.StatusForbidden, 2003, "Invalid request")
		return
	}
	if in.Percentage == nil || math.IsNaN(*in.Percentage) || in.Progress == nil || in.Device == "" {
		kosyncErr(w, http.StatusForbidden, 2003, "Invalid request")
		return
	}
	ctx := r.Context()
	u := userFrom(ctx)
	now := time.Now()
	p := &store.KosyncProgress{Document: strings.ToLower(in.Document), Progress: trim(*in.Progress, 1024),
		Percentage: math.Max(0, math.Min(1, *in.Percentage)), Device: trim(in.Device, 100), DeviceID: trim(in.DeviceID, 100), Timestamp: now.Unix()}
	if p.Progress == "" && in.Position != nil && in.Position.XPath != "" {
		p.Progress = trim(in.Position.XPath, 1024)
	}
	s.sync.mapMu.Lock()
	defer s.sync.mapMu.Unlock()
	prev, _ := s.store.GetKosync(ctx, u.ID, p.Document)
	b, f := s.mapDocument(ctx, u, p.Document, in.Metadata, prev)
	if b != nil {
		p.BookID, p.FileID = b.ID, f.ID
	}
	if err := s.store.SaveKosync(ctx, u.ID, p); err != nil {
		s.log.Error("kosync save", "err", err)
		kosyncErr(w, http.StatusInternalServerError, 2000, "Unknown server error")
		return
	}
	if b != nil {
		prevPct := -1.0
		if prev != nil {
			prevPct = prev.Percentage
		}
		s.kosyncToBook(ctx, u, b, f, p, prevPct, now)
	}
	writeJSON(w, http.StatusOK, map[string]any{"document": p.Document, "timestamp": p.Timestamp})
}

// kosyncLocator is where a device says it is, in the book's terms.
type kosyncLocator struct {
	Type     string   `json:"type"` // kosync
	Document string   `json:"document"`
	Device   string   `json:"device"`
	XPointer string   `json:"xpointer,omitempty"`
	Page     int      `json:"page,omitempty"`
	Spine    *int     `json:"spine,omitempty"`
	Fraction *float64 `json:"fraction,omitempty"`
}

// locatorOf describes a device's position string: an XPointer into an EPUB
// (resolved to spine and fraction), or a page number.
func locatorOf(f *store.File, p *store.KosyncProgress) kosyncLocator {
	l := kosyncLocator{Type: "kosync", Document: p.Document, Device: p.Device}
	switch {
	case strings.HasPrefix(p.Progress, "/"):
		l.XPointer = p.Progress
		if isEPUB(f) {
			if spine, frac, err := xpointer.Resolve(f.Path, p.Progress); err == nil {
				l.Spine, l.Fraction = &spine, &frac
			}
		}
	default:
		if n, err := strconv.Atoi(strings.TrimSpace(p.Progress)); err == nil && n > 0 {
			l.Page = n
		}
	}
	return l
}

func isEPUB(f *store.File) bool { return f != nil && (f.Format == "epub" || f.Format == "kepub") }

// kosyncToBook moves the book's place to a device's position and counts the reading.
func (s *Server) kosyncToBook(ctx context.Context, u *store.User, b *store.Book, f *store.File, p *store.KosyncProgress, prevPct float64, at time.Time) {
	loc, _ := json.Marshal(locatorOf(f, p))
	ub, ch, stale, err := s.store.SetProgress(ctx, u.ID, b.ID, store.Progress{FileID: f.ID, Percentage: p.Percentage, Locator: string(loc), Device: p.Device, At: at, AutoFinish: true})
	if err != nil {
		s.log.Warn("kosync progress", "user", u.ID, "book", b.ID, "err", err)
		return
	}
	if stale {
		return
	}
	pages := b.Pages
	if pages == 0 {
		pages = f.Pages
	}
	if _, err := s.store.RecordSession(ctx, u.ID, store.SessionReport{BookID: b.ID, FileID: f.ID, Device: p.Device, Source: "kosync", At: at,
		Pct: p.Percentage, PrevPct: prevPct, Estimate: true, PagesInBook: pages}); err != nil {
		s.log.Warn("kosync session", "err", err)
	}
	s.publishStatus(ctx, u, b, ch, p.Device)
	_ = ub
}

// publishStatus tells the Core bus a book was started or finished.
func (s *Server) publishStatus(ctx context.Context, u *store.User, b *store.Book, ch store.StatusChange, device string) {
	payload := func() map[string]any {
		authors := make([]string, 0, len(b.Authors))
		for _, a := range b.Authors {
			authors = append(authors, a.Name)
		}
		return map[string]any{"book_id": b.ID, "title": b.Title, "authors": authors, "device": device}
	}
	if ch.Started {
		s.publish(ctx, u, "library.started", b.Title, payload())
	}
	if ch.Finished {
		s.publish(ctx, u, "library.finished", b.Title, payload())
	}
}

func (s *Server) handleKosyncGet(w http.ResponseWriter, r *http.Request) {
	doc := strings.ToLower(chi.URLParam(r, "document"))
	if !kosyncDocumentRE.MatchString(doc) {
		kosyncErr(w, http.StatusForbidden, 2004, "Field 'document' not provided.")
		return
	}
	ctx := r.Context()
	u := userFrom(ctx)
	p, err := s.store.GetKosync(ctx, u.ID, doc)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		kosyncErr(w, http.StatusInternalServerError, 2000, "Unknown server error")
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		p = nil
	}
	if q := s.newerPlace(ctx, u, doc, p); q != nil {
		p = q
	}
	if p == nil {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"document": doc, "progress": p.Progress, "percentage": p.Percentage,
		"device": p.Device, "device_id": p.DeviceID, "timestamp": p.Timestamp,
	})
}

// newerPlace is the book's place written for this document, when the book was
// read further somewhere else than this document's last sync (or it was never
// synced here yet). nil = answer what the device itself sent.
func (s *Server) newerPlace(ctx context.Context, u *store.User, doc string, p *store.KosyncProgress) *store.KosyncProgress {
	b, f := s.mapDocument(ctx, u, doc, nil, p)
	if b == nil {
		return nil
	}
	ub, err := s.store.GetUserBook(ctx, u.ID, b.ID)
	if err != nil || ub.LastReadAt == "" || ub.Percentage <= 0 || ub.Status == "read" {
		return nil
	}
	last, err := time.Parse(time.RFC3339Nano, ub.LastReadAt)
	if err != nil {
		return nil
	}
	var loc struct {
		Type     string   `json:"type"`
		Document string   `json:"document"`
		Spine    *int     `json:"spine"`
		Fraction *float64 `json:"fraction"`
		Page     int      `json:"page"`
	}
	_ = json.Unmarshal([]byte(ub.Locator), &loc)
	if p != nil {
		if loc.Type == "kosync" && loc.Document == doc {
			return nil // this document's own sync is the newest
		}
		if !last.After(time.Unix(p.Timestamp, 0).Add(2 * time.Second)) {
			return nil
		}
	}
	out := &store.KosyncProgress{Document: doc, BookID: b.ID, FileID: f.ID, Percentage: ub.Percentage,
		Device: "Pocket Library", DeviceID: "pocket-library", Timestamp: last.Unix()}
	switch {
	case isEPUB(f):
		spine, frac := -1, 0.0
		// the place's spine and fraction hold for this file only if it is the same file
		if loc.Spine != nil && loc.Fraction != nil && ub.FileID == f.ID {
			spine, frac = *loc.Spine, *loc.Fraction
		}
		if spine < 0 {
			sp, fr, err := xpointer.Locate(f.Path, ub.Percentage)
			if err != nil {
				return nil
			}
			spine, frac = sp, fr
		}
		xp, err := xpointer.Build(f.Path, spine, frac)
		if err != nil {
			return nil
		}
		out.Progress = xp
		if pct, err := xpointer.Overall(f.Path, spine, frac); err == nil {
			out.Percentage = math.Floor(pct*10000) / 10000
		}
	default:
		pages := f.Pages
		if pages == 0 {
			pages = b.Pages
		}
		if pages <= 0 {
			return nil
		}
		out.Progress = strconv.Itoa(max(1, int(math.Round(ub.Percentage*float64(pages)))))
	}
	return out
}

// mapDocument finds the book (and its file) a document hash is, among the
// books the person may open: by the file hashes, the book an earlier sync was
// mapped to, or the title and authors the device sent (one clear match only).
func (s *Server) mapDocument(ctx context.Context, u *store.User, doc string, meta *docMeta, prev *store.KosyncProgress) (*store.Book, *store.File) {
	libs := s.allowedLibraries(ctx, u)
	allowed := map[int64]bool{}
	for _, id := range libs {
		allowed[id] = true
	}
	if files, err := s.store.FilesByHash(ctx, doc); err == nil {
		for _, f := range files {
			if !allowed[f.LibraryID] {
				continue
			}
			if b, err := s.store.GetBook(ctx, u.ID, f.BookID, libs); err == nil {
				ff := f
				return b, &ff
			}
		}
	}
	if prev != nil && prev.BookID > 0 {
		if b, err := s.store.GetBook(ctx, u.ID, prev.BookID, libs); err == nil {
			if f := fileOf(b, prev.FileID); f != nil {
				return b, f
			}
		}
	}
	if meta == nil {
		return nil, nil
	}
	title := strings.TrimSpace(meta.Title)
	if title == "" && meta.Filename != "" {
		name := meta.Filename
		if i := strings.LastIndexAny(name, "/\\"); i >= 0 {
			name = name[i+1:]
		}
		if i := strings.LastIndexByte(name, '.'); i > 0 {
			name = name[:i]
		}
		// "Author - Title" (CrossPoint's download naming) or "Title"
		if _, t, ok := strings.Cut(name, " - "); ok {
			title = t
		} else {
			title = name
		}
	}
	if title == "" {
		return nil, nil
	}
	books, _, err := s.store.ListBooks(ctx, store.BookQuery{Q: title, UserID: u.ID, Libraries: libs, ShowHidden: true, Status: "any", Limit: 20})
	if err != nil {
		return nil, nil
	}
	want := fold(title)
	var authors []string
	for _, a := range strings.FieldsFunc(meta.Authors, func(r rune) bool { return r == ',' || r == ';' || r == '\n' || r == '&' }) {
		if a = fold(a); a != "" {
			authors = append(authors, a)
		}
	}
	var hits []store.Book
	for _, b := range books {
		if fold(b.Title) != want && fold(b.Title+" "+b.Subtitle) != want && fold(b.Title+": "+b.Subtitle) != want {
			continue
		}
		if len(authors) > 0 && !authorsMatch(b, authors) {
			continue
		}
		hits = append(hits, b)
	}
	if len(hits) != 1 {
		return nil, nil
	}
	b, err := s.store.GetBook(ctx, u.ID, hits[0].ID, libs)
	if err != nil || len(b.Files) == 0 {
		return nil, nil
	}
	f := fileOf(b, 0)
	if f == nil {
		return nil, nil
	}
	return b, f
}

// fileOf returns the book's file with that id, else its best live file.
func fileOf(b *store.Book, id int64) *store.File {
	for i := range b.Files {
		if b.Files[i].ID == id && id > 0 {
			return &b.Files[i]
		}
	}
	for i := range b.Files {
		if !b.Files[i].Missing {
			return &b.Files[i]
		}
	}
	return nil
}

func authorsMatch(b store.Book, want []string) bool {
	for _, a := range b.Authors {
		fa := fold(a.Name)
		for _, w := range want {
			if fa == w || strings.Contains(fa, w) || strings.Contains(w, fa) {
				return true
			}
		}
	}
	return false
}

// fold makes names comparable: lower case, Latin diacritics and punctuation gone.
func fold(v string) string {
	var sb strings.Builder
	space := false
	for _, r := range strings.ToLower(v) {
		if m, ok := foldMap[r]; ok {
			sb.WriteString(m)
			space = false
			continue
		}
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			sb.WriteRune(r)
			space = false
		case !space && sb.Len() > 0:
			sb.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(sb.String())
}

var foldMap = map[rune]string{
	'č': "c", 'ć': "c", 'ç': "c", 'š': "s", 'ś': "s", 'ž': "z", 'ź': "z", 'ż': "z", 'đ': "dj", 'ð': "d",
	'á': "a", 'à': "a", 'â': "a", 'ä': "a", 'ã': "a", 'å': "a", 'ą': "a", 'é': "e", 'è': "e", 'ê': "e", 'ë': "e", 'ę': "e", 'ě': "e",
	'í': "i", 'ì': "i", 'î': "i", 'ï': "i", 'ó': "o", 'ò': "o", 'ô': "o", 'ö': "o", 'õ': "o", 'ø': "o", 'ő': "o",
	'ú': "u", 'ù': "u", 'û': "u", 'ü': "u", 'ů': "u", 'ű': "u", 'ý': "y", 'ÿ': "y", 'ñ': "n", 'ń': "n", 'ň': "n",
	'ł': "l", 'ř': "r", 'ť': "t", 'ď': "d", 'ß': "ss", 'æ': "ae", 'œ': "oe", '’': "", '\'': "",
}
