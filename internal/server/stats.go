package server

// What a person read: stats, one day in a sentence, the dashboard for a
// Homepage widget, the reading feed other Pocket apps follow (Bookshelf takes
// finished books in as reads), and the person's Pocket Bookshelf rating on a
// book page.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-library/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

func (s *Server) routesStats(api chi.Router) {
	api.Get("/stats", s.handleStats)
	api.Get("/day/{date}", s.handleDay)
	api.Get("/dashboard", s.handleDashboard)
	api.Get("/reading/current", s.handleReadingCurrent)
	api.Get("/reading/feed", s.handleReadingFeed)
}

// statsRange turns 7d / 30d / 90d / year / all / YYYY into days (inclusive).
func statsRange(rng string) (string, string, string) {
	today := time.Now()
	to := today.Format("2006-01-02")
	switch rng {
	case "7d", "30d", "90d":
		n, _ := strconv.Atoi(strings.TrimSuffix(rng, "d"))
		return rng, today.AddDate(0, 0, -(n - 1)).Format("2006-01-02"), to
	case "all":
		return rng, "", to
	case "", "year":
		return "year", fmt.Sprintf("%d-01-01", today.Year()), to
	}
	if y, err := strconv.Atoi(rng); err == nil && y > 1900 && y <= today.Year() {
		return rng, fmt.Sprintf("%d-01-01", y), fmt.Sprintf("%d-12-31", y)
	}
	return "year", fmt.Sprintf("%d-01-01", today.Year()), to
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	rng, from, to := statsRange(r.URL.Query().Get("range"))
	sessions, err := s.store.Sessions(ctx, u.ID, store.SessionFilter{From: from, To: to})
	if err != nil {
		s.fail(w, err, "stats")
		return
	}
	reads, err := s.store.Reads(ctx, u.ID, 0, from, to)
	if err != nil {
		s.fail(w, err, "stats")
		return
	}
	libs := s.allowedLibraries(ctx, u)
	out := map[string]any{"range": rng, "from": from, "to": to}
	var secs, pages int
	days := map[string]bool{}
	perDay := map[string][2]int{}
	perMonth := map[string][2]int{} // finished, seconds
	byDevice := map[string]int{}
	byFile := map[int64]int{}
	started := map[int64]bool{}
	var paceSecs, pacePages int
	for _, x := range sessions {
		secs += x.Seconds
		pages += x.Pages
		if x.Seconds > 0 {
			days[x.Day] = true
		}
		d := perDay[x.Day]
		perDay[x.Day] = [2]int{d[0] + x.Seconds, d[1] + x.Pages}
		m := perMonth[x.Day[:7]]
		perMonth[x.Day[:7]] = [2]int{m[0], m[1] + x.Seconds}
		dev := x.Device
		if dev == "" {
			dev = "Unknown"
		}
		byDevice[dev] += x.Seconds
		byFile[x.FileID] += x.Seconds
		started[x.BookID] = true
		if !x.Estimated && x.Pages > 0 {
			paceSecs += x.Seconds
			pacePages += x.Pages
		}
	}
	finished := 0
	var finishedIDs []int64
	for _, rd := range reads {
		if rd.Status != "read" {
			continue
		}
		finished++
		finishedIDs = append(finishedIDs, rd.BookID)
		m := perMonth[rd.Day[:7]]
		perMonth[rd.Day[:7]] = [2]int{m[0] + 1, m[1]}
	}
	out["seconds"], out["sessions"], out["pages"], out["days_read"] = secs, len(sessions), pages, len(days)
	out["finished"], out["books_started"] = finished, len(started)
	cur, best := s.streaks(ctx, u.ID)
	out["streak"], out["best_streak"] = cur, best
	pd := []map[string]any{}
	if from != "" {
		for d := mustDay(from); !d.After(mustDay(to)); d = d.AddDate(0, 0, 1) {
			k := d.Format("2006-01-02")
			pd = append(pd, map[string]any{"day": k, "seconds": perDay[k][0], "pages": perDay[k][1]})
		}
	} else {
		keys := make([]string, 0, len(perDay))
		for k := range perDay {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			pd = append(pd, map[string]any{"day": k, "seconds": perDay[k][0], "pages": perDay[k][1]})
		}
	}
	out["per_day"] = pd
	months := make([]string, 0, len(perMonth))
	for k := range perMonth {
		months = append(months, k)
	}
	sort.Strings(months)
	pm := []map[string]any{}
	for _, k := range months {
		pm = append(pm, map[string]any{"month": k, "finished": perMonth[k][0], "seconds": perMonth[k][1]})
	}
	out["per_month"] = pm
	out["by_device"] = sortedSeconds(byDevice, "device")
	byFormat := map[string]int{}
	for fid, sec := range byFile {
		format := "unknown"
		if f, err := s.store.GetFile(ctx, fid); err == nil {
			format = f.Format
		}
		byFormat[format] += sec
	}
	out["by_format"] = sortedSeconds(byFormat, "format")
	finishedBooks := []store.Book{}
	topAuthors := map[int64]*struct {
		name  string
		books int
	}{}
	topTags := map[string]int{}
	if len(finishedIDs) > 0 {
		books, _, _ := s.store.ListBooks(ctx, store.BookQuery{UserID: u.ID, Libraries: libs, IDs: finishedIDs, Sort: "last_read", Order: "desc", Limit: 500, ShowHidden: true})
		finishedBooks = books
		for _, b := range books {
			for _, a := range b.Authors {
				if topAuthors[a.ID] == nil {
					topAuthors[a.ID] = &struct {
						name  string
						books int
					}{name: a.Name}
				}
				topAuthors[a.ID].books++
			}
			for _, t := range b.Tags {
				topTags[t]++
			}
		}
	}
	if finishedBooks == nil {
		finishedBooks = []store.Book{}
	}
	out["finished_books"] = finishedBooks
	ta := []map[string]any{}
	for id, a := range topAuthors {
		ta = append(ta, map[string]any{"id": id, "name": a.name, "books": a.books})
	}
	sort.Slice(ta, func(i, j int) bool { return ta[i]["books"].(int) > ta[j]["books"].(int) })
	if len(ta) > 10 {
		ta = ta[:10]
	}
	out["top_authors"] = ta
	tt := []map[string]any{}
	for n, c := range topTags {
		tt = append(tt, map[string]any{"name": n, "books": c})
	}
	sort.Slice(tt, func(i, j int) bool { return tt[i]["books"].(int) > tt[j]["books"].(int) })
	if len(tt) > 10 {
		tt = tt[:10]
	}
	out["top_tags"] = tt
	pace := 0.0
	if paceSecs > 600 {
		pace = float64(pacePages) / (float64(paceSecs) / 3600)
	}
	out["pace_pages_per_hour"] = int(pace + 0.5)
	writeJSON(w, http.StatusOK, out)
}

