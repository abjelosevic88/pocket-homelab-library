package server

// The OPDS 1.2 catalogue at /opds, for KOReader, CrossPoint and any other
// reader with a catalogue browser: sign in with the account's email and a
// device's app password (or a personal token). CrossPoint 2.0 only sends its
// password after a 401 with a Basic challenge, so every request without one
// gets that challenge.
//
// Feeds page by 50 (CrossPoint holds about 62 entries), "Recently added" is
// newest first with download links that never change, which KOReader's
// catalogue sync relies on. A CrossPoint sees only books it can read (EPUB,
// or convertible to EPUB here) with the EPUB first.

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-library/internal/formats"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

const opdsPageSize = 50

const (
	opdsNav = "application/atom+xml;profile=opds-catalog;kind=navigation"
	opdsAcq = "application/atom+xml;profile=opds-catalog;kind=acquisition"
)

func (s *Server) routesOPDS(r chi.Router) {
	r.Use(s.syncEnabled("opds"))
	r.Use(s.deviceAuth(authOPDS))
	r.Get("/", s.opdsRoot)
	r.Get("/search.xml", s.opdsSearchDescription)
	r.Get("/search", s.opdsSearch)
	r.Get("/recent", s.opdsRecent)
	r.Get("/reading", s.opdsReading)
	r.Get("/want", s.opdsWant)
	r.Get("/queue", s.opdsQueue)
	r.Get("/shelves", s.opdsShelves)
	r.Get("/shelves/{id}", s.opdsShelf)
	r.Get("/libraries", s.opdsLibraries)
	r.Get("/libraries/{id}", s.opdsLibrary)
	r.Get("/authors", s.opdsAuthorLetters)
	r.Get("/authors/letter/{letter}", s.opdsAuthorsByLetter)
	r.Get("/authors/{id}", s.opdsAuthor)
	r.Get("/series", s.opdsSeriesList)
	r.Get("/series/{id}", s.opdsSeries)
	r.Get("/tags", s.opdsTags)
	r.Get("/tags/{tag}", s.opdsTag)
	r.Get("/download/{file}/{name}", s.opdsDownload)
	r.Get("/download/{file}", s.opdsDownload)
	r.Get("/cover/{book}", s.opdsCover)
}

// ---- Atom ----

type atomFeed struct {
	XMLName      xml.Name    `xml:"feed"`
	Xmlns        string      `xml:"xmlns,attr"`
	XmlnsDC      string      `xml:"xmlns:dc,attr"`
	XmlnsOPDS    string      `xml:"xmlns:opds,attr"`
	XmlnsOS      string      `xml:"xmlns:opensearch,attr"`
	XmlnsThr     string      `xml:"xmlns:thr,attr"`
	ID           string      `xml:"id"`
	Title        string      `xml:"title"`
	Updated      string      `xml:"updated"`
	Icon         string      `xml:"icon,omitempty"`
	Author       *atomAuthor `xml:"author,omitempty"`
	TotalResults int         `xml:"opensearch:totalResults,omitempty"`
	ItemsPerPage int         `xml:"opensearch:itemsPerPage,omitempty"`
	StartIndex   int         `xml:"opensearch:startIndex,omitempty"`
	Links        []atomLink  `xml:"link"`
	Entries      []atomEntry `xml:"entry"`
}

type atomLink struct {
	Rel   string `xml:"rel,attr,omitempty"`
	Href  string `xml:"href,attr"`
	Type  string `xml:"type,attr,omitempty"`
	Title string `xml:"title,attr,omitempty"`
	Count int    `xml:"thr:count,attr,omitempty"`
}

type atomAuthor struct {
	Name string `xml:"name"`
	URI  string `xml:"uri,omitempty"`
}

type atomText struct {
	Type string `xml:"type,attr,omitempty"`
	Text string `xml:",chardata"`
}

type atomCategory struct {
	Term  string `xml:"term,attr"`
	Label string `xml:"label,attr"`
}

