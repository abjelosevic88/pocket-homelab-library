package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

// Pocket Core app links (docs/links.md in pocket-homelab-core).
//
// As the target: another Pocket app the person allowed in Core may act here
// for them with a pcx_ token, only within these scopes. Every route not
// listed in linkRoutes stays closed to it (403).
//
// As the actor: Library reads and writes the person's Pocket Bookshelf (their
// rating and status of a book, and which book there is this one; bookshelf.go)
// and has Pocket AI translate books.

var linkOffers = []core.Offer{
	{Scope: "library:import", Name: "Import books into your library", Description: "Let Pocket Books add ebook files to your private Balkan Downloads collection, with their original forum source. Other apps and people cannot use this import."},
	{Scope: "reading:read", Name: "See what you read", Description: "Read what you read in your library (where you are in each book, books you finished, reading time per day, highlights and notes), for your reading log, journal and dashboards."},
	{Scope: "library:read", Name: "Look through your library", Description: "Search the books you can open in Pocket Library and see their covers and details, e.g. to show that you own a book as an ebook."},
}

var linkUses = []core.Use{
	{App: "bookshelf", Scopes: []string{"books:read", "books:write"}, Reason: "Show your rating and reading status from Pocket Bookshelf on each book's page, write the status and rating you set here there, and add or link a book to the same one on your shelves"},
	core.InferenceUse(), // Translate: books into Serbian with your Pocket AI
	requestsUse,         // Get it: the audiobook, another language, more by the author
}

// portable is what Pocket Move may copy out and bring in for a person
// (docs/move.md in pocket-homelab-core); Register adds move:export/import.
// The books themselves are the box's; what moves is the person's reading.
var portable = &core.Portable{
	Exports: []core.PortableExport{
		{Path: "/api/v1/export.json", File: "library.json", Kind: core.KindPocket, Title: "Your reading: where you are in each book, ratings, books you finished, reading time, highlights and shelves (the books named by title, author and ISBN)"},
		{Path: "/api/v1/annotations/export", File: "highlights.md", Kind: core.KindMarkdown, Title: "Your highlights and notes, book by book"},
	},
	Imports: []core.PortableImport{
		{Path: "/api/v1/import", Kind: core.KindPocket, ContentType: "application/json", Title: "Brings your reading back for the books this library has (matched by ISBN, else title and author); what you already have is left alone"},
		{Path: "/api/v1/import/kindle-clippings", Kind: "kindle-clippings", Form: "file", MaxBytes: 64 << 20, Title: "Adds the highlights and notes from a Kindle's My Clippings.txt to the books this library has; a second run adds nothing"},
	},
}

// capabilities is what the apps that bring everything together may ask
// (docs/capabilities.md in pocket-homelab-core): Pocket Search finds the
// books a person can open, Pocket Day reads what they read on a day, Pocket
// Hub shows what they are reading. Register adds the pocket:* offers; the
// routes are in capabilities.go. Nothing is kept from a quick line: a book
// here is a file.
var capabilities = core.Capabilities{
	Search: &core.Searchable{What: "The books, comics and manga you can open here, by title, author, series and publisher", Kinds: []string{"book", "comic", "manga"}, Bangs: []string{"lib", "library", "ebook"}},
	Day:    &core.DayFeed{Label: "Library", One: "book", Many: "books", Unit: "min", What: "The books you read that day with the minutes and pages, the ones you finished, and the books that were added"},
	Widget: &core.Widget{Title: "Continue reading", What: "The books you are reading and where you are in each, then the newest books"},
}

type linkRoute struct {
	methods string
	path    *regexp.Regexp
	scope   string // "" = any linked app, anyOwnScope = any but one with only pocket:* scopes
}

// anyOwnScope opens a route to every link but one that holds only pocket:*
// scopes: each of those opens its one route (capabilities) and nothing more.
const anyOwnScope = "*"

var linkRoutes = []linkRoute{
	{"POST", regexp.MustCompile(`^/api/v1/imports/balkan-books$`), "library:import"},
	{"GET", regexp.MustCompile(`^/api/v1/auth/me$`), ""},
	{"GET", regexp.MustCompile(`^/api/v1/(reading/feed|reading/current|stats|dashboard)$`), "reading:read"},
	{"GET", regexp.MustCompile(`^/api/v1/(sessions|reads|annotations)$`), "reading:read"},
	{"GET", regexp.MustCompile(`^/api/v1/day/\d{4}-\d{2}-\d{2}$`), "reading:read"},
	{"GET", regexp.MustCompile(`^/api/v1/books$`), "library:read"},
	{"GET", regexp.MustCompile(`^/api/v1/books/\d+$`), "library:read"},
	{"GET", regexp.MustCompile(`^/api/v1/books/\d+/cover$`), anyOwnScope},
}

var errLinkScope = errors.New("outside the link's scope")

// linkAllows reports whether a link token with these scopes may make this request.
func linkAllows(scopes []string, r *http.Request) bool {
	if portable.LinkAllows(scopes, r) || capabilities.LinkAllows(scopes, r) {
		return true
	}
	for _, lr := range linkRoutes {
		if !strings.Contains(" "+lr.methods+" ", " "+r.Method+" ") || !lr.path.MatchString(r.URL.Path) {
			continue
		}
		if lr.scope == "" || lr.scope == anyOwnScope && !onlyCapabilities(scopes) {
			return true
		}
		for _, sc := range scopes {
			if sc == lr.scope {
				return true
			}
		}
	}
	return false
}

// onlyCapabilities reports whether a link holds pocket:* scopes and no other.
func onlyCapabilities(scopes []string) bool {
	for _, sc := range scopes {
		if !strings.HasPrefix(sc, "pocket:") {
			return false
		}
	}
	return len(scopes) > 0
}

// linkUser validates a pcx_ token, checks the route against its scopes and
// maps it to the person it acts for. The second value is the acting app's slug.
func (s *Server) linkUser(ctx context.Context, tok string, r *http.Request) (*store.User, string, error) {
	in, err := s.core.Introspect(ctx, tok)
	if err != nil {
		return nil, "", err
	}
	if !in.Active || !in.Delegated() || in.User == nil || in.User.Email == "" || in.App == nil || in.App.Slug == "" {
		return nil, "", http.ErrNoCookie
	}
	if r.URL.Path == "/api/v1/imports/balkan-books" && (in.Audience != "library" || in.App.Slug != "books") {
		return nil, "", errLinkScope
	}
	if !linkAllows(strings.Fields(in.Scope), r) {
		return nil, "", errLinkScope
	}
	u, err := s.coreIdentityUser(ctx, in)
	return u, in.App.Slug, err
}

const linkAppKey ctxKey = 2

// linkApp is the acting app's slug when the request came in on a link token, "" otherwise.
func linkApp(ctx context.Context) string {
	a, _ := ctx.Value(linkAppKey).(string)
	return a
}