func mustDay(d string) time.Time {
	t, _ := time.ParseInLocation("2006-01-02", d, time.Local)
	return t
}

func sortedSeconds(m map[string]int, key string) []map[string]any {
	out := []map[string]any{}
	for k, v := range m {
		if v > 0 {
			out = append(out, map[string]any{key: k, "seconds": v})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["seconds"].(int) > out[j]["seconds"].(int) })
	return out
}

// streaks are the current and the longest run of days with reading (a day
// counts once it reaches the daily goal, or any reading when there is none).
func (s *Server) streaks(ctx context.Context, uid int64) (int, int) {
	st, _ := s.store.GetSettings(ctx, uid)
	need := max(st.DailyGoal*60, 1)
	rows, err := s.store.DB.QueryContext(ctx, `SELECT day, SUM(seconds) FROM reading_sessions WHERE user_id = ? GROUP BY day ORDER BY day`, uid)
	if err != nil {
		return 0, 0
	}
	defer rows.Close()
	var days []time.Time
	for rows.Next() {
		var d string
		var sec int
		if rows.Scan(&d, &sec) == nil && sec >= need {
			days = append(days, mustDay(d))
		}
	}
	best, run := 0, 0
	for i, d := range days {
		if i > 0 && d.Sub(days[i-1]) <= 25*time.Hour {
			run++
		} else {
			run = 1
		}
		best = max(best, run)
	}
	cur := 0
	if n := len(days); n > 0 {
		today := mustDay(store.Today())
		last := days[n-1]
		if today.Sub(last) <= 25*time.Hour {
			cur = 1
			for i := n - 1; i > 0 && days[i].Sub(days[i-1]) <= 25*time.Hour; i-- {
				cur++
			}
		}
	}
	return cur, best
}

