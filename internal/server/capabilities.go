package server

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

// The three routes behind `capabilities` (links.go): /api/v1/pocket/search,
// /day/{date} and /widget, for Pocket Search, Pocket Day and Pocket Hub. Each
// runs as the request's person: the books are the ones in the libraries they
// may open, and where they are in a book, what they read and what they
// finished is their own. The SDK's handlers check and cap what goes in and
// out. They read what /books?q=, /day/{date} and /home read; nothing is
// changed.

// bookURL opens a book's page in the app.
func bookURL(id int64) string { return "/book/" + itoa(id) }

// dots joins the parts of a line that are there: "Ivo Andrić · page 40 of 320".
func dots(parts ...string) string {
	out := parts[:0:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

// upperFirst starts a line with a capital letter.
func upperFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 || unicode.IsUpper(r) {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

// count is a number with its word: "1 book", "3 books".
func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// authorNames are a book's authors on one line, the first three by name.
func authorNames(b *store.Book) string {
	names := make([]string, 0, len(b.Authors))
	for _, a := range b.Authors {
		names = append(names, a.Name)
	}
	if len(names) > 3 {
		return strings.Join(names[:3], ", ") + fmt.Sprintf(" +%d", len(names)-3)
	}
	return strings.Join(names, ", ")
}

// seriesWords is the series with the book's number in it: "Saga #3".
func seriesWords(b *store.Book) string {
	if b.Series == "" || b.SeriesIndex == 0 {
		return b.Series
	}
	return b.Series + " #" + strconv.FormatFloat(math.Round(b.SeriesIndex*100)/100, 'f', -1, 64)
}

// progressWords is how far the person is in a book: "page 40 of 320" when the
// book has a page count (the page is worked out from the place, as reading
// time is), else "35%". Nothing read yet says nothing.
func progressWords(b *store.Book) string {
	if b.State == nil || b.State.Percentage <= 0 {
		return ""
	}
	pct := min(b.State.Percentage, 1)
	if b.Pages > 0 {
		return fmt.Sprintf("page %d of %d", max(1, int(math.Round(pct*float64(b.Pages)))), b.Pages)
	}
	return strconv.Itoa(max(1, int(math.Round(pct*100)))) + "%"
}

// standingWords says where the person stands with a book, as its card does:
// "read", "didn’t finish", "page 40 of 320", "want to read".
func standingWords(b *store.Book) string {
	st := b.State
	switch {
	case st == nil:
		return ""
	case st.Status == "read":
		return "read"
	case st.Status == "dnf":
		return "didn’t finish"
	case st.Percentage > 0:
		return progressWords(b)
	case st.Status == "want":
		return "want to read"
	case st.Status == "reading":
		return "reading"
	}
	return ""
}

// ---------- search ----------

// searchPool is how many matches are ranked before the best are kept.
const searchPool = 200

// hasWords reports whether every word starts a word of the texts, the way
// the search index matches them (capitals and accents aside).
func hasWords(words []string, texts ...string) bool {
	have := strings.Fields(fold(strings.Join(texts, " ")))
	for _, w := range words {
		found := false
		for _, h := range have {
			if strings.HasPrefix(h, w) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return len(words) > 0
}

// pocketSearch answers Pocket Search with the books GET /books?q= finds (by
// title, author, series, publisher, tag, description and ISBN) in the
// libraries the person browses, books they hid left out: a title that has
// the words first, then an author or a series, then the rest.
func (s *Server) pocketSearch(r *http.Request, q string, limit int) ([]core.SearchItem, error) {
	if store.FTSQuery(q) == "" {
		return nil, nil // no letter or digit: the list would be every book
	}
	ctx := r.Context()
	books, _, err := s.store.ListBooks(ctx, s.scopeQuery(ctx, userFrom(ctx), store.BookQuery{Q: q, Limit: searchPool}))
	if err != nil {
		return nil, err
	}
	words := strings.Fields(fold(q))
	out := make([]core.SearchItem, 0, len(books))
	for i := range books {
		b := &books[i]
		it := core.SearchItem{ID: itoa(b.ID), Title: b.Title, Kind: b.Kind, Date: b.AddedAt, URL: bookURL(b.ID), Score: 1}
		if b.State != nil && b.State.LastReadAt != "" {
			it.Date = b.State.LastReadAt
		}
		authors := authorNames(b)
		// where the words were found, when the line does not show it
		where := ""
		switch {
		case hasWords(words, b.Title, b.Subtitle):
			it.Score = 3
		case hasWords(words, b.Title, b.Subtitle, authors, b.Series):
			it.Score = 2
		case hasWords(words, b.Title, b.Subtitle, authors, b.Series, b.Publisher):
			where = b.Publisher
		case hasWords(words, b.Title, b.Subtitle, authors, b.Series, b.ISBN) && b.ISBN != "":
			where = "ISBN " + b.ISBN
		}
		it.Snippet = dots(authors, seriesWords(b), standingWords(b), where)
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ---------- day ----------

// dayAdded is how many of a day's new books are listed; the line counts them all.
const dayAdded = 12

// dayRead is one book on a day: the reading of it and whether it was finished.
type dayRead struct {
	id             int64
	seconds, pages int
	start, end     time.Time // of the reading; zero when the book was only marked finished
	sat, finished  bool
	book           *store.Book
}

// daySessions are the person's reading sessions of a day (what GET
// /day/{date} adds up). Without a time zone they are the ones the app files
// under that day; with one, the ones that started on that day there.
func (s *Server) daySessions(ctx context.Context, uid int64, date string, tz *time.Location) ([]store.ReadingSession, error) {
	if tz == nil {
		return s.store.Sessions(ctx, uid, store.SessionFilter{From: date, To: date})
	}
	// two zones can be two calendar days apart
	near, err := s.store.Sessions(ctx, uid, store.SessionFilter{From: store.AddDays(date, -2), To: store.AddDays(date, 2)})
	if err != nil {
		return nil, err
	}
	out := near[:0]
	for _, x := range near {
		day := x.Day
		if at, err := time.Parse(time.RFC3339Nano, x.StartedAt); err == nil {
			day = at.In(tz).Format("2006-01-02")
		}
		if day == date {
			out = append(out, x)
		}
	}
	return out, nil
}

// pocketDay answers Pocket Day with the day GET /day/{date} tells: one line
// for each book read that day with its minutes and pages, the books finished
// that day, and the books that came into the libraries the person browses. A
// book shows once. A book the person can no longer open is left out. The
// reading and the new books are placed in the caller's time zone when it
// gives one; the day a book was finished is the plain date the app keeps.
func (s *Server) pocketDay(r *http.Request, date string, tz *time.Location) (*core.Day, error) {
	ctx := r.Context()
	u := userFrom(ctx)
	loc := time.Local
	if tz != nil {
		loc = tz
	}
	sessions, err := s.daySessions(ctx, u.ID, date, tz)
	if err != nil {
		return nil, err
	}
	reads, err := s.store.Reads(ctx, u.ID, 0, date, date)
	if err != nil {
		return nil, err
	}
	var order []*dayRead
	byBook := map[int64]*dayRead{}
	of := func(id int64) *dayRead {
		d := byBook[id]
		if d == nil {
			d = &dayRead{id: id}
			byBook[id] = d
			order = append(order, d)
		}
		return d
	}
	for i := len(sessions) - 1; i >= 0; i-- { // the day's first reading first
		x := sessions[i]
		d := of(x.BookID)
		d.sat = true
		d.seconds += x.Seconds
		d.pages += x.Pages
		if at, err := time.Parse(time.RFC3339Nano, x.StartedAt); err == nil && (d.start.IsZero() || at.Before(d.start)) {
			d.start = at
		}
		if at, err := time.Parse(time.RFC3339Nano, x.EndedAt); err == nil && at.After(d.end) {
			d.end = at
		}
	}
	for _, rd := range reads {
		if rd.Status == "read" {
			of(rd.BookID).finished = true
		}
	}

	day := &core.Day{Date: date}
	libs := s.allowedLibraries(ctx, u)
	shown := map[int64]bool{}
	var sat, finished []*dayRead
	seconds, pages := 0, 0
	for _, d := range order {
		// the book as its page shows it: only in a library the person may open
		b, err := s.store.GetBook(ctx, u.ID, d.id, libs)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		d.book, shown[d.id] = b, true
		it := core.DayItem{ID: itoa(b.ID), Title: b.Title, Sub: authorNames(b), Kind: "read", URL: bookURL(b.ID),
			Minutes: math.Round(float64(d.seconds)/6) / 10, Qty: float64(d.pages)}
		if !d.start.IsZero() && !d.end.IsZero() {
			it.Time, it.End = d.start.In(loc).Format("15:04"), d.end.In(loc).Format("15:04")
		}
		if d.finished {
			it.Kind, it.Done = "finished", true
			finished = append(finished, d)
		}
		if d.sat {
			sat = append(sat, d)
			seconds, pages = seconds+d.seconds, pages+d.pages
		}
		day.Items = append(day.Items, it)
	}

	// the books that came in that day, the newest first
	from, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return nil, err
	}
	// added_at is UTC with a fraction; a bound without one sorts before every moment of its second
	stamp := func(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05") }
	added, addedTotal, err := s.store.ListBooks(ctx, s.scopeQuery(ctx, u, store.BookQuery{
		AddedSince: stamp(from), AddedUntil: stamp(from.AddDate(0, 0, 1)), Sort: "added", Limit: dayAdded}))
	if err != nil {
		return nil, err
	}
	for i := range added {
		b := &added[i]
		if shown[b.ID] {
			continue
		}
		it := core.DayItem{ID: itoa(b.ID), Title: b.Title, Sub: authorNames(b), Kind: "added", URL: bookURL(b.ID)}
		if at, err := time.Parse(time.RFC3339Nano, b.AddedAt); err == nil {
			it.Time = at.In(loc).Format("15:04")
		}
		day.Items = append(day.Items, it)
	}
	if len(day.Items) == 0 {
		return nil, nil
	}

	// "Read Saga for 40 min (25 pages) and finished it; 2 books added"
	var parts []string
	if n := len(sat); n > 0 {
		what := count(n, "book", "books")
		if n == 1 {
			what = sat[0].book.Title
		}
		line := fmt.Sprintf("read %s for %s", what, longDuration(seconds))
		if pages > 0 {
			line += " (" + count(pages, "page", "pages") + ")"
		}
		parts = append(parts, line)
	}
	switch n := len(finished); {
	case n == 1 && len(sat) == 1 && finished[0] == sat[0]:
		parts = append(parts, "finished it")
	case n == 1:
		parts = append(parts, "finished "+finished[0].book.Title)
	case n > 1:
		parts = append(parts, fmt.Sprintf("finished %d books", n))
	}
	line := strings.Join(parts, " and ")
	if addedTotal > 0 {
		if line != "" {
			line += "; "
		}
		line += count(addedTotal, "book", "books") + " added"
	}
	day.Summary = upperFirst(line)
	if seconds > 0 {
		day.Metrics = append(day.Metrics, core.Metric{Label: "Time", Value: longDuration(seconds)})
	}
	if pages > 0 {
		day.Metrics = append(day.Metrics, core.Metric{Label: "Pages", Value: strconv.Itoa(pages)})
	}
	return day, nil
}

// ---------- widget ----------

const (
	widgetRows    = 8 // what a widget shows
	widgetReading = 5 // of them, books in progress; the rest are the newest books
)

// addedWords says when a book came in: "added today", "added 3 days ago", "added 12 Nov".
func addedWords(at string, now time.Time) string {
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return "new"
	}
	t = t.In(now.Location())
	midnight := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC) }
	switch days := int(midnight(now).Sub(midnight(t)).Hours() / 24); {
	case days <= 0:
		return "added today"
	case days == 1:
		return "added yesterday"
	case days < 7:
		return fmt.Sprintf("added %d days ago", days)
	case t.Year() == now.Year():
		return "added " + t.Format("2 Jan")
	}
	return "added " + t.Format("2 Jan 2006")
}

// pocketWidget answers Pocket Hub with the top of Home in a few lines: the
// books being read, the one read last on top, each with where the person is,
// then the newest books. The numbers are Home's minutes read today and books
// finished this year, each of its goal when there is one. Only the newest few
// rows are read. Someone with no books and no reading gets nothing to show.
func (s *Server) pocketWidget(r *http.Request) (*core.WidgetData, error) {
	ctx := r.Context()
	u := userFrom(ctx)
	base := s.scopeQuery(ctx, u, store.BookQuery{})
	list := func(q store.BookQuery) ([]store.Book, int, error) {
		q.UserID, q.Libraries = base.UserID, base.Libraries
		return s.store.ListBooks(ctx, q)
	}
	reading, inProgress, err := list(store.BookQuery{Status: "reading", Sort: "last_read", Limit: widgetReading})
	if err != nil {
		return nil, err
	}
	added, _, err := list(store.BookQuery{Sort: "added", Limit: widgetRows})
	if err != nil {
		return nil, err
	}

	w := &core.WidgetData{}
	listed := map[int64]bool{}
	for i := range reading {
		b := &reading[i]
		listed[b.ID] = true
		w.Items = append(w.Items, core.WidgetItem{ID: itoa(b.ID), Title: b.Title, URL: bookURL(b.ID), Detail: firstNonEmpty(progressWords(b), authorNames(b))})
	}
	now := time.Now()
	for i := range added {
		b := &added[i]
		if listed[b.ID] || len(w.Items) >= widgetRows {
			continue
		}
		w.Items = append(w.Items, core.WidgetItem{ID: itoa(b.ID), Title: b.Title, URL: bookURL(b.ID), Detail: dots(addedWords(b.AddedAt, now), authorNames(b))})
	}
	if len(reading) > 0 {
		w.Line = "Reading " + reading[0].Title
		if at := progressWords(&reading[0]); at != "" {
			w.Line += ", " + at
		}
		if inProgress > 1 {
			w.Line += fmt.Sprintf(", and %d more", inProgress-1)
		}
	}

	// Home's strip (homeStats), without its streak: that one reads every day ever read
	st, _ := s.store.GetSettings(ctx, u.ID)
	var today, year int
	_ = s.store.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(seconds), 0) FROM reading_sessions WHERE user_id = ? AND day = ?`, u.ID, store.Today()).Scan(&today)
	_ = s.store.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM reads WHERE user_id = ? AND status = 'read' AND day >= ?`, u.ID, fmt.Sprintf("%d-01-01", now.Year())).Scan(&year)
	if minutes := today / 60; st.DailyGoal > 0 {
		w.Metrics = append(w.Metrics, core.Metric{Label: "Read today", Value: fmt.Sprintf("%d of %d min", minutes, st.DailyGoal)})
	} else if minutes > 0 {
		w.Metrics = append(w.Metrics, core.Metric{Label: "Read today", Value: strconv.Itoa(minutes) + " min"})
	}
	if st.YearlyGoal > 0 {
		w.Metrics = append(w.Metrics, core.Metric{Label: "Finished this year", Value: fmt.Sprintf("%d of %d", year, st.YearlyGoal)})
	} else if year > 0 {
		w.Metrics = append(w.Metrics, core.Metric{Label: "Finished this year", Value: strconv.Itoa(year)})
	}
	if len(w.Items) == 0 && len(w.Metrics) == 0 {
		return nil, nil
	}
	return w, nil
}