type atomEntry struct {
	Title      string         `xml:"title"`
	ID         string         `xml:"id"`
	Updated    string         `xml:"updated"`
	Authors    []atomAuthor   `xml:"author"`
	Language   string         `xml:"dc:language,omitempty"`
	Publisher  string         `xml:"dc:publisher,omitempty"`
	Issued     string         `xml:"dc:issued,omitempty"`
	Identifier string         `xml:"dc:identifier,omitempty"`
	Categories []atomCategory `xml:"category"`
	Summary    *atomText      `xml:"summary,omitempty"`
	Content    *atomText      `xml:"content,omitempty"`
	Links      []atomLink     `xml:"link"`
}

func newFeed(id, title string) *atomFeed {
	return &atomFeed{Xmlns: "http://www.w3.org/2005/Atom", XmlnsDC: "http://purl.org/dc/terms/", XmlnsOPDS: "http://opds-spec.org/2010/catalog",
		XmlnsOS: "http://a9.com/-/spec/opensearch/1.1/", XmlnsThr: "http://purl.org/syndication/thread/1.0", ID: "urn:pocket-library:" + id, Title: title, Updated: time.Now().UTC().Format(time.RFC3339),
		Icon: "/favicon.svg", Author: &atomAuthor{Name: "Pocket Library", URI: "https://github.com/abjelosevic88/pocket-homelab-library"}}
}

// commonLinks are on every feed: self, start, up, search.
func (f *atomFeed) commonLinks(self, kind string) {
	f.Links = append(f.Links,
		atomLink{Rel: "self", Href: self, Type: kind},
		atomLink{Rel: "start", Href: "/opds/", Type: opdsNav, Title: "Pocket Library"},
		atomLink{Rel: "search", Href: "/opds/search.xml", Type: "application/opensearchdescription+xml", Title: "Search"},
		atomLink{Rel: "search", Href: "/opds/search?q={searchTerms}", Type: opdsAcq, Title: "Search"},
	)
}