// homeStats is the small strip on Home.
func (s *Server) homeStats(ctx context.Context, u *store.User) map[string]any {
	st, _ := s.store.GetSettings(ctx, u.ID)
	var reading, finishedYear, today int
	_ = s.store.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM user_books WHERE user_id = ? AND status = 'reading'`, u.ID).Scan(&reading)
	_ = s.store.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM reads WHERE user_id = ? AND status = 'read' AND day >= ?`, u.ID, fmt.Sprintf("%d-01-01", time.Now().Year())).Scan(&finishedYear)
	_ = s.store.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(seconds), 0) FROM reading_sessions WHERE user_id = ? AND day = ?`, u.ID, store.Today()).Scan(&today)
	cur, _ := s.streaks(ctx, u.ID)
	return map[string]any{"reading": reading, "finished_year": finishedYear, "yearly_goal": st.YearlyGoal,
		"minutes_today": today / 60, "daily_goal": st.DailyGoal, "streak": cur}
}

// handleDay is one day of reading: the books, minutes and pages, and a sentence (Pocket Day).
func (s *Server) handleDay(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	day := chi.URLParam(r, "date")
	if !validDate(day) {
		writeErr(w, http.StatusBadRequest, "the date is YYYY-MM-DD")
		return
	}
	sessions, err := s.store.Sessions(ctx, u.ID, store.SessionFilter{From: day, To: day})
	if err != nil {
		s.fail(w, err, "day")
		return
	}
	reads, _ := s.store.Reads(ctx, u.ID, 0, day, day)
	type dayBook struct {
		BookID    int64    `json:"book_id"`
		Title     string   `json:"title"`
		Authors   []string `json:"authors"`
		Cover     string   `json:"cover"`
		Seconds   int      `json:"seconds"`
		Pages     int      `json:"pages"`
		Finished  bool     `json:"finished"`
		Estimated bool     `json:"estimated"`
		EndPct    float64  `json:"end_pct"`
		Devices   []string `json:"devices"`
	}
	byBook := map[int64]*dayBook{}
	var order []int64
	for i := len(sessions) - 1; i >= 0; i-- {
		x := sessions[i]
		b := byBook[x.BookID]
		if b == nil {
			b = &dayBook{BookID: x.BookID, Devices: []string{}}
			byBook[x.BookID] = b
			order = append(order, x.BookID)
		}
		b.Seconds += x.Seconds
		b.Pages += x.Pages
		b.Estimated = b.Estimated || x.Estimated
		b.EndPct = x.EndPct
		if x.Device != "" && !containsStr(b.Devices, x.Device) {
			b.Devices = append(b.Devices, x.Device)
		}
	}
	for _, rd := range reads {
		if rd.Status != "read" {
			continue
		}
		if byBook[rd.BookID] == nil {
			byBook[rd.BookID] = &dayBook{BookID: rd.BookID, Devices: []string{}}
			order = append(order, rd.BookID)
		}
		byBook[rd.BookID].Finished = true
	}
	items := []dayBook{}
	var secs, pages, finished int
	for _, id := range order {
		b := byBook[id]
		if bk, err := s.store.GetBookAny(ctx, id); err == nil {
			b.Title, b.Cover = bk.Title, bk.Cover
			for _, a := range bk.Authors {
				b.Authors = append(b.Authors, a.Name)
			}
		}
		if b.Authors == nil {
			b.Authors = []string{}
		}
		secs += b.Seconds
		pages += b.Pages
		if b.Finished {
			finished++
		}
		items = append(items, *b)
	}
	summary := ""
	if len(items) > 0 {
		names := []string{}
		for _, b := range items {
			names = append(names, b.Title)
		}
		summary = fmt.Sprintf("Read %s", longDuration(secs))
		if pages > 0 {
			summary += fmt.Sprintf(" (%d pages)", pages)
		}
		summary += ": " + joinNames(names)
		if finished > 0 {
			summary += fmt.Sprintf("; finished %d", finished)
		}
		summary += "."
	}
	writeJSON(w, http.StatusOK, map[string]any{"date": day, "seconds": secs, "pages": pages, "finished": finished, "books": items, "summary": summary})
}

func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func joinNames(n []string) string {
	switch len(n) {
	case 0:
		return ""
	case 1:
		return n[0]
	}
	return strings.Join(n[:len(n)-1], ", ") + " and " + n[len(n)-1]
}

func longDuration(sec int) string {
	h, m := sec/3600, (sec%3600)/60
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("%d h %d min", h, m)
	case h > 0:
		return fmt.Sprintf("%d h", h)
	case m > 0:
		return fmt.Sprintf("%d min", m)
	}
	return "under a minute"
}

// handleDashboard is a compact summary for a Homepage widget.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	hs := s.homeStats(ctx, u)
	out := map[string]any{"reading": hs["reading"], "finished_this_year": hs["finished_year"], "minutes_today": hs["minutes_today"], "streak": hs["streak"]}
	libs := s.allowedLibraries(ctx, u)
	var books int
	_ = s.store.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM books WHERE missing = 0 AND library_id IN (`+joinIDs(libs)+`)`).Scan(&books)
	out["books"] = books
	var week int
	_ = s.store.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(seconds), 0) FROM reading_sessions WHERE user_id = ? AND day >= ?`, u.ID, time.Now().AddDate(0, 0, -6).Format("2006-01-02")).Scan(&week)
	out["minutes_week"] = week / 60
	cur, _, _ := s.store.ListBooks(ctx, store.BookQuery{UserID: u.ID, Libraries: libs, Status: "reading", Sort: "last_read", Limit: 1})
	if len(cur) > 0 {
		b := cur[0]
		pct := 0.0
		if b.State != nil {
			pct = b.State.Percentage
		}
		out["current"] = map[string]any{"title": b.Title, "authors": b.Authors, "percent": int(pct*100 + 0.5), "cover": b.Cover}
		out["current_title"] = b.Title
		out["current_percent"] = int(pct*100 + 0.5)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleReadingCurrent lists the books the person is reading, with where they are.
func (s *Server) handleReadingCurrent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	books, _, err := s.store.ListBooks(ctx, store.BookQuery{UserID: u.ID, Libraries: s.allowedLibraries(ctx, u), Status: "reading", Sort: "last_read", Limit: 50, ShowHidden: true})
	if err != nil {
		s.fail(w, err, "reading")
		return
	}
	out := []map[string]any{}
	for _, b := range books {
		out = append(out, feedBook(&b))
	}
	writeJSON(w, http.StatusOK, out)
}

// feedBook is how another app sees a book: enough to match it on its own shelves.
func feedBook(b *store.Book) map[string]any {
	authors := []string{}
	for _, a := range b.Authors {
		authors = append(authors, a.Name)
	}
	m := map[string]any{"book_id": b.ID, "title": b.Title, "subtitle": b.Subtitle, "authors": authors, "isbn": b.ISBN, "identifiers": b.Identifiers,
		"series": b.Series, "series_index": b.SeriesIndex, "pages": b.Pages, "language": b.Language, "published": b.Published, "cover": b.Cover, "kind": b.Kind}
	if b.State != nil {
		m["status"], m["percentage"], m["last_read_at"], m["started_at"], m["finished_at"], m["rating"], m["device"] =
			b.State.Status, b.State.Percentage, b.State.LastReadAt, b.State.StartedAt, b.State.FinishedAt, b.State.Rating, b.State.LastDevice
	}
	return m
}

// handleReadingFeed is what changed in the person's reading since a moment:
// books whose state moved (started, progressed, finished) and finished reads,
// for Pocket Bookshelf and other followers. ?since=RFC3339 (default 30 days).
func (s *Server) handleReadingFeed(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	since := r.URL.Query().Get("since")
	if _, err := time.Parse(time.RFC3339Nano, since); err != nil {
		since = time.Now().AddDate(0, 0, -30).UTC().Format(time.RFC3339Nano)
	}
	ids, err := s.store.ChangedUserBooks(ctx, u.ID, since, 500)
	if err != nil {
		s.fail(w, err, "feed")
		return
	}
	books := []map[string]any{}
	if len(ids) > 0 {
		list, _, err := s.store.ListBooks(ctx, store.BookQuery{UserID: u.ID, Libraries: s.allowedLibraries(ctx, u), IDs: ids, Sort: "last_read", Order: "desc", Limit: 500, ShowHidden: true})
		if err != nil {
			s.fail(w, err, "feed")
			return
		}
		for i := range list {
			books = append(books, feedBook(&list[i]))
		}
	}
	reads := []map[string]any{}
	all, _ := s.store.Reads(ctx, u.ID, 0, since[:10], "")
	for _, rd := range all {
		if rd.FinishedAt < since && rd.Day < since[:10] {
			continue
		}
		m := map[string]any{"id": rd.ID, "book_id": rd.BookID, "status": rd.Status, "started_at": rd.StartedAt, "finished_at": rd.FinishedAt, "day": rd.Day}
		if b, err := s.store.GetBookAny(ctx, rd.BookID); err == nil {
			for k, v := range feedBook(b) {
				if _, ok := m[k]; !ok {
					m[k] = v
				}
			}
		}
		reads = append(reads, m)
	}
	writeJSON(w, http.StatusOK, map[string]any{"since": since, "now": time.Now().UTC().Format(time.RFC3339Nano), "books": books, "reads": reads})
}

// notifyFinished pushes "you finished …" when the person asked for it.
func (s *Server) notifyFinished(ctx context.Context, u *store.User, b *store.Book) {
	st, err := s.store.GetSettings(ctx, u.ID)
	if err != nil || !st.NotifyFinished || st.NotifyURL == "" {
		return
	}
	authors := []string{}
	for _, a := range b.Authors {
		authors = append(authors, a.Name)
	}
	body := "You finished " + b.Title
	if len(authors) > 0 {
		body += " by " + joinNames(authors)
	}
	go func() {
		c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := notify.New(st.NotifyURL, st.NotifyFormat).Send(c, notify.Message{Title: "Finished a book", Body: body + ".", Priority: "info", Tags: []string{"books"}}); err != nil {
			s.log.Warn("notify finished", "err", err)
		}
	}()
}

// ---- Pocket Bookshelf (Core link books:read) ----

type bsCacheEntry struct {
	at  time.Time
	val map[string]any
}

var (
	bsMu    sync.Mutex
	bsCache = map[string]bsCacheEntry{}
)

// bookshelfInfo is the person's own entry for this book in Pocket Bookshelf
// (rating, status, a link), when Core links the two for them. nil when there
// is no Core or the link is off; {available:false} when the book isn't there.
func (s *Server) bookshelfInfo(ctx context.Context, u *store.User, b *store.Book) map[string]any {
	if !s.coreConnected() {
		return nil
	}
	key := fmt.Sprintf("%d:%d", u.ID, b.ID)
	bsMu.Lock()
	if e, ok := bsCache[key]; ok && time.Since(e.at) < 10*time.Minute {
		bsMu.Unlock()
		return e.val
	}
	bsMu.Unlock()
	c, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	val := s.lookupBookshelf(c, u, b)
	bsMu.Lock()
	if len(bsCache) > 5000 {
		bsCache = map[string]bsCacheEntry{}
	}
	bsCache[key] = bsCacheEntry{at: time.Now(), val: val}
	bsMu.Unlock()
	return val
}

func (s *Server) lookupBookshelf(ctx context.Context, u *store.User, b *store.Book) map[string]any {
	tok, err := s.core.ActAs(ctx, u.Email, "bookshelf", "books:read")
	if err != nil || tok.URL == "" {
		return nil
	}
	type bsBook struct {
		ID      int64   `json:"id"`
		Title   string  `json:"title"`
		Authors string  `json:"authors"`
		Status  string  `json:"status"`
		Rating  float64 `json:"rating"`
	}
	get := func(q url.Values) []bsBook {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(tok.URL, "/")+"/api/v1/books?"+q.Encode(), nil)
		if err != nil {
			return nil
		}
		req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		res, err := s.http.Do(req)
		if err != nil {
			return nil
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return nil
		}
		var raw json.RawMessage
		if json.NewDecoder(res.Body).Decode(&raw) != nil {
			return nil
		}
		var list []bsBook
		if json.Unmarshal(raw, &list) == nil {
			return list
		}
		var page struct {
			Items []bsBook `json:"items"`
			Books []bsBook `json:"books"`
		}
		if json.Unmarshal(raw, &page) == nil {
			if page.Items != nil {
				return page.Items
			}
			return page.Books
		}
		return nil
	}
	var hits []bsBook
	if b.ISBN != "" {
		hits = get(url.Values{"isbn": {b.ISBN}})
	}
	if len(hits) == 0 {
		for _, h := range get(url.Values{"q": {b.Title}}) {
			if strings.EqualFold(strings.TrimSpace(h.Title), strings.TrimSpace(b.Title)) {
				hits = append(hits, h)
			}
		}
	}
	base := strings.TrimRight(tok.PublicURL, "/")
	if base == "" {
		base = strings.TrimRight(tok.URL, "/")
	}
	if len(hits) == 0 {
		return map[string]any{"available": false, "url": base}
	}
	h := hits[0]
	return map[string]any{"available": true, "url": fmt.Sprintf("%s/books/%d", base, h.ID), "rating": int(h.Rating*2 + 0.5), "status": h.Status}
}

// ---- export / import of one person's own data ----

type exportBook struct {
	Title       string            `json:"title"`
	Authors     []string          `json:"authors"`
	ISBN        string            `json:"isbn,omitempty"`
	Identifiers map[string]string `json:"identifiers,omitempty"`
}

type exportDoc struct {
	App         string           `json:"app"`
	Version     string           `json:"version"`
	Exported    string           `json:"exported_at"`
	Books       []exportBookData `json:"books"`
	Shelves     []exportShelf    `json:"shelves"`
	Annotations int              `json:"annotations"`
}

type exportBookData struct {
	exportBook
	State       *store.UserBook        `json:"state,omitempty"`
	Reads       []store.Read           `json:"reads,omitempty"`
	Sessions    []store.ReadingSession `json:"sessions,omitempty"`
	Annotations []store.Annotation     `json:"annotations,omitempty"`
}

type exportShelf struct {
	store.Shelf
	Books []exportBook `json:"books"`
}

func exportOf(b *store.Book) exportBook {
	e := exportBook{Title: b.Title, ISBN: b.ISBN, Identifiers: b.Identifiers}
	for _, a := range b.Authors {
		e.Authors = append(e.Authors, a.Name)
	}
	return e
}

// handleExportJSON is everything the person did in the library as JSON: their
// place, ratings, reads, reading time, highlights and shelves, with the books
// named by title, authors and ISBN so another box can match them.
func (s *Server) handleExportJSON(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	doc := exportDoc{App: "pocket-library", Version: Version, Exported: store.Now(), Books: []exportBookData{}, Shelves: []exportShelf{}}
	ids, err := s.store.ChangedUserBooks(ctx, u.ID, "", 1000000)
	if err != nil {
		s.fail(w, err, "export")
		return
	}
	sessions, _ := s.store.Sessions(ctx, u.ID, store.SessionFilter{})
	anns, _ := s.store.Annotations(ctx, u.ID, store.AnnotationFilter{})
	bySess := map[int64][]store.ReadingSession{}
	for _, x := range sessions {
		bySess[x.BookID] = append(bySess[x.BookID], x)
	}
	byAnn := map[int64][]store.Annotation{}
	for _, a := range anns {
		a.BookTitle, a.BookCover = "", ""
		byAnn[a.BookID] = append(byAnn[a.BookID], a)
	}
	seen := map[int64]bool{}
	add := func(id int64) {
		if seen[id] {
			return
		}
		seen[id] = true
		b, err := s.store.GetBookAny(ctx, id)
		if err != nil {
			return
		}
		ub, _ := s.store.GetUserBook(ctx, u.ID, id)
		reads, _ := s.store.Reads(ctx, u.ID, id, "", "")
		doc.Books = append(doc.Books, exportBookData{exportBook: exportOf(b), State: ub, Reads: reads, Sessions: bySess[id], Annotations: byAnn[id]})
		doc.Annotations += len(byAnn[id])
	}
	for _, id := range ids {
		add(id)
	}
	for id := range bySess {
		add(id)
	}
	for id := range byAnn {
		add(id)
	}
	shelves, _ := s.store.ListShelves(ctx, u.ID)
	for _, sh := range shelves {
		es := exportShelf{Shelf: sh, Books: []exportBook{}}
		if sh.Kind == "manual" {
			books, _, _ := s.shelfBooks(ctx, u, &sh, "", "", 0, 500)
			for i := range books {
				es.Books = append(es.Books, exportOf(&books[i]))
			}
		}
		doc.Shelves = append(doc.Shelves, es)
	}
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-library-export.json"`)
	writeJSON(w, http.StatusOK, doc)
}

