package server

// Settings → Import: bring over what Calibre-Web, Kavita and a Kindle knew.
// Calibre-Web and Kavita are read from copies of their databases (their
// paths come from CALIBRE_WEB_DB, CALIBRE_DB and KAVITA_DB); people are
// matched by e-mail address, books by KOReader hash, path, Calibre id,
// ISBN or title. ?dry=1 reports what would happen without changing anything.

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-library/internal/importers"
	pmail "github.com/abjelosevic88/pocket-homelab-library/internal/mail"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

func (s *Server) routesImports(api chi.Router) {
	api.Get("/import/sources", s.handleImportSources)
	api.Post("/import/calibre-web", s.handleImportCalibreWeb)
	api.Post("/import/kavita", s.handleImportKavita)
}

// importResult is what an import did (or, dry, would do).
type importResult struct {
	Dry       bool           `json:"dry"`
	Counts    map[string]int `json:"counts"`
	People    []string       `json:"people"`              // matched accounts
	Unmatched []string       `json:"unmatched,omitempty"` // books or people not found here
	Notes     []string       `json:"notes,omitempty"`
}

func newResult(dry bool) *importResult {
	return &importResult{Dry: dry, Counts: map[string]int{}, People: []string{}}
}

func (res *importResult) miss(v string) {
	if len(res.Unmatched) < 200 {
		res.Unmatched = append(res.Unmatched, v)
	}
	res.Counts["unmatched"]++
}