// writeFeed sends a feed with every link absolute (not every reader resolves
// relative ones).
func (s *Server) writeFeed(w http.ResponseWriter, r *http.Request, f *atomFeed, kind string) {
	base := s.publicBase(r)
	abs := func(h string) string {
		if strings.HasPrefix(h, "/") {
			return base + h
		}
		return h
	}
	f.Icon = abs(f.Icon)
	for i := range f.Links {
		f.Links[i].Href = abs(f.Links[i].Href)
	}
	for i := range f.Entries {
		for j := range f.Entries[i].Links {
			f.Entries[i].Links[j].Href = abs(f.Entries[i].Links[j].Href)
		}
		for j := range f.Entries[i].Authors {
			f.Entries[i].Authors[j].URI = abs(f.Entries[i].Authors[j].URI)
		}
	}
	w.Header().Set("Content-Type", kind+";charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	enc.Indent("", " ")
	_ = enc.Encode(f)
}

// navEntry links to another feed.
func navEntry(id, title, href, kind, about string, count int) atomEntry {
	e := atomEntry{Title: title, ID: "urn:pocket-library:" + id, Updated: time.Now().UTC().Format(time.RFC3339),
		Links: []atomLink{{Rel: "subsection", Href: href, Type: kind, Count: count}}}
	if about != "" {
		e.Content = &atomText{Type: "text", Text: about}
	}
	return e
}

// ---- context ----

type opdsCtx struct {
	u         *store.User
	login     *syncLogin
	libs      []int64
	crossview bool // a CrossPoint: EPUBs (or convertible to EPUB) only
	convert   bool
}

func (s *Server) opdsContext(r *http.Request) *opdsCtx {
	ctx := r.Context()
	u := userFrom(ctx)
	l := loginFrom(ctx)
	return &opdsCtx{u: u, login: l, libs: s.visibleLibraries(ctx, u), crossview: l.deviceKind() == "crosspoint", convert: s.cfg.ConvertPath() != ""}
}

func pageOf(r *http.Request) int {
	p, _ := strconv.Atoi(r.URL.Query().Get("page"))
	return max(p, 1)
}

// convertibleToEPUB are formats ebook-convert turns into an EPUB worth reading.
var convertibleToEPUB = map[string]bool{"mobi": true, "azw": true, "azw3": true, "prc": true, "fb2": true, "fbz": true, "docx": true, "rtf": true, "txt": true}

// ---- feeds ----

func (s *Server) opdsRoot(w http.ResponseWriter, r *http.Request) {
	oc := s.opdsContext(r)
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx, 0)
	f := newFeed("root", st.Title)
	f.commonLinks("/opds/", opdsNav)
	add := func(e atomEntry) { f.Entries = append(f.Entries, e) }
	if oc.login.device != nil {
		n, _ := s.store.SyncPendingSends(ctx, oc.u.ID, oc.login.device.ID)
		add(navEntry("queue", "To send to "+oc.login.device.Name, "/opds/queue", opdsAcq, fmt.Sprintf("Books sent to this device from Pocket Library (%d waiting)", len(n)), len(n)))
	}
	add(navEntry("reading", "Continue reading", "/opds/reading", opdsAcq, "Books you are in the middle of", 0))
	add(navEntry("want", "Want to read", "/opds/want", opdsAcq, "Your want-to-read list", 0))
	add(navEntry("recent", "Recently added", "/opds/recent", opdsAcq, "The newest books in your libraries", 0))
	if shelves, err := s.store.ListShelves(ctx, oc.u.ID); err == nil {
		n := 0
		for _, sh := range shelves {
			if sh.OPDS {
				n++
			}
		}
		if n > 0 {
			add(navEntry("shelves", "My shelves", "/opds/shelves", opdsNav, "Your shelves and reading lists", n))
		}
	}
	add(navEntry("libraries", "Libraries", "/opds/libraries", opdsNav, "Every library you can open", len(oc.libs)))
	add(navEntry("authors", "Authors", "/opds/authors", opdsNav, "By author, A to Z", 0))
	add(navEntry("series", "Series", "/opds/series", opdsNav, "Books in series, in order", 0))
	add(navEntry("tags", "Tags", "/opds/tags", opdsNav, "By subject", 0))
	s.writeFeed(w, r, f, opdsNav)
}

func (s *Server) opdsSearchDescription(w http.ResponseWriter, r *http.Request) {
	base := s.publicBase(r)
	w.Header().Set("Content-Type", "application/opensearchdescription+xml;charset=utf-8")
	_, _ = fmt.Fprint(w, xml.Header+`<OpenSearchDescription xmlns="http://a9.com/-/spec/opensearch/1.1/">
 <ShortName>Pocket Library</ShortName>
 <Description>Search titles, authors, series and tags</Description>
 <InputEncoding>UTF-8</InputEncoding>
 <OutputEncoding>UTF-8</OutputEncoding>
 <Url type="`+opdsAcq+`" template="`+base+`/opds/search?q={searchTerms}"/>
 <Url type="application/atom+xml" template="`+base+`/opds/search?q={searchTerms}"/>
</OpenSearchDescription>
`)
}

// bookFeed lists one page of books for a query.
func (s *Server) bookFeed(w http.ResponseWriter, r *http.Request, id, title, self string, q store.BookQuery) {
	oc := s.opdsContext(r)
	q.UserID = oc.u.ID
	if q.Libraries == nil {
		q.Libraries = oc.libs
	}
	if oc.crossview && !oc.convert {
		q.Format = "epub"
	}
	page := pageOf(r)
	q.Limit, q.Offset = opdsPageSize, (page-1)*opdsPageSize
	books, total, err := s.store.ListBooks(r.Context(), q)
	if err != nil {
		http.Error(w, "catalogue unavailable", http.StatusInternalServerError)
		return
	}
	f := newFeed(id, title)
	f.commonLinks(pageURL(self, page), opdsAcq)
	f.paging(self, page, total)
	for _, b := range books {
		if e, ok := s.bookEntry(r.Context(), oc, &b, ""); ok {
			f.Entries = append(f.Entries, e)
		}
	}
	s.writeFeed(w, r, f, opdsAcq)
}

