// Package metadata looks books and authors up in free public catalogues —
// Open Library (no key) and Google Books (an API key) — and downloads covers.
package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/abjelosevic88/pocket-homelab-library/internal/formats"
)

// Candidate is one search result, normalised across sources (the web app's
// MetadataCandidate).
type Candidate struct {
	Source      string            `json:"source"` // openlibrary | google
	ID          string            `json:"id"`     // "/works/OL45804W" or a Google volume id
	Title       string            `json:"title"`
	Subtitle    string            `json:"subtitle"`
	Authors     []string          `json:"authors"`
	Description string            `json:"description"`
	Publisher   string            `json:"publisher"`
	Published   string            `json:"published"`
	ISBN        string            `json:"isbn"`
	Language    string            `json:"language"`
	Pages       int               `json:"pages"`
	Series      string            `json:"series"`
	SeriesIndex float64           `json:"series_index"`
	Tags        []string          `json:"tags"`
	Cover       string            `json:"cover"` // remote URL
	Identifiers map[string]string `json:"identifiers"`
}

// Client talks to the catalogues.
type Client struct {
	HTTP      *http.Client
	UserAgent string
}

// New returns a client.
func New(userAgent string) *Client {
	return &Client{HTTP: &http.Client{Timeout: 20 * time.Second}, UserAgent: userAgent}
}

// ErrNoGoogleKey is returned when Google Books is asked for without a key.
var ErrNoGoogleKey = errors.New("Google Books needs an API key (Settings → Metadata)")

func (c *Client) getJSON(ctx context.Context, u string, v any) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(300*attempt) * time.Millisecond):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", c.UserAgent)
		req.Header.Set("Accept", "application/json")
		res, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if res.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
			res.Body.Close()
			lastErr = fmt.Errorf("HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
			if res.StatusCode >= 500 || res.StatusCode == http.StatusTooManyRequests {
				continue
			}
			return lastErr
		}
		err = json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(v)
		res.Body.Close()
		return err
	}
	return lastErr
}

// Search looks a book up by ISBN (when given) or by words, in Open Library
// and, with a key, Google Books; results known to both are merged.
func (c *Client) Search(ctx context.Context, query, isbn, googleKey string, limit int) ([]Candidate, []string, error) {
	query = strings.TrimSpace(query)
	isbn = formats.NormalizeISBN(isbn)
	if isbn == "" && formats.NormalizeISBN(query) != "" && !strings.ContainsAny(strings.ToLower(query), "abcdefghijklmnopqrstuvwyz") {
		isbn = formats.NormalizeISBN(query)
	}
	if isbn == "" && len([]rune(query)) < 3 {
		return []Candidate{}, nil, nil
	}
	if limit <= 0 || limit > 30 {
		limit = 10
	}
	var wg sync.WaitGroup
	var ol, gb []Candidate
	var olErr, gbErr error
	wg.Add(1)
	go func() { defer wg.Done(); ol, olErr = c.openLibrary(ctx, query, isbn, limit) }()
	if googleKey != "" {
		wg.Add(1)
		go func() { defer wg.Done(); gb, gbErr = c.google(ctx, query, isbn, googleKey, limit) }()
	}
	wg.Wait()
	var warnings []string
	if olErr != nil {
		warnings = append(warnings, "Open Library: "+olErr.Error())
	}
	if gbErr != nil {
		warnings = append(warnings, "Google Books: "+gbErr.Error())
	}
	if olErr != nil && (googleKey == "" || gbErr != nil) {
		return []Candidate{}, warnings, olErr
	}
	return merge(ol, gb, limit), warnings, nil
}