type sourceInfo struct {
	Configured bool   `json:"configured"`
	Path       string `json:"path"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
}

func probe(p string) sourceInfo {
	if p == "" {
		return sourceInfo{}
	}
	si := sourceInfo{Configured: true, Path: p}
	if _, err := os.Stat(p); err != nil {
		si.Error = "not found (mount it read-only into the container)"
		return si
	}
	si.OK = true
	return si
}

func (s *Server) handleImportSources(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]sourceInfo{
		"calibre_web": probe(s.cfg.CalibreWebDB), "calibre": probe(s.cfg.CalibreDB), "kavita": probe(s.cfg.KavitaDB),
	})
}

// ---- matching ----

// importer carries what one import run needs.
type importer struct {
	s       *Server
	ctx     context.Context
	dry     bool
	res     *importResult
	libs    map[int64][]int64 // person → libraries they may open
	calibre map[int64]importers.CalibreBook
	paths   []pathMap
}

type pathMap struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (im *importer) librariesOf(uid int64) []int64 {
	if l, ok := im.libs[uid]; ok {
		return l
	}
	u, err := im.s.store.GetUser(im.ctx, uid)
	if err != nil {
		return nil
	}
	l := im.s.allowedLibraries(im.ctx, u)
	im.libs[uid] = l
	return l
}

// open reports whether the book is in a library the person may open.
func (im *importer) open(uid, bookID int64) bool {
	b, err := im.s.store.GetBookAny(im.ctx, bookID)
	if err != nil {
		return false
	}
	for _, l := range im.librariesOf(uid) {
		if l == b.LibraryID {
			return true
		}
	}
	return false
}

var calibreFolderID = regexp.MustCompile(`\((\d+)\)$`)

// matchFiles finds the book (and file) one of another app's files is.
func (im *importer) matchFiles(files []importers.KFile) (bookID, fileID int64) {
	st := im.s.store
	for _, f := range files {
		if f.Hash != "" {
			if fs, err := st.FilesByHash(im.ctx, f.Hash); err == nil && len(fs) > 0 {
				return fs[0].BookID, fs[0].ID
			}
		}
		for _, m := range im.paths {
			if m.From != "" && strings.HasPrefix(f.Path, m.From) {
				if lf, err := st.FileByPath(im.ctx, path.Clean(m.To+"/"+strings.TrimPrefix(f.Path, m.From))); err == nil {
					return lf.BookID, lf.ID
				}
			}
		}
		parts := strings.Split(strings.Trim(f.Path, "/"), "/")
		if len(parts) >= 2 {
			suffix := "/" + strings.Join(parts[len(parts)-2:], "/")
			if fs, err := st.FilesBySuffix(im.ctx, suffix, 5); err == nil {
				if len(fs) == 1 {
					return fs[0].BookID, fs[0].ID
				}
				for _, lf := range fs {
					if f.Bytes > 0 && lf.Size == f.Bytes {
						return lf.BookID, lf.ID
					}
				}
			}
		}
		// Calibre's layout: …/Author/Title (123)/file
		if len(parts) >= 2 {
			if m := calibreFolderID.FindStringSubmatch(parts[len(parts)-2]); m != nil {
				id, _ := strconv.ParseInt(m[1], 10, 64)
				if b := im.calibreBook(id); b > 0 {
					return b, 0
				}
			}
		}
	}
	return 0, 0
}

// calibreBook finds a Calibre library's book here: by its Calibre id (the
// sidecar OPFs carry it), else its UUID or ISBN from metadata.db.
func (im *importer) calibreBook(id int64) int64 {
	st := im.s.store
	if ids, _ := st.BooksByIdentifier(im.ctx, "calibre", strconv.FormatInt(id, 10)); len(ids) > 0 {
		return ids[0]
	}
	if cb, ok := im.calibre[id]; ok {
		if ids, _ := st.BooksByIdentifier(im.ctx, "uuid", cb.UUID); len(ids) > 0 {
			return ids[0]
		}
		if ids, _ := st.BooksByIdentifier(im.ctx, "isbn", cb.ISBN); len(ids) > 0 {
			return ids[0]
		}
	}
	return 0
}

func parseImportTime(v string) time.Time {
	v = strings.TrimSpace(v)
	for _, l := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05", "2006-01-02T15:04:05.999999999", "2006-01-02"} {
		if t, err := time.ParseInLocation(l, v, time.UTC); err == nil {
			return t
		}
	}
	return time.Time{}
}

func dayOf(v string) string {
	if t := parseImportTime(v); !t.IsZero() {
		return t.Local().Format("2006-01-02")
	}
	return ""
}

// setStatus moves a book to read/reading/want unless the person moved it already.
func (im *importer) setStatus(uid, bookID int64, status, day string) bool {
	ub, err := im.s.store.GetUserBook(im.ctx, uid, bookID)
	if err != nil {
		return false
	}
	switch {
	case ub.Status == status, ub.Status == "read" && status != "read":
		return false
	case status == "want" && (ub.Status != "" || ub.Percentage > 0):
		return false
	case status == "reading" && ub.Status == "reading":
		return false
	}
	if im.dry {
		return true
	}
	_, _, err = im.s.store.SetStatus(im.ctx, uid, bookID, status, day)
	return err == nil
}

// shelf makes (or finds) a person's shelf and puts books on it in order.
func (im *importer) shelf(uid int64, name, desc string, ordered bool, books []int64) {
	if len(books) == 0 {
		return
	}
	im.res.Counts["shelves"]++
	im.res.Counts["shelf books"] += len(books)
	if im.dry {
		return
	}
	shelves, _ := im.s.store.ListShelves(im.ctx, uid)
	var id int64
	for _, sh := range shelves {
		if strings.EqualFold(sh.Name, name) && sh.Kind == "manual" {
			id = sh.ID
		}
	}
	if id == 0 {
		sh := &store.Shelf{Name: name, Description: desc, Kind: "manual", Ordered: ordered, OPDS: true}
		if err := im.s.store.CreateShelf(im.ctx, uid, sh); err != nil {
			return
		}
		id = sh.ID
	}
	_ = im.s.store.AddToShelf(im.ctx, uid, id, books)
}

func importBody(r *http.Request, v any) error {
	if r.ContentLength == 0 {
		return nil
	}
	return decode(r, v)
}

// ---- Calibre-Web ----

func (s *Server) handleImportCalibreWeb(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	in := struct {
		Mail       *bool            `json:"mail"`
		Kindle     *bool            `json:"kindle"`
		Shelves    *bool            `json:"shelves"`
		ReadStatus *bool            `json:"read_status"`
		Force      bool             `json:"force"` // replace the box's mail account
		UsersMap   map[string]int64 `json:"users_map"`
	}{}
	if err := importBody(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	on := func(b *bool) bool { return b == nil || *b }
	if s.cfg.CalibreWebDB == "" {
		writeErr(w, http.StatusBadRequest, "CALIBRE_WEB_DB is not set: mount Calibre-Web's config folder read-only and point CALIBRE_WEB_DB at its app.db")
		return
	}
	ctx := r.Context()
	cw, err := importers.ReadCalibreWeb(s.cfg.CalibreWebDB)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "Calibre-Web: "+err.Error())
		return
	}
	im := &importer{s: s, ctx: ctx, dry: queryBool(r, "dry"), res: newResult(queryBool(r, "dry")), libs: map[int64][]int64{}}
	if s.cfg.CalibreDB != "" {
		if cb, err := importers.ReadCalibre(s.cfg.CalibreDB); err == nil {
			im.calibre = cb
		} else {
			im.res.Notes = append(im.res.Notes, "Calibre library not read: "+err.Error())
		}
	}
	// people
	who := map[int64]*store.User{}
	for _, cu := range cw.Users {
		var u *store.User
		if id := in.UsersMap[strconv.FormatInt(cu.ID, 10)]; id > 0 {
			u, _ = s.store.GetUser(ctx, id)
		} else if cu.Email != "" {
			u, _ = s.store.GetUserByEmail(ctx, cu.Email)
		}
		if u == nil {
			im.res.miss(fmt.Sprintf("Calibre-Web user %s (%s): no account here with that e-mail", cu.Name, cu.Email))
			continue
		}
		who[cu.ID] = u
		im.res.People = append(im.res.People, fmt.Sprintf("%s → %s", cu.Name, u.Email))
		if on(in.Kindle) && cu.Kindle != "" {
			if _, err := s.store.DeviceByEmail(ctx, u.ID, cu.Kindle); err == nil {
				continue
			}
			im.res.Counts["kindles"]++
			if !im.dry {
				devs, _ := s.store.ListDevices(ctx, u.ID)
				if err := s.addKindle(ctx, u.ID, cu.Kindle, len(devs) == 0); err != nil {
					im.res.Notes = append(im.res.Notes, "Kindle "+cu.Kindle+": "+err.Error())
				}
			}
		}
	}
	// mail
	if on(in.Mail) && cw.Mail != nil {
		st, _ := s.store.GetSettings(ctx, 0)
		switch {
		case cw.MailError != "":
			im.res.Notes = append(im.res.Notes, "Mail account not taken: "+cw.MailError)
		case st.SMTPHost != "" && !in.Force:
			im.res.Notes = append(im.res.Notes, "Mail is set up here already ("+st.SMTPHost+"); import with force to replace it with Calibre-Web's ("+cw.Mail.Host+").")
		default:
			im.res.Counts["mail account"] = 1
			im.res.Notes = append(im.res.Notes, fmt.Sprintf("Mail: %s:%d (%s) from %s", cw.Mail.Host, cw.Mail.Port, cw.Mail.TLS, cw.Mail.From))
			if !im.dry {
				st.SMTPHost, st.SMTPPort, st.SMTPTLS, st.SMTPUser = cw.Mail.Host, cw.Mail.Port, cw.Mail.TLS, cw.Mail.User
				st.SMTPFrom, st.SMTPFromName = cw.Mail.From, cw.Mail.FromName
				st.SMTPPassword = ""
				if cw.Mail.Password != "" {
					if st.SMTPPassword, err = s.seal(cw.Mail.Password); err != nil {
						s.fail(w, err, "seal")
						return
					}
				}
				if err := s.store.SaveBoxSettings(ctx, st); err != nil {
					s.fail(w, err, "save mail")
					return
				}
			}
		}
	}
	// read status
	if on(in.ReadStatus) {
		for _, rs := range cw.ReadStates {
			u := who[rs.UserID]
			if u == nil {
				continue
			}
			b := im.calibreBook(rs.BookID)
			if b == 0 || !im.open(u.ID, b) {
				im.res.miss(fmt.Sprintf("Calibre book %d (%s)", rs.BookID, im.calibre[rs.BookID].Title))
				continue
			}
			if im.setStatus(u.ID, b, rs.Status, dayOf(rs.Modified)) {
				im.res.Counts[rs.Status]++
			}
		}
	}
	// shelves
	if on(in.Shelves) {
		for _, sh := range cw.Shelves {
			u := who[sh.UserID]
			if u == nil {
				continue
			}
			var books []int64
			for _, id := range sh.Books {
				if b := im.calibreBook(id); b > 0 && im.open(u.ID, b) {
					books = append(books, b)
				} else {
					im.res.miss(fmt.Sprintf("Calibre book %d on shelf %s", id, sh.Name))
				}
			}
			im.shelf(u.ID, sh.Name, "From Calibre-Web", false, books)
		}
	}
	writeJSON(w, http.StatusOK, im.res)
}

// ---- Kavita ----

func (s *Server) handleImportKavita(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	in := struct {
		PathMap  []pathMap        `json:"path_map"`
		UsersMap map[string]int64 `json:"users_map"`
	}{}
	if err := importBody(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if s.cfg.KavitaDB == "" {
		writeErr(w, http.StatusBadRequest, "KAVITA_DB is not set: mount Kavita's config folder read-only and point KAVITA_DB at its kavita.db")
		return
	}
	ctx := r.Context()
	k, err := importers.ReadKavita(s.cfg.KavitaDB)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "Kavita: "+err.Error())
		return
	}
	im := &importer{s: s, ctx: ctx, dry: queryBool(r, "dry"), res: newResult(queryBool(r, "dry")), libs: map[int64][]int64{}, paths: in.PathMap}
	if s.cfg.CalibreDB != "" {
		if cb, err := importers.ReadCalibre(s.cfg.CalibreDB); err == nil {
			im.calibre = cb
		}
	}
	who := map[int64]*store.User{}
	for _, ku := range k.Users {
		var u *store.User
		if id := in.UsersMap[strconv.FormatInt(ku.ID, 10)]; id > 0 {
			u, _ = s.store.GetUser(ctx, id)
		} else if ku.Email != "" {
			u, _ = s.store.GetUserByEmail(ctx, ku.Email)
		}
		if u == nil {
			im.res.miss(fmt.Sprintf("Kavita user %s (%s): no account here with that e-mail", ku.Name, ku.Email))
			continue
		}
		who[ku.ID] = u
		im.res.People = append(im.res.People, fmt.Sprintf("%s → %s", ku.Name, u.Email))
	}
	seriesBooks := func(uid, sid int64) []int64 {
		seen := map[int64]bool{}
		var out []int64
		for _, f := range k.Series[sid].Files {
			if b, _ := im.matchFiles([]importers.KFile{f}); b > 0 && !seen[b] && im.open(uid, b) {
				seen[b] = true
				out = append(out, b)
			}
		}
		return out
	}
	for _, p := range k.Progress {
		u := who[p.UserID]
		if u == nil || p.PagesRead <= 0 || len(p.Files) == 0 {
			continue
		}
		b, fid := im.matchFiles(p.Files)
		if b == 0 || !im.open(u.ID, b) {
			im.res.miss(p.Files[0].Path)
			continue
		}
		at := parseImportTime(p.Modified)
		if p.TotalReads > 0 || (p.Pages > 0 && p.PagesRead >= p.Pages) {
			if im.setStatus(u.ID, b, "read", dayOf(p.Modified)) {
				im.res.Counts["read"]++
			}
			continue
		}
		pct := 0.0
		if p.Pages > 0 {
			pct = float64(p.PagesRead) / float64(p.Pages)
		}
		im.res.Counts["progress"]++
		if im.dry {
			continue
		}
		loc, _ := json.Marshal(map[string]any{"type": "import", "source": "kavita", "page": p.PagesRead, "pages": p.Pages, "xpath": p.ScrollID})
		if _, _, _, err := s.store.SetProgress(ctx, u.ID, b, store.Progress{FileID: fid, Percentage: pct, Locator: string(loc), Device: "Kavita", At: at}); err != nil {
			im.res.Notes = append(im.res.Notes, "progress: "+err.Error())
		}
	}
	for _, wnt := range k.Want {
		if u := who[wnt.UserID]; u != nil {
			books := seriesBooks(u.ID, wnt.SeriesID)
			if len(books) == 0 {
				im.res.miss("series " + k.Series[wnt.SeriesID].Name)
			}
			for _, b := range books {
				if im.setStatus(u.ID, b, "want", "") {
					im.res.Counts["want"]++
				}
			}
		}
	}
	for _, rt := range k.Ratings {
		u := who[rt.UserID]
		if u == nil {
			continue
		}
		stars := int(rt.Rating*2 + 0.5)
		if stars > 10 {
			stars = 10
		}
		for _, b := range seriesBooks(u.ID, rt.SeriesID) {
			ub, _ := s.store.GetUserBook(ctx, u.ID, b)
			if ub == nil || ub.Rating > 0 {
				continue
			}
			im.res.Counts["ratings"]++
			if !im.dry {
				_, _ = s.store.UpdateUserBook(ctx, u.ID, b, &stars, nil, nil)
			}
		}
	}
	for _, c := range k.Collections {
		if u := who[c.UserID]; u != nil {
			var books []int64
			for _, sid := range c.Series {
				books = append(books, seriesBooks(u.ID, sid)...)
			}
			im.shelf(u.ID, c.Title, nz(c.Summary, "From Kavita"), false, books)
		}
	}
	for _, l := range k.ReadingLists {
		if u := who[l.UserID]; u != nil {
			var books []int64
			seen := map[int64]bool{}
			for _, item := range l.Items {
				if b, _ := im.matchFiles(item); b > 0 && !seen[b] && im.open(u.ID, b) {
					seen[b] = true
					books = append(books, b)
				}
			}
			im.shelf(u.ID, l.Title, nz(l.Summary, "Reading list from Kavita"), true, books)
		}
	}
	for _, a := range k.Annotations {
		u := who[a.UserID]
		if u == nil || strings.TrimSpace(a.Text) == "" {
			continue
		}
		b, fid := im.matchFiles(a.Files)
		if b == 0 || !im.open(u.ID, b) {
			continue
		}
		im.res.Counts["highlights"]++
		if im.dry {
			continue
		}
		h := sha1.Sum([]byte(a.XPath + "|" + a.Text))
		loc, _ := json.Marshal(map[string]any{"type": "import", "source": "kavita", "xpath": a.XPath, "page": a.Page})
		created := ""
		if t := parseImportTime(a.Created); !t.IsZero() {
			created = t.Format(time.RFC3339Nano)
		}
		_, _ = s.store.SaveAnnotation(ctx, u.ID, &store.Annotation{BookID: b, FileID: fid, Kind: "highlight", Text: a.Text, Note: a.Comment,
			Chapter: a.Chapter, Page: a.Page, Locator: string(loc), Source: "import", ExternalID: "kavita:" + hex.EncodeToString(h[:]), CreatedAt: created})
	}
	writeJSON(w, http.StatusOK, im.res)
}

// ---- Kindle clippings (anyone, for themselves) ----

// foldTitle makes titles comparable: lower case, plain letters, no punctuation.
func foldTitle(v string) string {
	v = pmail.ASCIIName(strings.ToLower(v))
	var b strings.Builder
	space := false
	for _, r := range v {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

// findBookByTitle finds the one book in the person's libraries with this
// title (and author, when several share it); 0 when none or unclear.
func (s *Server) findBookByTitle(ctx context.Context, u *store.User, title, author string) int64 {
	libs := s.allowedLibraries(ctx, u)
	want := foldTitle(title)
	short := want
	if i := strings.IndexAny(title, ":("); i > 0 {
		short = foldTitle(title[:i])
	}
	if want == "" {
		return 0
	}
	books, _, err := s.store.ListBooks(ctx, store.BookQuery{Q: short, UserID: u.ID, Libraries: libs, Limit: 50, ShowHidden: true})
	if err != nil {
		return 0
	}
	var hits []store.Book
	for _, b := range books {
		t := foldTitle(b.Title)
		if t == want || t == short || foldTitle(b.Title+" "+b.Subtitle) == want {
			hits = append(hits, b)
		}
	}
	if len(hits) > 1 && author != "" {
		var by []store.Book
		fa := foldTitle(author)
		for _, b := range hits {
			for _, a := range b.Authors {
				if n := foldTitle(a.Name); n != "" && (strings.Contains(fa, n) || strings.Contains(n, fa)) {
					by = append(by, b)
					break
				}
			}
		}
		if len(by) > 0 {
			hits = by
		}
	}
	if len(hits) != 1 {
		return 0
	}
	return hits[0].ID
}

func (s *Server) handleImportClippings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	f, _, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "send My Clippings.txt as the form field \"file\"")
		return
	}
	defer f.Close()
	clips, err := importers.ParseClippings(f)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "could not read the clippings: "+err.Error())
		return
	}
	dry := queryBool(r, "dry")
	res := newResult(dry)
	res.People = append(res.People, u.Email)
	type found struct {
		Title  string `json:"title"`
		BookID int64  `json:"book_id"`
		Count  int    `json:"count"`
	}
	var matched []found
	for _, cb := range importers.GroupClippings(clips) {
		b := s.findBookByTitle(ctx, u, cb.Title, cb.Author)
		if b == 0 {
			res.miss(strings.TrimSpace(cb.Title + " (" + cb.Author + ")"))
			continue
		}
		n := 0
		for _, e := range cb.Entries {
			kind, text, note := "highlight", e.Text, e.Note
			if e.NoteOnly {
				kind, text, note = "note", "", e.Text
			}
			page, _ := strconv.Atoi(strings.SplitN(e.Page, "-", 2)[0])
			h := sha1.Sum([]byte(cb.Title + "|" + strconv.Itoa(e.LocStart) + "|" + e.Text))
			loc, _ := json.Marshal(map[string]any{"type": "kindle", "location": e.LocStart, "location_end": e.LocEnd, "page": e.Page})
			a := &store.Annotation{BookID: b, Kind: kind, Text: text, Note: note, Page: page, Locator: string(loc), Source: "kindle",
				ExternalID: hex.EncodeToString(h[:]), CreatedAt: e.Added}
			if dry {
				n++
				continue
			}
			created, err := s.store.SaveAnnotation(ctx, u.ID, a)
			if err != nil {
				continue
			}
			if created {
				n++
				res.Counts["new"]++
			} else {
				res.Counts["updated"]++
			}
		}
		res.Counts["highlights"] += len(cb.Entries)
		res.Counts["books"]++
		matched = append(matched, found{Title: cb.Title, BookID: b, Count: n})
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].Count > matched[j].Count })
	if res.Counts["new"] > 0 {
		s.publish(ctx, u, "library.highlighted", "kindle", map[string]any{"count": res.Counts["new"], "source": "kindle", "books": res.Counts["books"]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"dry": dry, "counts": res.Counts, "books": matched, "unmatched": res.Unmatched})
}
