package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

// capFixture is devFixture's library (Ana the admin, Bob a member, one shared
// library with "Na Drini ćuprija") with a comic and a book for Bob to read,
// and a library only Ana may open.
type capFixture struct {
	*devFixture
	lib, private       int64
	saga, life, secret int64

	mu       sync.Mutex
	manifest map[string]any
}

func newCapFixture(t *testing.T) *capFixture {
	t.Helper()
	f := &capFixture{devFixture: newDevFixture(t)}
	ctx := context.Background()
	b, err := f.st.GetBookAny(ctx, f.book)
	if err != nil {
		t.Fatal(err)
	}
	f.lib = b.LibraryID
	f.saga = f.add(f.lib, store.BookMeta{Title: "Saga", Subtitle: "Volume One", Authors: []string{"Brian K. Vaughan", "Fiona Staples"}, Publisher: "Image Comics", ISBN: "9781607066019", Kind: "comic", Pages: 320})
	f.life = f.add(f.lib, store.BookMeta{Title: "Andrić: a life", Authors: []string{"Someone Else"}})
	private := &store.Library{Name: "Ana's", Kind: "books", Paths: []string{f.dir + "/private"}, Access: "people", People: []int64{f.ana.ID}}
	if err := f.st.CreateLibrary(ctx, private); err != nil {
		t.Fatal(err)
	}
	f.private = private.ID
	f.secret = f.add(f.private, store.BookMeta{Title: "Secret diary", Authors: []string{"Ana"}})
	return f
}

// add puts a book into a library, as a scan does.
func (f *capFixture) add(lib int64, m store.BookMeta) int64 {
	f.t.Helper()
	id, err := f.st.CreateBook(context.Background(), lib, "/"+strings.ToLower(m.Title), m)
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}

// addedOn moves the moment a book came in.
func (f *capFixture) addedOn(id int64, at string) {
	f.t.Helper()
	if _, err := f.st.DB.Exec(`UPDATE books SET added_at = ? WHERE id = ?`, at, id); err != nil {
		f.t.Fatal(err)
	}
}

// withCore puts Library behind a fake Pocket Core that remembers the manifest
// and vouches for link tokens pcx_<ana|bob>_<search|day|widget|capture|
// reading|library|import|export> from Pocket Hub.
func (f *capFixture) withCore() {
	f.t.Helper()
	scopes := map[string]string{"search": core.ScopeSearch, "day": core.ScopeDay, "widget": core.ScopeWidget, "capture": core.ScopeCapture,
		"reading": "reading:read", "library": "library:read", "import": "library:import", "export": core.ScopeMoveExport}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/register":
			var m map[string]any
			_ = json.NewDecoder(r.Body).Decode(&m)
			f.mu.Lock()
			f.manifest = m
			f.mu.Unlock()
			io.WriteString(w, `{}`)
		case "/api/v1/auth/introspect":
			var in struct{ Token string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			parts := strings.Split(in.Token, "_")
			people := map[string]*store.User{"ana": f.ana, "bob": f.bob}
			if len(parts) != 3 || parts[0] != "pcx" || people[parts[1]] == nil || scopes[parts[2]] == "" {
				io.WriteString(w, `{"active":false}`)
				return
			}
			p := people[parts[1]]
			_ = json.NewEncoder(w).Encode(map[string]any{"active": true, "kind": "delegated", "scope": scopes[parts[2]], "audience": "library",
				"user": map[string]any{"id": p.ID, "email": p.Email, "name": p.Name, "role": "member"}, "app": map[string]any{"id": 9, "slug": "hub", "name": "Pocket Hub"}})
		default:
			http.NotFound(w, r)
		}
	}))
	f.t.Cleanup(fake.Close)
	f.s.core = core.New(fake.URL, core.Options{DB: f.st.DB, Token: "synthetic-application-token"})
}

func (f *capFixture) search(tok, q string) []core.SearchItem {
	f.t.Helper()
	var res core.SearchResults
	f.json(tok, "GET", "/api/v1/pocket/search?"+q, nil, http.StatusOK, &res)
	if res.Items == nil {
		f.t.Fatalf("search %s: no items list", q)
	}
	return res.Items
}

func (f *capFixture) day(tok, date, tz string) core.Day {
	f.t.Helper()
	path := "/api/v1/pocket/day/" + date
	if tz != "" {
		path += "?tz=" + tz
	}
	var d core.Day
	f.json(tok, "GET", path, nil, http.StatusOK, &d)
	if d.Date != date || d.Items == nil {
		f.t.Fatalf("day %s = %+v", date, d)
	}
	return d
}