// merge interleaves two lists, folding a book both know (same ISBN) into one.
func merge(a, b []Candidate, limit int) []Candidate {
	out := []Candidate{}
	byISBN := map[string]int{}
	add := func(c Candidate) {
		if c.ISBN != "" {
			if i, ok := byISBN[c.ISBN]; ok {
				fill(&out[i], c)
				return
			}
			byISBN[c.ISBN] = len(out)
		}
		out = append(out, c)
	}
	for i := 0; i < len(a) || i < len(b); i++ {
		if i < len(a) {
			add(a[i])
		}
		if i < len(b) {
			add(b[i])
		}
	}
	if len(out) > limit*2 {
		out = out[:limit*2]
	}
	return out
}

func fill(dst *Candidate, src Candidate) {
	if dst.Subtitle == "" {
		dst.Subtitle = src.Subtitle
	}
	if dst.Description == "" {
		dst.Description = src.Description
	}
	if dst.Publisher == "" {
		dst.Publisher = src.Publisher
	}
	if dst.Published == "" {
		dst.Published = src.Published
	}
	if dst.Pages == 0 {
		dst.Pages = src.Pages
	}
	if dst.Language == "" {
		dst.Language = src.Language
	}
	if dst.Cover == "" {
		dst.Cover = src.Cover
	}
	if len(dst.Tags) == 0 {
		dst.Tags = src.Tags
	}
	for k, v := range src.Identifiers {
		if _, ok := dst.Identifiers[k]; !ok {
			dst.Identifiers[k] = v
		}
	}
}

// ---- Open Library ----

const olBase = "https://openlibrary.org"

type olDoc struct {
	Key          string   `json:"key"`
	Title        string   `json:"title"`
	Subtitle     string   `json:"subtitle"`
	AuthorName   []string `json:"author_name"`
	FirstPublish int      `json:"first_publish_year"`
	ISBN         []string `json:"isbn"`
	CoverI       int      `json:"cover_i"`
	PagesMedian  int      `json:"number_of_pages_median"`
	Publisher    []string `json:"publisher"`
	Language     []string `json:"language"`
	Subject      []string `json:"subject"`
}

func olCover(id int) string {
	if id <= 0 {
		return ""
	}
	return fmt.Sprintf("https://covers.openlibrary.org/b/id/%d-L.jpg", id)
}

func (c *Client) openLibrary(ctx context.Context, query, isbn string, limit int) ([]Candidate, error) {
	v := url.Values{}
	if isbn != "" {
		v.Set("q", "isbn:"+isbn)
	} else {
		v.Set("q", query)
	}
	v.Set("limit", strconv.Itoa(limit))
	v.Set("fields", "key,title,subtitle,author_name,first_publish_year,isbn,cover_i,number_of_pages_median,publisher,language,subject")
	var res struct {
		Docs []olDoc `json:"docs"`
	}
	if err := c.getJSON(ctx, olBase+"/search.json?"+v.Encode(), &res); err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(res.Docs))
	for _, d := range res.Docs {
		cand := Candidate{Source: "openlibrary", ID: d.Key, Title: d.Title, Subtitle: d.Subtitle, Authors: firstN(d.AuthorName, 4),
			Pages: d.PagesMedian, Tags: cleanSubjects(d.Subject), Cover: olCover(d.CoverI), Identifiers: map[string]string{"openlibrary": strings.TrimPrefix(d.Key, "/works/")}}
		if d.FirstPublish > 0 {
			cand.Published = strconv.Itoa(d.FirstPublish)
		}
		if len(d.Publisher) > 0 {
			cand.Publisher = d.Publisher[0]
		}
		if len(d.Language) == 1 {
			cand.Language = formats.Language(d.Language[0])
		}
		// a work has many editions: only an ISBN that was searched for names this one
		if isbn != "" {
			cand.ISBN = pickISBN(d.ISBN, isbn)
		}
		if cand.ISBN != "" {
			cand.Identifiers["isbn"] = cand.ISBN
		}
		out = append(out, cand)
	}
	// the search leaves descriptions out: fetch the works of the first few
	var wg sync.WaitGroup
	for i := range out {
		if i >= 5 {
			break
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var w struct {
				Description json.RawMessage `json:"description"`
				Covers      []int           `json:"covers"`
			}
			if err := c.getJSON(ctx, olBase+out[i].ID+".json", &w); err == nil {
				out[i].Description = PlainText(stripOLSources(olText(w.Description)))
				if out[i].Cover == "" && len(w.Covers) > 0 {
					out[i].Cover = olCover(w.Covers[0])
				}
			}
		}(i)
	}
	wg.Wait()
	if isbn != "" && len(out) > 0 {
		// an ISBN names one edition: its publisher, pages, date and cover
		var ed struct {
			Publishers    []string `json:"publishers"`
			NumberOfPages int      `json:"number_of_pages"`
			PublishDate   string   `json:"publish_date"`
			Covers        []int    `json:"covers"`
			Languages     []struct {
				Key string `json:"key"`
			} `json:"languages"`
		}
		if err := c.getJSON(ctx, olBase+"/isbn/"+isbn+".json", &ed); err == nil {
			if len(ed.Publishers) > 0 {
				out[0].Publisher = ed.Publishers[0]
			}
			if ed.NumberOfPages > 0 {
				out[0].Pages = ed.NumberOfPages
			}
			if d := formats.NormalizeDate(ed.PublishDate); d != "" {
				out[0].Published = d
			}
			if len(ed.Covers) > 0 && ed.Covers[0] > 0 {
				out[0].Cover = olCover(ed.Covers[0])
			}
			if len(ed.Languages) > 0 {
				out[0].Language = formats.Language(strings.TrimPrefix(ed.Languages[0].Key, "/languages/"))
			}
			out[0].ISBN = isbn
			out[0].Identifiers["isbn"] = isbn
		}
	}
	return out, nil
}