func pageURL(self string, page int) string {
	if page <= 1 {
		return self
	}
	sep := "?"
	if strings.Contains(self, "?") {
		sep = "&"
	}
	return self + sep + "page=" + strconv.Itoa(page)
}

func (f *atomFeed) paging(self string, page, total int) {
	last := max(1, (total+opdsPageSize-1)/opdsPageSize)
	f.TotalResults, f.ItemsPerPage, f.StartIndex = total, opdsPageSize, (page-1)*opdsPageSize+1
	kind := opdsAcq
	f.Links = append(f.Links, atomLink{Rel: "first", Href: pageURL(self, 1), Type: kind})
	if page > 1 {
		f.Links = append(f.Links, atomLink{Rel: "previous", Href: pageURL(self, page-1), Type: kind})
	}
	if page < last {
		f.Links = append(f.Links, atomLink{Rel: "next", Href: pageURL(self, page+1), Type: kind})
	}
	f.Links = append(f.Links, atomLink{Rel: "last", Href: pageURL(self, last), Type: kind})
}

// bookEntry is one book with a download link per format. sendFormat asks for
// that format first (a send waiting for this device).
func (s *Server) bookEntry(ctx context.Context, oc *opdsCtx, b *store.Book, sendFormat string) (atomEntry, bool) {
	files, err := s.store.FilesOfBook(ctx, b.ID)
	if err != nil {
		return atomEntry{}, false
	}
	var live []store.File
	hasEPUB := false
	for _, f := range files {
		if f.Missing {
			continue
		}
		live = append(live, f)
		if f.Format == "epub" {
			hasEPUB = true
		}
	}
	var links []atomLink
	name := opdsFileName(b)
	acq := func(f store.File, format string) atomLink {
		ext, mime := f.Format, formats.MIME(f.Format)
		href := fmt.Sprintf("/opds/download/%d/%s", f.ID, url.PathEscape(name+"."+fileExt(f)))
		if format != "" && format != f.Format {
			ext, mime = format, formats.MIME(format)
			href = fmt.Sprintf("/opds/download/%d/%s?format=%s", f.ID, url.PathEscape(name+"."+ext), format)
		}
		return atomLink{Rel: "http://opds-spec.org/acquisition", Href: href, Type: mime, Title: strings.ToUpper(ext)}
	}
	if !hasEPUB && oc.convert {
		for _, f := range live {
			if convertibleToEPUB[f.Format] {
				links = append(links, acq(f, "epub"))
				break
			}
		}
	}
	for _, f := range live {
		if oc.crossview && f.Format != "epub" {
			continue
		}
		links = append(links, acq(f, ""))
	}
	if len(links) == 0 {
		return atomEntry{}, false
	}
	if sendFormat != "" {
		sort.SliceStable(links, func(i, j int) bool {
			return strings.EqualFold(links[i].Title, sendFormat) && !strings.EqualFold(links[j].Title, sendFormat)
		})
	}
	if b.Cover != "" {
		links = append(links,
			atomLink{Rel: "http://opds-spec.org/image", Href: fmt.Sprintf("/opds/cover/%d?w=600", b.ID), Type: "image/jpeg"},
			atomLink{Rel: "http://opds-spec.org/image/thumbnail", Href: fmt.Sprintf("/opds/cover/%d?w=200", b.ID), Type: "image/jpeg"})
	}
	e := atomEntry{Title: cut(b.Title, 160), ID: fmt.Sprintf("urn:pocket-library:book:%d", b.ID), Updated: atomTime(b.UpdatedAt),
		Language: b.Language, Publisher: b.Publisher, Issued: b.Published, Links: links}
	if b.ISBN != "" {
		e.Identifier = "urn:isbn:" + b.ISBN
	}
	left := 120
	for _, a := range b.Authors {
		if left <= 0 {
			break
		}
		n := cut(a.Name, left)
		left -= utf8.RuneCountInString(n) + 2
		e.Authors = append(e.Authors, atomAuthor{Name: n, URI: fmt.Sprintf("/opds/authors/%d", a.ID)})
	}
	for _, t := range b.Tags {
		e.Categories = append(e.Categories, atomCategory{Term: t, Label: t})
	}
	var about []string
	if b.Series != "" {
		about = append(about, fmt.Sprintf("%s #%s", b.Series, trimFloat(b.SeriesIndex)))
	}
	if b.State != nil && b.State.Percentage > 0 && b.State.Status == "reading" {
		about = append(about, fmt.Sprintf("Read %d%%", int(b.State.Percentage*100)))
	}
	desc := plainText(b.Description)
	if desc == "" {
		if full, err := s.store.GetBookAny(ctx, b.ID); err == nil {
			desc = plainText(full.Description)
		}
	}
	if desc != "" {
		about = append(about, desc)
	}
	if len(about) > 0 {
		e.Summary = &atomText{Type: "text", Text: cut(strings.Join(about, " · "), 500)}
	}
	return e, true
}