func (f *capFixture) widget(tok string) core.WidgetData {
	f.t.Helper()
	var w core.WidgetData
	f.json(tok, "GET", "/api/v1/pocket/widget", nil, http.StatusOK, &w)
	if w.Items == nil {
		f.t.Fatalf("widget: no items list")
	}
	return w
}

// reads records reading of a book as the web reader reports it: the place
// now, and the seconds and pages since the last report.
func (f *capFixture) reads(u *store.User, book int64, at time.Time, pct float64, seconds, pages int) {
	f.t.Helper()
	ctx := context.Background()
	if _, _, _, err := f.st.SetProgress(ctx, u.ID, book, store.Progress{Percentage: pct, At: at}); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.st.RecordSession(ctx, u.ID, store.SessionReport{BookID: book, Device: "Web reader", Source: "web", At: at, Pct: pct, PrevPct: -1, Seconds: seconds, Pages: pages}); err != nil {
		f.t.Fatal(err)
	}
}

func searchTitles(items []core.SearchItem) string {
	var t []string
	for _, it := range items {
		t = append(t, it.Title)
	}
	return strings.Join(t, " | ")
}

func dayItem(d core.Day, title string) core.DayItem {
	for _, it := range d.Items {
		if it.Title == title {
			return it
		}
	}
	return core.DayItem{}
}