func pickISBN(all []string, want string) string {
	for _, s := range all {
		if want != "" && formats.NormalizeISBN(s) == want {
			return want
		}
	}
	for _, s := range all {
		if len(s) == 13 {
			if n := formats.NormalizeISBN(s); n != "" {
				return n
			}
		}
	}
	for _, s := range all {
		if n := formats.NormalizeISBN(s); n != "" {
			return n
		}
	}
	return ""
}

func olText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var v struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &v) == nil {
		return v.Value
	}
	return ""
}

func stripOLSources(s string) string {
	if i := strings.Index(s, "\n----------"); i > 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// ---- Google Books ----

type gbVolume struct {
	ID         string `json:"id"`
	VolumeInfo struct {
		Title               string   `json:"title"`
		Subtitle            string   `json:"subtitle"`
		Authors             []string `json:"authors"`
		Publisher           string   `json:"publisher"`
		PublishedDate       string   `json:"publishedDate"`
		Description         string   `json:"description"`
		PageCount           int      `json:"pageCount"`
		Categories          []string `json:"categories"`
		Language            string   `json:"language"`
		IndustryIdentifiers []struct {
			Type       string `json:"type"`
			Identifier string `json:"identifier"`
		} `json:"industryIdentifiers"`
		ImageLinks map[string]string `json:"imageLinks"`
	} `json:"volumeInfo"`
}

func (c *Client) google(ctx context.Context, query, isbn, key string, limit int) ([]Candidate, error) {
	if key == "" {
		return nil, ErrNoGoogleKey
	}
	v := url.Values{}
	if isbn != "" {
		v.Set("q", "isbn:"+isbn)
	} else {
		v.Set("q", query)
	}
	v.Set("maxResults", strconv.Itoa(min(limit, 40)))
	v.Set("printType", "books")
	v.Set("key", key)
	v.Set("country", "US")
	var res struct {
		Items []gbVolume `json:"items"`
	}
	if err := c.getJSON(ctx, "https://www.googleapis.com/books/v1/volumes?"+v.Encode(), &res); err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(res.Items))
	for _, it := range res.Items {
		vi := it.VolumeInfo
		cand := Candidate{Source: "google", ID: it.ID, Title: vi.Title, Subtitle: vi.Subtitle, Authors: firstN(vi.Authors, 4),
			Publisher: vi.Publisher, Published: formats.NormalizeDate(vi.PublishedDate), Pages: vi.PageCount, Language: formats.Language(vi.Language),
			Description: PlainText(vi.Description), Identifiers: map[string]string{"google": it.ID}}
		for _, id := range vi.IndustryIdentifiers {
			if (id.Type == "ISBN_13" || id.Type == "ISBN_10") && cand.ISBN == "" {
				cand.ISBN = formats.NormalizeISBN(id.Identifier)
			}
		}
		if cand.ISBN != "" {
			cand.Identifiers["isbn"] = cand.ISBN
		}
		var subj []string
		for _, cat := range vi.Categories {
			for _, p := range strings.Split(cat, "/") {
				if p = strings.TrimSpace(p); p != "" && p != "General" {
					subj = append(subj, p)
				}
			}
		}
		cand.Tags = cleanSubjects(subj)
		for _, k := range []string{"extraLarge", "large", "medium", "small", "thumbnail"} {
			if u := vi.ImageLinks[k]; u != "" {
				cand.Cover = strings.Replace(strings.Replace(u, "http://", "https://", 1), "&edge=curl", "", 1)
				break
			}
		}
		out = append(out, cand)
	}
	return out, nil
}