func fileExt(f store.File) string {
	if f.Format == "kepub" {
		return "kepub.epub"
	}
	return f.Format
}

// opdsFileName is "Author - Title" without characters devices choke on
// (CrossPoint cuts names at a colon).
func opdsFileName(b *store.Book) string {
	name := b.Title
	if len(b.Authors) > 0 {
		name = b.Authors[0].Name + " - " + b.Title
	}
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		name = "book"
	}
	return cut(name, 120)
}

func cut(v string, n int) string {
	if utf8.RuneCountInString(v) <= n {
		return v
	}
	r := []rune(v)
	return strings.TrimSpace(string(r[:max(0, n-1)])) + "…"
}

func trimFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func atomTime(v string) string {
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return time.Now().UTC().Format(time.RFC3339)
}

// plainText strips markup from a description.
func plainText(v string) string {
	var sb strings.Builder
	in := false
	for _, r := range v {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
			sb.WriteByte(' ')
		case !in:
			sb.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(htmlUnescape(sb.String())), " ")
}

var htmlEntities = strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&nbsp;", " ", "&apos;", "'")

func htmlUnescape(v string) string { return htmlEntities.Replace(v) }

func (s *Server) opdsSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	s.bookFeed(w, r, "search", "Search: "+q, "/opds/search?q="+url.QueryEscape(q), store.BookQuery{Q: q, Sort: "title"})
}

func (s *Server) opdsRecent(w http.ResponseWriter, r *http.Request) {
	s.bookFeed(w, r, "recent", "Recently added", "/opds/recent", store.BookQuery{Sort: "added", Order: "desc"})
}

func (s *Server) opdsReading(w http.ResponseWriter, r *http.Request) {
	s.bookFeed(w, r, "reading", "Continue reading", "/opds/reading", store.BookQuery{Status: "reading", Sort: "last_read", Order: "desc"})
}

func (s *Server) opdsWant(w http.ResponseWriter, r *http.Request) {
	s.bookFeed(w, r, "want", "Want to read", "/opds/want", store.BookQuery{Status: "want", Sort: "added", Order: "desc"})
}

