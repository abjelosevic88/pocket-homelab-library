// Kindle clippings: the "My Clippings.txt" file a Kindle keeps in its
// documents folder: every highlight, note and bookmark, one entry after the
// other, separated by "==========".
//
//	The Power of Now (Tolle, Eckhart)
//	- Your Highlight on page 21 | Location 312-314 | Added on Sunday, 3 May 2021 21:14:02
//
//	Realize deeply that the present moment is all you ever have.
//	==========

package importers

import (
	"bufio"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Kinds of clippings.
const (
	clipHighlight = "highlight"
	clipNote      = "note"
	clipBookmark  = "bookmark"
)

// Clipping is one entry of the file.
type Clipping struct {
	Kind     string
	Title    string
	Author   string
	Page     string // as printed, e.g. "21" or "xii"
	LocStart int
	LocEnd   int
	Added    string // RFC 3339 (UTC), "" when the date could not be read
	Text     string
}

// The metadata line is localised; these cover the Kindle UI languages that
// matter here. Matching is on lower-cased text.
var (
	kindWords = []struct{ kind, word string }{
		{clipBookmark, "bookmark"}, {clipBookmark, "lesezeichen"}, {clipBookmark, "signet"}, {clipBookmark, "marcador"}, {clipBookmark, "segnalibro"},
		{clipNote, "note"}, {clipNote, "notiz"}, {clipNote, "nota"},
		{clipHighlight, "highlight"}, {clipHighlight, "markierung"}, {clipHighlight, "surlignement"}, {clipHighlight, "subrayado"}, {clipHighlight, "evidenziazione"}, {clipHighlight, "destaque"},
	}
	pageRe  = regexp.MustCompile(`(?i)\b(?:page|seite|página|pagina)\s+([0-9ivxlcdm]+(?:[-–][0-9ivxlcdm]+)?)`)
	locRe   = regexp.MustCompile(`(?i)\b(?:location|loc\.|position|emplacement|posición|posizione)\s+(\d+)(?:[-–](\d+))?`)
	addedRe = regexp.MustCompile(`(?i)(?:added on|hinzugefügt am|ajouté le|añadido el|aggiunto il|adicionado:?)\s+(.+)$`)
	titleRe = regexp.MustCompile(`^(.*\S)\s*\(([^()]*)\)\s*$`)
)

var dateLayouts = []string{
	"2 January 2006 15:04:05", "January 2, 2006 3:04:05 PM", "January 2, 2006 15:04:05", "2 January 2006 15:04",
	"2. January 2006 15:04:05", "02 January 2006 15:04:05",
}

func parseDate(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, ", "); i > 0 && i < 12 && !strings.ContainsAny(s[:i], "0123456789") {
		s = s[i+2:] // drop the weekday
	}
	for _, l := range dateLayouts {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return ""
}

// splitTitle separates "Title (Author)" and turns "Tolle, Eckhart" into
// "Eckhart Tolle"; several authors come separated by ";".
func splitTitle(line string) (title, author string) {
	line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
	m := titleRe.FindStringSubmatch(line)
	if m == nil {
		return line, ""
	}
	var names []string
	for _, a := range strings.Split(m[2], ";") {
		a = strings.TrimSpace(a)
		if parts := strings.Split(a, ","); len(parts) == 2 && !strings.Contains(a, " and ") {
			a = strings.TrimSpace(parts[1]) + " " + strings.TrimSpace(parts[0])
		}
		if a != "" {
			names = append(names, a)
		}
	}
	return m[1], strings.Join(names, ", ")
}

func parseEntry(lines []string) (Clipping, bool) {
	for len(lines) > 0 && strings.TrimSpace(strings.TrimPrefix(lines[0], "\ufeff")) == "" {
		lines = lines[1:]
	}
	if len(lines) < 2 {
		return Clipping{}, false
	}
	c := Clipping{}
	c.Title, c.Author = splitTitle(lines[0])
	meta := strings.TrimSpace(lines[1])
	low := strings.ToLower(meta)
	for _, kw := range kindWords {
		if strings.Contains(low, kw.word) {
			c.Kind = kw.kind
			break
		}
	}
	if c.Kind == "" {
		return Clipping{}, false
	}
	if m := pageRe.FindStringSubmatch(meta); m != nil {
		c.Page = m[1]
	}
	if m := locRe.FindStringSubmatch(meta); m != nil {
		c.LocStart, _ = strconv.Atoi(m[1])
		c.LocEnd = c.LocStart
		if m[2] != "" {
			c.LocEnd, _ = strconv.Atoi(m[2])
			if c.LocEnd < c.LocStart { // "1012-15" style shorthand
				c.LocEnd = c.LocStart
			}
		}
	}
	if m := addedRe.FindStringSubmatch(meta); m != nil {
		c.Added = parseDate(m[1])
	}
	c.Text = strings.TrimSpace(strings.Join(lines[2:], "\n"))
	return c, true
}

// ParseClippings reads a whole clippings file.
func ParseClippings(r io.Reader) ([]Clipping, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	var out []Clipping
	var cur []string
	flush := func() {
		if c, ok := parseEntry(cur); ok {
			out = append(out, c)
		}
		cur = cur[:0]
	}
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.HasPrefix(strings.TrimSpace(line), "==========") {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return out, sc.Err()
}

// ClipEntry is a highlight ready to keep, with its note attached, or a note that
// had no highlight to belong to (Text holds the note then, and Note is "").
type ClipEntry struct {
	Text     string `json:"text"`
	Note     string `json:"note"`
	Page     string `json:"page"`
	LocStart int    `json:"loc_start"`
	LocEnd   int    `json:"loc_end"`
	Added    string `json:"added"`
	NoteOnly bool   `json:"note_only"`
}

// ClipBook is everything clipped from one book.
type ClipBook struct {
	Title   string      `json:"title"`
	Author  string      `json:"author"`
	Entries []ClipEntry `json:"entries"`
}

// Where returns "142" for a page, else "loc. 1201".
func (e ClipEntry) Where() string {
	if e.Page != "" {
		return e.Page
	}
	if e.LocStart > 0 {
		return "loc. " + strconv.Itoa(e.LocStart)
	}
	return ""
}

// clipLimit is what Kindle writes instead of the text once a publisher's
// clipping limit is reached.
func clipLimit(s string) bool {
	return strings.HasPrefix(s, "<") && strings.HasSuffix(s, ">") && strings.Contains(strings.ToLower(s), "clipping limit")
}

func squash(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

func overlaps(a, b ClipEntry) bool {
	if a.LocStart == 0 || b.LocStart == 0 {
		return true
	}
	return a.LocStart <= b.LocEnd && b.LocStart <= a.LocEnd
}

// GroupClippings collects clippings per book, drops bookmarks and repeated or
// superseded highlights (Kindle appends a new entry every time a highlight is
// changed), attaches each note to the highlight it was written on, and sorts
// by position in the book.
func GroupClippings(clips []Clipping) []ClipBook {
	type acc struct {
		book  ClipBook
		notes []Clipping
	}
	byKey := map[string]*acc{}
	var order []string
	for _, c := range clips {
		if c.Kind == clipBookmark || c.Text == "" || clipLimit(c.Text) {
			continue
		}
		key := squash(c.Title) + "\x00" + squash(c.Author)
		a := byKey[key]
		if a == nil {
			a = &acc{book: ClipBook{Title: c.Title, Author: c.Author}}
			byKey[key] = a
			order = append(order, key)
		}
		if c.Kind == clipNote {
			a.notes = append(a.notes, c)
			continue
		}
		e := ClipEntry{Text: c.Text, Page: c.Page, LocStart: c.LocStart, LocEnd: c.LocEnd, Added: c.Added}
		replaced := false
		for i, old := range a.book.Entries {
			if !overlaps(old, e) {
				continue
			}
			on, en := squash(old.Text), squash(e.Text)
			if on == en || strings.Contains(on, en) {
				replaced = true // already have it (or a longer version)
				break
			}
			if strings.Contains(en, on) || (old.LocStart == e.LocStart && old.LocStart > 0) {
				a.book.Entries[i] = e // the later, extended version wins
				replaced = true
				break
			}
		}
		if !replaced {
			a.book.Entries = append(a.book.Entries, e)
		}
	}
	out := make([]ClipBook, 0, len(order))
	for _, key := range order {
		a := byKey[key]
		for _, n := range a.notes {
			attached := false
			// A note sits at the last location of the highlight it belongs to.
			for i := range a.book.Entries {
				h := &a.book.Entries[i]
				if !h.NoteOnly && n.LocStart > 0 && n.LocStart >= h.LocStart && n.LocStart <= h.LocEnd {
					if h.Note != "" && squash(h.Note) != squash(n.Text) {
						h.Note += "\n" + n.Text
					} else {
						h.Note = n.Text
					}
					attached = true
					break
				}
			}
			if !attached {
				a.book.Entries = append(a.book.Entries, ClipEntry{Text: n.Text, Page: n.Page, LocStart: n.LocStart, LocEnd: n.LocEnd, Added: n.Added, NoteOnly: true})
			}
		}
		sort.SliceStable(a.book.Entries, func(i, j int) bool { return a.book.Entries[i].LocStart < a.book.Entries[j].LocStart })
		if len(a.book.Entries) > 0 {
			out = append(out, a.book)
		}
	}
	return out
}