// Pocket Search finds the books a person may open by title, author, series
// and publisher, the title first, each with where that person is in it.
func TestPocketSearch(t *testing.T) {
	f := newCapFixture(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	f.reads(f.bob, f.saga, at, 0.125, 600, 5)

	got := f.search(f.bobTok, "q=saga")
	if len(got) != 1 {
		t.Fatalf("saga = %s", searchTitles(got))
	}
	if it := got[0]; it.ID != itoa(f.saga) || it.Kind != "comic" || it.URL != fmt.Sprintf("/book/%d", f.saga) ||
		it.Snippet != "Brian K. Vaughan, Fiona Staples · page 40 of 320" || it.Date != at.Format(time.RFC3339Nano) {
		t.Errorf("Saga = %+v", it)
	}
	// the place is Bob's: Ana has not opened it, so the day it came in stands
	if got := f.search(f.anaTok, "q=saga"); len(got) != 1 || got[0].Snippet != "Brian K. Vaughan, Fiona Staples" || got[0].Date == "" || got[0].Date == at.Format(time.RFC3339Nano) {
		t.Errorf("Ana's Saga = %+v", got)
	}
	// a title that has the words comes before an author who has them; accents and capitals aside
	for _, q := range []string{"q=andric", "q=ANDRIĆ"} {
		got = f.search(f.bobTok, q)
		if searchTitles(got) != "Andrić: a life | Na Drini ćuprija" || got[0].Kind != "book" || got[0].Snippet != "Someone Else" || got[1].Snippet != "Ivo Andrić · Bosnian trilogy #1" {
			t.Errorf("%s = %+v", q, got)
		}
	}
	// by series, by subtitle, by publisher and by ISBN: the line says where the words were found
	if got := f.search(f.bobTok, "q=bosnian+tril"); len(got) != 1 || got[0].ID != itoa(f.book) {
		t.Errorf("series = %+v", got)
	}
	if got := f.search(f.bobTok, "q=volume+one"); len(got) != 1 || got[0].Title != "Saga" {
		t.Errorf("subtitle = %+v", got)
	}
	if got := f.search(f.bobTok, "q=image+comics"); len(got) != 1 || got[0].Snippet != "Brian K. Vaughan, Fiona Staples · page 40 of 320 · Image Comics" {
		t.Errorf("publisher = %+v", got)
	}
	if got := f.search(f.bobTok, "q=9781607066019"); len(got) != 1 || !strings.HasSuffix(got[0].Snippet, " · ISBN 9781607066019") {
		t.Errorf("isbn = %+v", got)
	}
	// where a book stands, in the card's words
	if _, _, err := f.st.SetStatus(ctx, f.bob.ID, f.book, "read", "2026-10-04"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.st.SetStatus(ctx, f.bob.ID, f.life, "want", ""); err != nil {
		t.Fatal(err)
	}
	if got := f.search(f.bobTok, "q=andric"); len(got) != 2 || got[0].Snippet != "Someone Else · want to read" || got[1].Snippet != "Ivo Andrić · Bosnian trilogy #1 · read" {
		t.Errorf("standing = %+v", got)
	}
	// the limit; no words, and words without a letter or a digit, find nothing
	if got := f.search(f.bobTok, "q=andric&limit=1"); len(got) != 1 || got[0].Title != "Andrić: a life" {
		t.Errorf("limit 1 = %s", searchTitles(got))
	}
	for _, q := range []string{"q=", "q=+", "q=%3F%3F", "q=*", "q=%22%22"} {
		if got := f.search(f.bobTok, q); len(got) != 0 {
			t.Errorf("%s = %s", q, searchTitles(got))
		}
	}
	// a library only Ana may open is hers alone
	for _, q := range []string{"q=secret", "q=diary", "q=ana", "q=s"} {
		for _, it := range f.search(f.bobTok, q) {
			if it.ID == itoa(f.secret) {
				t.Errorf("Bob found Ana's book with %s", q)
			}
		}
	}
	if got := f.search(f.anaTok, "q=diary"); len(got) != 1 || got[0].ID != itoa(f.secret) || got[0].Snippet != "Ana" {
		t.Errorf("Ana's own = %+v", got)
	}
	// as GET /books?q=: a book the person hid, and a library they left out of browsing, are not found
	hide := true
	if _, err := f.st.UpdateUserBook(ctx, f.bob.ID, f.life, nil, nil, &hide); err != nil {
		t.Fatal(err)
	}
	if got := f.search(f.bobTok, "q=andric"); searchTitles(got) != "Na Drini ćuprija" {
		t.Errorf("after hiding = %s", searchTitles(got))
	}
	if got := f.search(f.anaTok, "q=andric"); len(got) != 2 {
		t.Errorf("Bob's hiding reached Ana: %s", searchTitles(got))
	}
	f.json(f.bobTok, "PUT", "/api/v1/settings", map[string]any{"hidden_libraries": itoa(f.lib)}, http.StatusOK, nil)
	var page struct{ Total int }
	f.json(f.bobTok, "GET", "/api/v1/books?q=saga", nil, http.StatusOK, &page)
	if got := f.search(f.bobTok, "q=saga"); len(got) != 0 || page.Total != 0 {
		t.Errorf("hidden library: search %s, /books %d", searchTitles(got), page.Total)
	}
	if code, _, _ := f.do("", "GET", "/api/v1/pocket/search?q=saga", nil); code != http.StatusUnauthorized {
		t.Errorf("nobody = %d, want 401", code)
	}
}

// Pocket Day gets one line for each book read that day with its minutes and
// pages, the books finished, and the books that came in.
func TestPocketDay(t *testing.T) {
	f := newCapFixture(t)
	ctx := context.Background()
	const day, next = "2026-10-05", "2026-10-06"
	noon := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

	if d := f.day(f.bobTok, day, "UTC"); len(d.Items) != 0 || d.Summary != "" || len(d.Metrics) != 0 {
		t.Fatalf("an empty day = %+v", d)
	}
	// ten minutes and five pages from 12:00, then half an hour and twenty pages more
	f.reads(f.bob, f.saga, noon.Add(10*time.Minute), 0.10, 600, 5)
	f.reads(f.bob, f.saga, noon.Add(40*time.Minute), 0.20, 1800, 20)
	d := f.day(f.bobTok, day, "UTC")
	if d.Summary != "Read Saga for 40 min (25 pages)" || len(d.Items) != 1 {
		t.Fatalf("day = %q %+v", d.Summary, d.Items)
	}
	if it := d.Items[0]; it.ID != itoa(f.saga) || it.Title != "Saga" || it.Sub != "Brian K. Vaughan, Fiona Staples" || it.Kind != "read" || it.Minutes != 40 || it.Qty != 25 ||
		it.Time != "12:00" || it.End != "12:40" || it.Done || it.URL != fmt.Sprintf("/book/%d", f.saga) || it.Image != "" {
		t.Errorf("Saga = %+v", it)
	}
	if len(d.Metrics) != 2 || d.Metrics[0] != (core.Metric{Label: "Time", Value: "40 min"}) || d.Metrics[1] != (core.Metric{Label: "Pages", Value: "25"}) {
		t.Errorf("metrics = %+v", d.Metrics)
	}

	// finishing it that day
	if _, _, err := f.st.SetStatus(ctx, f.bob.ID, f.saga, "read", day); err != nil {
		t.Fatal(err)
	}
	d = f.day(f.bobTok, day, "UTC")
	if it := d.Items[0]; d.Summary != "Read Saga for 40 min (25 pages) and finished it" || len(d.Items) != 1 || it.Kind != "finished" || !it.Done || it.Minutes != 40 {
		t.Fatalf("finished = %q %+v", d.Summary, d.Items)
	}
	// a second book read, one marked finished without opening it, one given up (not a day's line, as in /day)
	f.reads(f.bob, f.book, noon.Add(8*time.Hour), 0.5, 1200, 0)
	if _, _, err := f.st.SetStatus(ctx, f.bob.ID, f.life, "read", day); err != nil {
		t.Fatal(err)
	}
	gone := f.add(f.lib, store.BookMeta{Title: "Dull book"})
	if _, _, err := f.st.SetStatus(ctx, f.bob.ID, gone, "dnf", day); err != nil {
		t.Fatal(err)
	}
	// and two books came in: one into the shared library, one into Ana's own
	f.addedOn(gone, "2026-10-05T09:30:00.000Z")
	f.addedOn(f.secret, "2026-10-05T10:00:00.000Z")
	d = f.day(f.bobTok, day, "UTC")
	if d.Summary != "Read 2 books for 1 h (25 pages) and finished 2 books; 1 book added" || len(d.Items) != 4 {
		t.Fatalf("full day = %q %+v", d.Summary, d.Items)
	}
	if it := dayItem(d, "Na Drini ćuprija"); it.Kind != "read" || it.Minutes != 20 || it.Qty != 0 || it.Time != "19:40" || it.End != "20:00" || it.Sub != "Ivo Andrić" {
		t.Errorf("second book = %+v", it)
	}
	if it := dayItem(d, "Andrić: a life"); it.Kind != "finished" || !it.Done || it.Minutes != 0 || it.Time != "" {
		t.Errorf("only finished = %+v", it)
	}
	if it := dayItem(d, "Dull book"); it.ID != itoa(gone) || it.Kind != "added" || it.Time != "09:30" || it.Done || it.URL != fmt.Sprintf("/book/%d", gone) {
		t.Errorf("added = %+v", it)
	}
	if dayItem(d, "Secret diary").Title != "" || strings.Contains(d.Summary, "Secret") {
		t.Errorf("Bob's day has Ana's book: %+v", d)
	}
	// what the app's own /day tells is as it was
	var old struct {
		Seconds, Pages, Finished int
		Books                    []map[string]any
	}
	f.json(f.bobTok, "GET", "/api/v1/day/"+noon.Local().Format("2006-01-02"), nil, http.StatusOK, &old)
	if old.Seconds != 3600 && old.Seconds != 2400 { // the evening's reading may be the next day where the machine is
		t.Errorf("/day = %+v", old)
	}

	// Ana read nothing: her day has the two new books and none of Bob's reading
	a := f.day(f.anaTok, day, "UTC")
	if a.Summary != "2 books added" || len(a.Items) != 2 || a.Items[0].Title != "Secret diary" || a.Items[0].Kind != "added" || a.Items[1].Title != "Dull book" || len(a.Metrics) != 0 {
		t.Errorf("Ana's day = %q %+v", a.Summary, a.Items)
	}

	// the day is the caller's: far east of UTC the noon reading is the next morning's,
	// the evening's too; the day a book was finished is the plain date the app keeps
	east := f.day(f.bobTok, next, "Pacific/Kiritimati")
	if east.Summary != "Read 2 books for 1 h (25 pages)" || len(east.Items) != 2 || dayItem(east, "Saga").Time != "02:00" || dayItem(east, "Saga").Kind != "read" || dayItem(east, "Na Drini ćuprija").Time != "09:40" {
		t.Errorf("east, next day = %q %+v", east.Summary, east.Items)
	}
	// (the book that came in at 09:30 UTC did so late that evening there)
	if east := f.day(f.bobTok, day, "Pacific/Kiritimati"); east.Summary != "Finished 2 books; 1 book added" || len(east.Items) != 3 || dayItem(east, "Saga").Minutes != 0 || dayItem(east, "Dull book").Time != "23:30" {
		t.Errorf("east, the day = %q %+v", east.Summary, east.Items)
	}
	// without a time zone: the days the app files the reading under
	local := f.day(f.bobTok, noon.Local().Format("2006-01-02"), "")
	if it := dayItem(local, "Saga"); it.Minutes != 40 || it.Time != noon.Local().Format("15:04") {
		t.Errorf("the app's own day = %+v", local)
	}

	// a book in a library Bob may no longer open leaves his day
	lib, err := f.st.GetLibrary(ctx, f.lib)
	if err != nil {
		t.Fatal(err)
	}
	lib.Access, lib.People = "people", []int64{f.ana.ID}
	if err := f.st.UpdateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	if d := f.day(f.bobTok, day, "UTC"); len(d.Items) != 0 || d.Summary != "" {
		t.Errorf("after losing the library = %q %+v", d.Summary, d.Items)
	}

	for _, bad := range []string{"2026-13-01", "today", "20261005"} {
		if code, _, _ := f.do(f.bobTok, "GET", "/api/v1/pocket/day/"+bad, nil); code != http.StatusBadRequest {
			t.Errorf("day %s = %d, want 400", bad, code)
		}
	}
}

// Many books scanned in on one day: the line counts them all, the list keeps
// the newest few.
func TestPocketDayManyAdded(t *testing.T) {
	f := newCapFixture(t)
	for i := 0; i < 30; i++ {
		f.addedOn(f.add(f.lib, store.BookMeta{Title: fmt.Sprintf("Scanned %02d", i)}), fmt.Sprintf("2026-10-05T08:%02d:00.000Z", i))
	}
	d := f.day(f.bobTok, "2026-10-05", "UTC")
	if d.Summary != "30 books added" || len(d.Items) != dayAdded || d.Items[0].Title != "Scanned 29" || d.Items[0].Time != "08:29" {
		t.Errorf("day = %q, %d items, first %+v", d.Summary, len(d.Items), d.Items[0])
	}
	// the day's edges: the first and the last moment belong to it, midnight after does not
	f.addedOn(f.saga, "2026-10-05T00:00:00.000Z")
	f.addedOn(f.life, "2026-10-05T23:59:59.999Z")
	f.addedOn(f.book, "2026-10-06T00:00:00.000Z")
	if d := f.day(f.bobTok, "2026-10-05", "UTC"); d.Summary != "32 books added" {
		t.Errorf("edges = %q", d.Summary)
	}
	if d := f.day(f.bobTok, "2026-10-06", "UTC"); d.Summary != "1 book added" || d.Items[0].Title != "Na Drini ćuprija" {
		t.Errorf("the next day = %q %+v", d.Summary, d.Items)
	}
}

// Pocket Hub's widget: the books in progress, the one read last first, then
// the newest books.
func TestPocketWidget(t *testing.T) {
	f := newCapFixture(t)
	ctx := context.Background()
	now := time.Now()
	f.addedOn(f.life, now.AddDate(0, 0, -3).UTC().Format("2006-01-02T15:04:05.000Z"))
	f.addedOn(f.book, "2020-03-09T10:00:00.000Z")

	// nothing in progress: the newest books, no line, no numbers
	w := f.widget(f.bobTok)
	if w.Line != "" || len(w.Metrics) != 0 || len(w.Items) != 3 || w.URL != "" {
		t.Fatalf("nothing read = %+v", w)
	}
	if it := w.Items[0]; it.ID != itoa(f.saga) || it.Title != "Saga" || it.Detail != "added today · Brian K. Vaughan, Fiona Staples" || it.URL != fmt.Sprintf("/book/%d", f.saga) {
		t.Errorf("newest = %+v", it)
	}
	old := time.Date(2020, 3, 9, 10, 0, 0, 0, time.UTC).Local().Format("2 Jan 2006") // "9 Mar 2020" in most places
	if w.Items[1].Detail != "added 3 days ago · Someone Else" || w.Items[2].Detail != "added "+old+" · Ivo Andrić" {
		t.Errorf("added = %+v", w.Items)
	}

	// a comic with pages and a book without: the one read last leads
	f.reads(f.bob, f.saga, now.Add(-2*time.Hour), 0.125, 1500, 12)
	w = f.widget(f.bobTok)
	if w.Line != "Reading Saga, page 40 of 320" || len(w.Items) != 3 || w.Items[0].Title != "Saga" || w.Items[0].Detail != "page 40 of 320" || w.Items[1].Title != "Andrić: a life" {
		t.Fatalf("one in progress = %+v", w)
	}
	f.reads(f.bob, f.book, now.Add(-time.Hour), 0.349, 0, 0)
	w = f.widget(f.bobTok)
	if w.Line != "Reading Na Drini ćuprija, 35%, and 1 more" || len(w.Items) != 3 || w.Items[0].Detail != "35%" || w.Items[1].Detail != "page 40 of 320" || w.Items[2].Detail != "added 3 days ago · Someone Else" {
		t.Fatalf("two in progress = %+v", w)
	}
	// marked as reading, not opened yet: the author stands in for the place
	if _, _, err := f.st.SetStatus(ctx, f.bob.ID, f.life, "reading", ""); err != nil {
		t.Fatal(err)
	}
	if w = f.widget(f.bobTok); w.Line != "Reading Na Drini ćuprija, 35%, and 2 more" || w.Items[2].Detail != "Someone Else" {
		t.Errorf("three in progress = %+v", w)
	}

	// the numbers are Home's: minutes today and books finished this year, of the goals when set
	today := 0
	if now.Add(-2*time.Hour).Format("2006-01-02") == store.Today() {
		today = 25
	}
	wantToday := func(goal string) bool {
		if today == 0 && goal == "" {
			return len(w.Metrics) == 0
		}
		return len(w.Metrics) > 0 && w.Metrics[0] == core.Metric{Label: "Read today", Value: fmt.Sprintf("%d%s min", today, goal)}
	}
	if !wantToday("") {
		t.Errorf("today = %+v", w.Metrics)
	}
	if _, _, err := f.st.SetStatus(ctx, f.bob.ID, f.saga, "read", ""); err != nil {
		t.Fatal(err)
	}
	w = f.widget(f.bobTok)
	if m := w.Metrics[len(w.Metrics)-1]; m != (core.Metric{Label: "Finished this year", Value: "1"}) || w.Items[1].Title != "Andrić: a life" || w.Items[2].Detail != "added today · Brian K. Vaughan, Fiona Staples" {
		t.Errorf("after finishing = %+v", w)
	}
	f.json(f.bobTok, "PUT", "/api/v1/settings", map[string]any{"daily_goal": 30, "yearly_goal": 12}, http.StatusOK, nil)
	w = f.widget(f.bobTok)
	if len(w.Metrics) != 2 || !wantToday(" of 30") || w.Metrics[1] != (core.Metric{Label: "Finished this year", Value: "1 of 12"}) {
		t.Errorf("with goals = %+v", w.Metrics)
	}

	// a big library: five in progress at most, eight rows in all, each book once
	for i := 0; i < 12; i++ {
		id := f.add(f.lib, store.BookMeta{Title: fmt.Sprintf("More %02d", i)})
		if i < 6 {
			f.reads(f.bob, id, now.Add(time.Duration(i-30)*time.Minute), 0.5, 0, 0)
		}
	}
	w = f.widget(f.bobTok)
	seen := map[string]bool{}
	for _, it := range w.Items {
		if seen[it.ID] {
			t.Errorf("%s shows twice", it.Title)
		}
		seen[it.ID] = true
	}
	if len(w.Items) != widgetRows || w.Line != "Reading More 05, 50%, and 7 more" || w.Items[4].Title != "More 01" || !strings.HasPrefix(w.Items[5].Detail, "added today") {
		t.Errorf("big library = %q %+v", w.Line, w.Items)
	}

	// Ana sees the books she may open and nothing of Bob's reading
	a := f.widget(f.anaTok)
	if a.Line != "" || len(a.Metrics) != 0 || len(a.Items) != widgetRows {
		t.Errorf("Ana's widget = %+v", a)
	}
	for _, it := range a.Items {
		if !strings.HasPrefix(it.Detail, "added ") {
			t.Errorf("Ana's row = %+v", it)
		}
	}
	for _, it := range w.Items {
		if it.ID == itoa(f.secret) {
			t.Errorf("Bob's widget has Ana's book")
		}
	}
	// someone who may open no library has nothing to show
	lib, err := f.st.GetLibrary(ctx, f.lib)
	if err != nil {
		t.Fatal(err)
	}
	lib.Access, lib.People = "people", []int64{f.ana.ID}
	if err := f.st.UpdateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	f.json(f.bobTok, "PUT", "/api/v1/settings", map[string]any{"daily_goal": 0, "yearly_goal": 0}, http.StatusOK, nil)
	if _, err := f.st.DB.Exec(`DELETE FROM reads WHERE user_id = ?`, f.bob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.DB.Exec(`DELETE FROM reading_sessions WHERE user_id = ?`, f.bob.ID); err != nil {
		t.Fatal(err)
	}
	if w := f.widget(f.bobTok); w.Line != "" || len(w.Items) != 0 || len(w.Metrics) != 0 {
		t.Errorf("no library = %+v", w)
	}
}

// A pcx_ link token with a pocket:* scope reaches its one route and nothing
// of the rest of the app; the links Library had before do not open the new routes.
func TestPocketLinkScopes(t *testing.T) {
	f := newCapFixture(t)
	f.withCore()
	const day = "2026-10-05"
	f.reads(f.bob, f.saga, time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC), 0.125, 600, 5)
	f.reads(f.ana, f.secret, time.Date(2026, 10, 5, 13, 0, 0, 0, time.UTC), 0.5, 600, 5)
	type call struct{ m, p string }
	routes := map[string]call{
		"search": {"GET", "/api/v1/pocket/search?q=s"},
		"day":    {"GET", "/api/v1/pocket/day/" + day + "?tz=UTC"},
		"widget": {"GET", "/api/v1/pocket/widget"},
	}
	for scope := range routes {
		tok := "pcx_bob_" + scope
		for name, c := range routes {
			code, body, _ := f.do(tok, c.m, c.p, nil)
			switch {
			case name == scope && (code != http.StatusOK || !strings.Contains(string(body), "Saga") || strings.Contains(string(body), "Secret diary")):
				t.Errorf("pocket:%s %s %s = %d %s", scope, c.m, c.p, code, body)
			case name != scope && (code != http.StatusForbidden || !strings.Contains(string(body), "link does not cover")):
				t.Errorf("pocket:%s on %s %s = %d %s, want 403", scope, c.m, c.p, code, body)
			}
		}
		var me store.User
		f.json(tok, "GET", "/api/v1/auth/me", nil, http.StatusOK, &me)
		if me.Email != f.bob.Email {
			t.Errorf("%s acts for %s", tok, me.Email)
		}
		// nothing else in the app
		for _, c := range []call{
			{"GET", "/api/v1/books"}, {"GET", "/api/v1/books?q=saga"}, {"GET", fmt.Sprintf("/api/v1/books/%d", f.saga)}, {"GET", "/api/v1/search?q=saga"}, {"GET", "/api/v1/home"},
			{"GET", "/api/v1/day/" + day}, {"GET", "/api/v1/stats"}, {"GET", "/api/v1/dashboard"}, {"GET", "/api/v1/reading/current"}, {"GET", "/api/v1/sessions"},
			{"GET", "/api/v1/settings"}, {"GET", "/api/v1/export.json"}, {"GET", "/api/v1/libraries"}, {"GET", fmt.Sprintf("/api/v1/files/%d/content", f.file)},
			{"PUT", fmt.Sprintf("/api/v1/books/%d/state", f.saga)}, {"POST", fmt.Sprintf("/api/v1/books/%d/progress", f.saga)}, {"POST", "/api/v1/shelves"},
			{"GET", fmt.Sprintf("/api/v1/books/%d/cover", f.saga)},
		} {
			if code, body, _ := f.do(tok, c.m, c.p, map[string]any{}); code != http.StatusForbidden {
				t.Errorf("%s %s %s = %d %s, want 403", tok, c.m, c.p, code, body)
			}
		}
		// and its own route in no other way (the router has no such route to refuse)
		for _, c := range []call{
			{"POST", "/api/v1/pocket/search?q=s"}, {"POST", "/api/v1/pocket/widget"}, {"DELETE", "/api/v1/pocket/day/" + day}, {"GET", "/api/v1/pocket/day/" + day + "/more"},
			{"POST", "/api/v1/pocket/capture"}, {"GET", "/api/v1/pocket/capture"},
		} {
			if code, body, _ := f.do(tok, c.m, c.p, map[string]any{}); code != http.StatusForbidden && code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s %s = %d %s", tok, c.m, c.p, code, body)
			}
		}
	}
	// each link acts for its own person
	if code, body, _ := f.do("pcx_ana_search", "GET", "/api/v1/pocket/search?q=diary", nil); code != http.StatusOK || !strings.Contains(string(body), "Secret diary") {
		t.Errorf("Ana's link = %d %s", code, body)
	}
	if code, body, _ := f.do("pcx_bob_search", "GET", "/api/v1/pocket/search?q=diary", nil); code != http.StatusOK || strings.Contains(string(body), "Secret diary") {
		t.Errorf("Bob's link = %d %s", code, body)
	}
	// Library keeps nothing from a quick line: that scope opens nothing, and there is no such route
	for _, c := range routes {
		if code, _, _ := f.do("pcx_bob_capture", c.m, c.p, nil); code != http.StatusForbidden {
			t.Errorf("pocket:capture %s %s = %d, want 403", c.m, c.p, code)
		}
	}
	if code, _, _ := f.do("pcx_bob_capture", "GET", "/api/v1/books", nil); code != http.StatusForbidden {
		t.Errorf("pocket:capture on /books = %d, want 403", code)
	}
	for _, tok := range []string{"pcx_bob_capture", f.bobTok} {
		if code, _, _ := f.do(tok, "POST", "/api/v1/pocket/capture", map[string]any{"request_id": "hub-0000000000000001", "text": "Dune"}); code != http.StatusNotFound {
			t.Errorf("%s capture = %d, want 404", tok, code)
		}
	}
	// the links Library had before do not open the new routes, and still open their own
	for _, old := range []string{"reading", "library", "import", "export"} {
		for _, c := range routes {
			if code, _, _ := f.do("pcx_bob_"+old, c.m, c.p, nil); code != http.StatusForbidden {
				t.Errorf("%s %s %s = %d, want 403", old, c.m, c.p, code)
			}
		}
	}
	if code, _, _ := f.do("pcx_bob_reading", "GET", "/api/v1/day/"+day, nil); code != http.StatusOK {
		t.Errorf("reading:read on /day = %d", code)
	}
	if code, _, _ := f.do("pcx_bob_library", "GET", "/api/v1/books?q=saga", nil); code != http.StatusOK {
		t.Errorf("library:read on /books = %d", code)
	}
	// covers stay open to every link that had them (the book here has none: 404, not 403)
	for _, old := range []string{"reading", "library", "import", "export"} {
		if code, _, _ := f.do("pcx_bob_"+old, "GET", fmt.Sprintf("/api/v1/books/%d/cover", f.saga), nil); code != http.StatusNotFound {
			t.Errorf("%s on a cover = %d, want 404", old, code)
		}
	}
	// a token Core does not vouch for
	if code, _, _ := f.do("pcx_bob_revoked", "GET", "/api/v1/pocket/widget", nil); code != http.StatusUnauthorized {
		t.Errorf("unknown link token = %d, want 401", code)
	}
}

// The manifest tells Pocket Core the shelf Hub puts the app on and what
// Search, Day and Hub can ask it.
func TestManifestCapabilities(t *testing.T) {
	f := newCapFixture(t)
	f.withCore()
	if err := f.s.registerWithCore(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	m := f.manifest
	f.mu.Unlock()
	if m["slug"] != "library" || m["category"] != "Media" {
		t.Fatalf("manifest: %v %v", m["slug"], m["category"])
	}
	search, _ := m["search"].(map[string]any)
	day, _ := m["day"].(map[string]any)
	widget, _ := m["widget"].(map[string]any)
	if search == nil || day == nil || widget == nil {
		t.Fatalf("capabilities: %v %v %v", search, day, widget)
	}
	if _, has := m["capture"]; has {
		t.Errorf("capture declared: %v", m["capture"])
	}
	if fmt.Sprint(search["bangs"]) != "[lib library ebook]" || fmt.Sprint(search["kinds"]) != "[book comic manga]" || search["what"] == "" {
		t.Errorf("search: %v", search)
	}
	if day["label"] != "Library" || day["one"] != "book" || day["many"] != "books" || day["unit"] != "min" || day["what"] == "" {
		t.Errorf("day: %v", day)
	}
	if widget["title"] != "Continue reading" || widget["what"] == "" {
		t.Errorf("widget: %v", widget)
	}
	offered := map[string]bool{}
	for _, o := range m["offers"].([]any) {
		offered[o.(map[string]any)["scope"].(string)] = true
	}
	for _, sc := range []string{"reading:read", "library:read", "library:import", core.ScopeSearch, core.ScopeDay, core.ScopeWidget, core.ScopeMoveExport, core.ScopeMoveImport} {
		if !offered[sc] {
			t.Errorf("%s is not offered", sc)
		}
	}
	if offered[core.ScopeCapture] {
		t.Errorf("%s is offered", core.ScopeCapture)
	}
}

// Without Pocket Core the routes answer the person signed in to the app.
func TestPocketRoutesWithoutCore(t *testing.T) {
	f := newCapFixture(t)
	if f.s.core.Configured() {
		t.Fatal("the fixture has a Core")
	}
	f.reads(f.bob, f.saga, time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC), 0.125, 600, 5)
	if got := f.search(f.bobTok, "q=saga"); len(got) != 1 || got[0].Snippet != "Brian K. Vaughan, Fiona Staples · page 40 of 320" {
		t.Errorf("search = %+v", got)
	}
	if d := f.day(f.bobTok, "2026-10-05", "UTC"); d.Summary != "Read Saga for 10 min (5 pages)" {
		t.Errorf("day = %+v", d)
	}
	if w := f.widget(f.bobTok); w.Line != "Reading Saga, page 40 of 320" {
		t.Errorf("widget = %+v", w)
	}
	// a link token means nothing here
	for _, tok := range []string{"pcx_bob_widget", ""} {
		if code, _, _ := f.do(tok, "GET", "/api/v1/pocket/widget", nil); code != http.StatusUnauthorized {
			t.Errorf("%q without Core = %d, want 401", tok, code)
		}
	}
}