// opdsQueue lists the books waiting for this device; downloading one marks it delivered.
func (s *Server) opdsQueue(w http.ResponseWriter, r *http.Request) {
	oc := s.opdsContext(r)
	ctx := r.Context()
	f := newFeed("queue", "To send")
	f.commonLinks("/opds/queue", opdsAcq)
	if oc.login.device == nil {
		s.writeFeed(w, r, f, opdsAcq)
		return
	}
	f.Title = "To send to " + oc.login.device.Name
	pending, err := s.store.SyncPendingSends(ctx, oc.u.ID, oc.login.device.ID)
	if err != nil {
		http.Error(w, "catalogue unavailable", http.StatusInternalServerError)
		return
	}
	libs := s.allowedLibraries(ctx, oc.u)
	seen := map[int64]bool{}
	for _, p := range pending {
		if seen[p.BookID] {
			continue
		}
		seen[p.BookID] = true
		b, err := s.store.GetBook(ctx, oc.u.ID, p.BookID, libs)
		if err != nil {
			continue
		}
		if e, ok := s.bookEntry(ctx, oc, b, p.Format); ok {
			f.Entries = append(f.Entries, e)
		}
	}
	s.writeFeed(w, r, f, opdsAcq)
}

func (s *Server) opdsShelves(w http.ResponseWriter, r *http.Request) {
	oc := s.opdsContext(r)
	f := newFeed("shelves", "My shelves")
	f.commonLinks("/opds/shelves", opdsNav)
	shelves, _ := s.store.ListShelves(r.Context(), oc.u.ID)
	for _, sh := range shelves {
		if !sh.OPDS {
			continue
		}
		f.Entries = append(f.Entries, navEntry(fmt.Sprintf("shelf:%d", sh.ID), sh.Name, fmt.Sprintf("/opds/shelves/%d", sh.ID), opdsAcq, sh.Description, sh.Books))
	}
	s.writeFeed(w, r, f, opdsNav)
}

func (s *Server) opdsShelf(w http.ResponseWriter, r *http.Request) {
	oc := s.opdsContext(r)
	sh, err := s.store.GetShelf(r.Context(), oc.u.ID, idParam(r, "id"))
	if err != nil || !sh.OPDS {
		http.NotFound(w, r)
		return
	}
	q := store.BookQuery{ShowHidden: true}
	if sh.Kind == "smart" {
		_ = json.Unmarshal([]byte(sh.Filter), &q)
		q.Offset, q.Limit = 0, 0
	} else {
		q.Shelf, q.Status = sh.ID, "any"
		if sh.Ordered {
			q.Sort = "shelf"
		} else if q.Sort == "" {
			q.Sort, q.Order = "added", "desc"
		}
	}
	s.bookFeed(w, r, fmt.Sprintf("shelf:%d", sh.ID), sh.Name, fmt.Sprintf("/opds/shelves/%d", sh.ID), q)
}

func (s *Server) opdsLibraries(w http.ResponseWriter, r *http.Request) {
	oc := s.opdsContext(r)
	f := newFeed("libraries", "Libraries")
	f.commonLinks("/opds/libraries", opdsNav)
	libs, _ := s.store.LibrariesFor(r.Context(), oc.u)
	for _, l := range libs {
		f.Entries = append(f.Entries, navEntry(fmt.Sprintf("library:%d", l.ID), l.Name, fmt.Sprintf("/opds/libraries/%d", l.ID), opdsAcq, "", l.Books))
	}
	s.writeFeed(w, r, f, opdsNav)
}

func (s *Server) opdsLibrary(w http.ResponseWriter, r *http.Request) {
	oc := s.opdsContext(r)
	id := idParam(r, "id")
	var lib *store.Library
	libs, _ := s.store.LibrariesFor(r.Context(), oc.u)
	for i := range libs {
		if libs[i].ID == id {
			lib = &libs[i]
		}
	}
	if lib == nil {
		http.NotFound(w, r)
		return
	}
	sortBy := "title"
	if lib.Kind != "books" {
		sortBy = "series"
	}
	s.bookFeed(w, r, fmt.Sprintf("library:%d", id), lib.Name, fmt.Sprintf("/opds/libraries/%d", id), store.BookQuery{Libraries: []int64{id}, Sort: sortBy})
}