// ---- helpers ----

func cleanSubjects(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		k := strings.ToLower(s)
		if s == "" || len(s) > 28 || strings.ContainsAny(s, ":,()") || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, s)
		if len(out) == 8 {
			break
		}
	}
	return out
}

func firstN(ss []string, n int) []string {
	if len(ss) > n {
		return ss[:n]
	}
	return ss
}

var (
	tagBreak = regexp.MustCompile(`(?i)<\s*(br|/p|/div|/li)\s*/?>`)
	tagAny   = regexp.MustCompile(`<[^>]*>`)
	blank3   = regexp.MustCompile(`\n{3,}`)
)

// PlainText turns the light HTML some catalogues return into plain text.
func PlainText(s string) string {
	s = tagBreak.ReplaceAllString(s, "\n")
	s = tagAny.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = blank3.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// Fold makes titles and names comparable: lower case, no diacritics, no punctuation.
func Fold(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		if f, ok := foldMap[r]; ok {
			b.WriteString(f)
			space = false
			continue
		}
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127 && isLetter(r) {
			b.WriteRune(r)
			space = false
		} else if !space && b.Len() > 0 {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

var foldMap = map[rune]string{'č': "c", 'ć': "c", 'š': "s", 'ž': "z", 'đ': "dj", 'á': "a", 'à': "a", 'â': "a", 'ä': "a", 'ã': "a", 'å': "a",
	'é': "e", 'è': "e", 'ê': "e", 'ë': "e", 'í': "i", 'ì': "i", 'î': "i", 'ï': "i", 'ó': "o", 'ò': "o", 'ô': "o", 'ö': "o", 'õ': "o", 'ø': "o",
	'ú': "u", 'ù': "u", 'û': "u", 'ü': "u", 'ñ': "n", 'ç': "c", 'ý': "y", 'ÿ': "y", 'ß': "ss", 'ł': "l", 'ń': "n", 'ś': "s", 'ź': "z", 'ż': "z",
	'ř': "r", 'ě': "e", 'ů': "u", 'ť': "t", 'ď': "d", 'ň': "n", 'ő': "o", 'ű': "u"}

func isLetter(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// Confident reports whether a candidate is the same book: equal titles and a shared author.
func Confident(c Candidate, title string, authors []string) bool {
	if Fold(c.Title) != Fold(title) || Fold(title) == "" {
		return false
	}
	for _, a := range authors {
		fa := Fold(a)
		for _, b := range c.Authors {
			fb := Fold(b)
			if fa == fb || (fa != "" && fb != "" && lastWord(fa) == lastWord(fb)) {
				return true
			}
		}
	}
	return false
}

func lastWord(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return f[len(f)-1]
}
