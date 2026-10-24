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
// As the actor: Library reads the person's Pocket Bookshelf (their rating and
// status of a book, shown on the book page).

var linkOffers = []core.Offer{
	{Scope: "reading:read", Name: "See what you read", Description: "Read what you read in your library (where you are in each book, books you finished, reading time per day, highlights and notes), for your reading log, journal and dashboards."},
	{Scope: "library:read", Name: "Look through your library", Description: "Search the books you can open in Pocket Library and see their covers and details, e.g. to show that you own a book as an ebook."},
}

var linkUses = []core.Use{
	{App: "bookshelf", Scopes: []string{"books:read"}, Reason: "Show your rating and reading status from Pocket Bookshelf on each book's page"},
}

type linkRoute struct {
	methods string
	path    *regexp.Regexp
	scope   string // "" = any linked app
}

var linkRoutes = []linkRoute{
	{"GET", regexp.MustCompile(`^/api/v1/auth/me$`), ""},
	{"GET", regexp.MustCompile(`^/api/v1/(reading/feed|reading/current|stats|dashboard)$`), "reading:read"},
	{"GET", regexp.MustCompile(`^/api/v1/(sessions|reads|annotations)$`), "reading:read"},
	{"GET", regexp.MustCompile(`^/api/v1/day/\d{4}-\d{2}-\d{2}$`), "reading:read"},
	{"GET", regexp.MustCompile(`^/api/v1/books$`), "library:read"},
	{"GET", regexp.MustCompile(`^/api/v1/books/\d+$`), "library:read"},
	{"GET", regexp.MustCompile(`^/api/v1/books/\d+/cover$`), ""},
}

var errLinkScope = errors.New("outside the link's scope")

// linkAllows reports whether a link token with these scopes may make this request.
func linkAllows(scopes []string, r *http.Request) bool {
	for _, lr := range linkRoutes {
		if !strings.Contains(" "+lr.methods+" ", " "+r.Method+" ") || !lr.path.MatchString(r.URL.Path) {
			continue
		}
		if lr.scope == "" {
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