// allAuthors loads every author with books the person can open.
func (s *Server) allAuthors(ctx context.Context, libs []int64) []store.Author {
	var out []store.Author
	for off := 0; ; off += 1000 {
		list, total, err := s.store.ListAuthors(ctx, libs, "", off, 1000)
		if err != nil {
			break
		}
		out = append(out, list...)
		if off+1000 >= total || len(list) == 0 {
			break
		}
	}
	return out
}

func letterOf(name string) string {
	for _, r := range name {
		if m, ok := foldMap[unicode.ToLower(r)]; ok && m != "" {
			return strings.ToUpper(m[:1])
		}
		if unicode.IsLetter(r) {
			return string(unicode.ToUpper(r))
		}
		if !unicode.IsSpace(r) {
			return "#"
		}
	}
	return "#"
}

func (s *Server) opdsAuthorLetters(w http.ResponseWriter, r *http.Request) {
	oc := s.opdsContext(r)
	f := newFeed("authors", "Authors")
	f.commonLinks("/opds/authors", opdsNav)
	counts := map[string]int{}
	for _, a := range s.allAuthors(r.Context(), oc.libs) {
		counts[letterOf(a.SortName)]++
	}
	letters := make([]string, 0, len(counts))
	for l := range counts {
		letters = append(letters, l)
	}
	sort.Strings(letters)
	for _, l := range letters {
		f.Entries = append(f.Entries, navEntry("authors:"+l, l, "/opds/authors/letter/"+url.PathEscape(l), opdsNav, fmt.Sprintf("%d authors", counts[l]), counts[l]))
	}
	s.writeFeed(w, r, f, opdsNav)
}

func (s *Server) opdsAuthorsByLetter(w http.ResponseWriter, r *http.Request) {
	oc := s.opdsContext(r)
	letter := chi.URLParam(r, "letter")
	self := "/opds/authors/letter/" + url.PathEscape(letter)
	var list []store.Author
	for _, a := range s.allAuthors(r.Context(), oc.libs) {
		if letterOf(a.SortName) == letter {
			list = append(list, a)
		}
	}
	page := pageOf(r)
	f := newFeed("authors:"+letter, "Authors: "+letter)
	f.commonLinks(pageURL(self, page), opdsNav)
	f.paging(self, page, len(list))
	for _, a := range pageSlice(list, page) {
		f.Entries = append(f.Entries, navEntry(fmt.Sprintf("author:%d", a.ID), a.Name, fmt.Sprintf("/opds/authors/%d", a.ID), opdsAcq, fmt.Sprintf("%d books", a.Books), a.Books))
	}
	s.writeFeed(w, r, f, opdsNav)
}

func pageSlice[T any](list []T, page int) []T {
	from := (page - 1) * opdsPageSize
	if from >= len(list) {
		return nil
	}
	return list[from:min(len(list), from+opdsPageSize)]
}

func (s *Server) opdsAuthor(w http.ResponseWriter, r *http.Request) {
	oc := s.opdsContext(r)
	a, err := s.store.GetAuthor(r.Context(), idParam(r, "id"), oc.libs)
	if err != nil || a.Books == 0 {
		http.NotFound(w, r)
		return
	}
	s.bookFeed(w, r, fmt.Sprintf("author:%d", a.ID), a.Name, fmt.Sprintf("/opds/authors/%d", a.ID), store.BookQuery{Author: a.ID, Sort: "series"})
}

func (s *Server) opdsSeriesList(w http.ResponseWriter, r *http.Request) {
	oc := s.opdsContext(r)
	page := pageOf(r)
	list, total, err := s.store.ListSeries(r.Context(), oc.u.ID, oc.libs, "", (page-1)*opdsPageSize, opdsPageSize)
	if err != nil {
		http.Error(w, "catalogue unavailable", http.StatusInternalServerError)
		return
	}
	f := newFeed("series", "Series")
	f.commonLinks(pageURL("/opds/series", page), opdsNav)
	f.paging("/opds/series", page, total)
	for _, se := range list {
		f.Entries = append(f.Entries, navEntry(fmt.Sprintf("series:%d", se.ID), se.Name, fmt.Sprintf("/opds/series/%d", se.ID), opdsAcq, fmt.Sprintf("%d books", se.Books), se.Books))
	}
	s.writeFeed(w, r, f, opdsNav)
}