// handleImportJSON adds an export to the caller's own data, matching books by
// ISBN, else by title and first author, in the libraries they may open.
func (s *Server) handleImportJSON(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	var doc exportDoc
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadBytes)).Decode(&doc); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	libs := s.allowedLibraries(ctx, u)
	match := func(e exportBook) int64 {
		if e.ISBN != "" {
			if list, _, _ := s.store.ListBooks(ctx, store.BookQuery{UserID: u.ID, Libraries: libs, Q: e.ISBN, Limit: 2, ShowHidden: true}); len(list) == 1 {
				return list[0].ID
			}
		}
		list, _, _ := s.store.ListBooks(ctx, store.BookQuery{UserID: u.ID, Libraries: libs, Q: e.Title, Limit: 50, ShowHidden: true})
		for _, b := range list {
			if !strings.EqualFold(b.Title, e.Title) {
				continue
			}
			if len(e.Authors) == 0 || len(b.Authors) > 0 && strings.EqualFold(b.Authors[0].Name, e.Authors[0]) {
				return b.ID
			}
		}
		return 0
	}
	counts := map[string]int{}
	var unmatched []string
	for _, e := range doc.Books {
		id := match(e.exportBook)
		if id == 0 {
			unmatched = append(unmatched, e.Title)
			continue
		}
		counts["books"]++
		if e.State != nil {
			cur, _ := s.store.GetUserBook(ctx, u.ID, id)
			if cur.UpdatedAt == "" || cur.UpdatedAt < e.State.UpdatedAt {
				if e.State.Status != "" && e.State.Status != cur.Status {
					_, _, _ = s.store.SetStatus(ctx, u.ID, id, e.State.Status, "")
				}
				r, f, h := e.State.Rating, e.State.Favorite, e.State.Hidden
				_, _ = s.store.UpdateUserBook(ctx, u.ID, id, &r, &f, &h)
				if e.State.Percentage > 0 {
					_, _, _, _ = s.store.SetProgress(ctx, u.ID, id, store.Progress{Percentage: e.State.Percentage, Locator: e.State.Locator, Device: e.State.LastDevice})
				}
			}
		}
		for _, a := range e.Annotations {
			a.ID, a.BookID, a.FileID = 0, id, 0
			if a.ExternalID == "" {
				a.ExternalID = fmt.Sprintf("import:%s:%s", a.CreatedAt, trim(a.Text, 40))
			}
			if created, err := s.store.SaveAnnotation(ctx, u.ID, &a); err == nil && created {
				counts["annotations"]++
			}
		}
	}
	for _, sh := range doc.Shelves {
		ns := sh.Shelf
		ns.ID = 0
		if err := s.store.CreateShelf(ctx, u.ID, &ns); err != nil {
			continue
		}
		counts["shelves"]++
		var ids []int64
		for _, e := range sh.Books {
			if id := match(e); id > 0 {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			_ = s.store.AddToShelf(ctx, u.ID, ns.ID, ids)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"counts": counts, "unmatched": unmatched})
}