func (s *Server) opdsSeries(w http.ResponseWriter, r *http.Request) {
	oc := s.opdsContext(r)
	se, err := s.store.GetSeries(r.Context(), oc.u.ID, idParam(r, "id"), oc.libs)
	if err != nil || se.Books == 0 {
		http.NotFound(w, r)
		return
	}
	s.bookFeed(w, r, fmt.Sprintf("series:%d", se.ID), se.Name, fmt.Sprintf("/opds/series/%d", se.ID), store.BookQuery{Series: se.ID, Sort: "series"})
}

func (s *Server) opdsTags(w http.ResponseWriter, r *http.Request) {
	oc := s.opdsContext(r)
	tags, err := s.store.ListTags(r.Context(), oc.libs, "", 2000)
	if err != nil {
		http.Error(w, "catalogue unavailable", http.StatusInternalServerError)
		return
	}
	sort.Slice(tags, func(i, j int) bool { return strings.ToLower(tags[i].Name) < strings.ToLower(tags[j].Name) })
	page := pageOf(r)
	f := newFeed("tags", "Tags")
	f.commonLinks(pageURL("/opds/tags", page), opdsNav)
	f.paging("/opds/tags", page, len(tags))
	for _, t := range pageSlice(tags, page) {
		f.Entries = append(f.Entries, navEntry("tag:"+t.Name, t.Name, "/opds/tags/"+url.PathEscape(t.Name), opdsAcq, fmt.Sprintf("%d books", t.Books), t.Books))
	}
	s.writeFeed(w, r, f, opdsNav)
}

func (s *Server) opdsTag(w http.ResponseWriter, r *http.Request) {
	tag := chi.URLParam(r, "tag")
	if t, err := url.PathUnescape(tag); err == nil {
		tag = t
	}
	s.bookFeed(w, r, "tag:"+tag, tag, "/opds/tags/"+url.PathEscape(tag), store.BookQuery{Tag: tag, Sort: "title"})
}

// opdsDownload sends a file (converted with ?format=) to a signed-in e-reader;
// a book this device was waiting for counts as delivered.
func (s *Server) opdsDownload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	f, err := s.store.GetFile(ctx, idParam(r, "file"))
	if err != nil || f.Missing {
		http.NotFound(w, r)
		return
	}
	b, err := s.store.GetBook(ctx, u.ID, f.BookID, s.allowedLibraries(ctx, u))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == f.Format {
		format = ""
	}
	if dev := loginFrom(ctx).deviceID(); dev > 0 && r.Method == http.MethodGet && r.Header.Get("Range") == "" {
		if n, err := s.store.SyncMarkBookFetched(ctx, u.ID, dev, b.ID); err == nil && n > 0 {
			s.log.Info("sent book fetched", "user", u.ID, "device", dev, "book", b.ID)
		}
	}
	s.serveBookFile(w, r, u, b, f, format)
}

// opdsCover sends a book's cover to a signed-in e-reader.
func (s *Server) opdsCover(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	id := idParam(r, "book")
	if _, err := s.store.GetBook(ctx, u.ID, id, s.allowedLibraries(ctx, u)); err != nil {
		http.NotFound(w, r)
		return
	}
	name, err := s.store.SyncCoverFile(ctx, id)
	if err != nil || name == "" || strings.ContainsAny(name, "/\\") {
		http.NotFound(w, r)
		return
	}
	p := filepath.Join(s.cfg.CoversDir, name)
	if _, err := os.Stat(p); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, p)
}
