package store

import (
	"context"
	"database/sql"
	"math"
	"strings"
	"time"
)

// ============ reading sessions ============

// SessionGap is how long a pause may be before the next report starts a new session.
const SessionGap = 30 * time.Minute

// SessionReport is one report of reading: the web reader every half minute
// or so, an e-reader's sync, an import.
type SessionReport struct {
	BookID  int64
	FileID  int64
	Device  string
	Source  string    // web | kosync | koreader | import
	At      time.Time // when the report was made (end of the reading)
	Pct     float64   // position now
	PrevPct float64   // position at the previous report (-1 = unknown)
	// Seconds actually spent since the previous report, when the client
	// measured it (web reader, KOReader statistics); 0 = derive from the gap.
	Seconds int
	Pages   int // pages turned, when known
	// Estimate the time from the pages read and the person's pace (e-reader
	// syncs, which only send a position).
	Estimate     bool
	PagesInBook  int     // for estimates: printed pages
	MinutesPerPg float64 // for estimates: the person's pace (0 = 1.5)
}

// RecordSession adds a report to the person's latest session of that book
// on that device, or starts a new one after a pause of SessionGap.
func (s *Store) RecordSession(ctx context.Context, uid int64, r SessionReport) (*ReadingSession, error) {
	if r.At.IsZero() {
		r.At = time.Now()
	}
	if r.Source == "" {
		r.Source = "web"
	}
	at := r.At.UTC().Format(time.RFC3339Nano)
	var cur ReadingSession
	var fileID sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT id, book_id, file_id, device, source, day, started_at, ended_at, seconds, start_pct, end_pct, pages, estimated
		FROM reading_sessions WHERE user_id = ? AND book_id = ? AND device = ? ORDER BY ended_at DESC LIMIT 1`, uid, r.BookID, r.Device).
		Scan(&cur.ID, &cur.BookID, &fileID, &cur.Device, &cur.Source, &cur.Day, &cur.StartedAt, &cur.EndedAt, &cur.Seconds, &cur.StartPct, &cur.EndPct, &cur.Pages, &cur.Estimated)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	pagesDelta := r.Pages
	if pagesDelta == 0 && r.PagesInBook > 0 {
		prev := r.PrevPct
		if prev < 0 && cur.ID > 0 {
			prev = cur.EndPct
		}
		if prev >= 0 && r.Pct > prev {
			pagesDelta = int(math.Round((r.Pct - prev) * float64(r.PagesInBook)))
		}
	}
	estimate := func() int {
		pace := r.MinutesPerPg
		if pace <= 0 {
			pace = 1.5
		}
		return int(float64(pagesDelta) * pace * 60)
	}
	if cur.ID > 0 && err == nil {
		last, perr := time.Parse(time.RFC3339Nano, cur.EndedAt)
		if perr == nil && r.At.Sub(last) >= 0 && r.At.Sub(last) <= SessionGap {
			add := r.Seconds
			if add == 0 {
				if r.Estimate {
					add = estimate()
				} else {
					add = int(r.At.Sub(last).Seconds())
				}
			}
			cur.Seconds += add
			cur.EndedAt, cur.EndPct = at, r.Pct
			cur.Pages += pagesDelta
			cur.Estimated = cur.Estimated || (r.Estimate && r.Seconds == 0)
			if _, err := s.DB.ExecContext(ctx, `UPDATE reading_sessions SET ended_at = ?, seconds = ?, end_pct = ?, pages = ?, estimated = ? WHERE id = ?`,
				cur.EndedAt, cur.Seconds, cur.EndPct, cur.Pages, boolInt(cur.Estimated), cur.ID); err != nil {
				return nil, err
			}
			return &cur, nil
		}
	}
	// a new session
	secs := r.Seconds
	start := r.At
	if secs == 0 && r.Estimate {
		secs = estimate()
	}
	if secs > 0 {
		start = r.At.Add(-time.Duration(secs) * time.Second)
	}
	startPct := r.PrevPct
	if startPct < 0 {
		startPct = r.Pct
	}
	n := ReadingSession{BookID: r.BookID, FileID: r.FileID, Device: r.Device, Source: r.Source, Day: start.Local().Format("2006-01-02"),
		StartedAt: start.UTC().Format(time.RFC3339Nano), EndedAt: at, Seconds: secs, StartPct: startPct, EndPct: r.Pct, Pages: pagesDelta,
		Estimated: r.Estimate && r.Seconds == 0}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO reading_sessions (user_id, book_id, file_id, device, source, day, started_at, ended_at, seconds, start_pct, end_pct, pages, estimated)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, uid, n.BookID, nullI(&n.FileID), n.Device, n.Source, n.Day, n.StartedAt, n.EndedAt, n.Seconds, n.StartPct, n.EndPct, n.Pages, boolInt(n.Estimated))
	if err != nil {
		return nil, err
	}
	n.ID, _ = res.LastInsertId()
	return &n, nil
}

// SessionFilter narrows a person's sessions.
type SessionFilter struct {
	BookID int64
	From   string // day, inclusive
	To     string // day, inclusive
	Limit  int
	Offset int
}

// Sessions lists a person's reading sessions, newest first.
func (s *Store) Sessions(ctx context.Context, uid int64, f SessionFilter) ([]ReadingSession, error) {
	q := `SELECT rs.id, rs.book_id, COALESCE(rs.file_id, 0), rs.device, rs.source, rs.day, rs.started_at, rs.ended_at, rs.seconds, rs.start_pct, rs.end_pct, rs.pages, rs.estimated
		FROM reading_sessions rs WHERE rs.user_id = ?`
	args := []any{uid}
	if f.BookID > 0 {
		q += ` AND rs.book_id = ?`
		args = append(args, f.BookID)
	}
	if f.From != "" {
		q += ` AND rs.day >= ?`
		args = append(args, f.From)
	}
	if f.To != "" {
		q += ` AND rs.day <= ?`
		args = append(args, f.To)
	}
	q += ` ORDER BY rs.started_at DESC`
	if f.Limit > 0 {
		q += ` LIMIT ? OFFSET ?`
		args = append(args, f.Limit, max(f.Offset, 0))
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReadingSession{}
	for rows.Next() {
		var r ReadingSession
		if err := rows.Scan(&r.ID, &r.BookID, &r.FileID, &r.Device, &r.Source, &r.Day, &r.StartedAt, &r.EndedAt, &r.Seconds, &r.StartPct, &r.EndPct, &r.Pages, &r.Estimated); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteSession removes one of a person's reading sessions.
func (s *Store) DeleteReadingSession(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM reading_sessions WHERE id = ? AND user_id = ?`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ============ annotations ============

const annCols = `a.id, a.book_id, COALESCE(a.file_id, 0), a.kind, a.text, a.note, a.color, a.locator, a.chapter, a.percentage, a.page, a.source, a.external_id, a.created_at, a.updated_at`

func scanAnnotation(row interface{ Scan(...any) error }) (*Annotation, error) {
	var a Annotation
	if err := row.Scan(&a.ID, &a.BookID, &a.FileID, &a.Kind, &a.Text, &a.Note, &a.Color, &a.Locator, &a.Chapter, &a.Percentage, &a.Page, &a.Source, &a.ExternalID, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	return &a, nil
}

// AnnotationFilter narrows a person's annotations.
type AnnotationFilter struct {
	BookID int64
	Kind   string
	Q      string
	Since  string // updated_at >= (sync)
	Limit  int
	Offset int
}

// Annotations lists a person's annotations: one book's in reading order, or
// across books newest first (with the book's title and cover).
func (s *Store) Annotations(ctx context.Context, uid int64, f AnnotationFilter) ([]Annotation, error) {
	q := `SELECT ` + annCols + `, b.title, b.cover FROM annotations a JOIN books b ON b.id = a.book_id WHERE a.user_id = ?`
	args := []any{uid}
	if f.BookID > 0 {
		q += ` AND a.book_id = ?`
		args = append(args, f.BookID)
	}
	if f.Kind != "" {
		q += ` AND a.kind = ?`
		args = append(args, f.Kind)
	}
	if v := strings.TrimSpace(f.Q); v != "" {
		q += ` AND (a.text LIKE ? OR a.note LIKE ? OR b.title LIKE ?)`
		args = append(args, "%"+v+"%", "%"+v+"%", "%"+v+"%")
	}
	if f.Since != "" {
		q += ` AND a.updated_at >= ?`
		args = append(args, f.Since)
	}
	if f.BookID > 0 {
		q += ` ORDER BY a.percentage, a.page, a.id`
	} else {
		q += ` ORDER BY a.created_at DESC`
	}
	if f.Limit > 0 {
		q += ` LIMIT ? OFFSET ?`
		args = append(args, f.Limit, max(f.Offset, 0))
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Annotation{}
	for rows.Next() {
		var a Annotation
		var cover string
		if err := rows.Scan(&a.ID, &a.BookID, &a.FileID, &a.Kind, &a.Text, &a.Note, &a.Color, &a.Locator, &a.Chapter, &a.Percentage, &a.Page, &a.Source, &a.ExternalID, &a.CreatedAt, &a.UpdatedAt, &a.BookTitle, &cover); err != nil {
			return nil, err
		}
		if cover != "" {
			a.BookCover = CoverURL(a.BookID, cover)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAnnotation returns one of a person's annotations.
func (s *Store) GetAnnotation(ctx context.Context, uid, id int64) (*Annotation, error) {
	return scanAnnotation(s.DB.QueryRowContext(ctx, `SELECT `+annCols+` FROM annotations a WHERE a.id = ? AND a.user_id = ?`, id, uid))
}

func cleanAnnotation(a *Annotation) {
	switch a.Kind {
	case "highlight", "note", "bookmark":
	default:
		a.Kind = "highlight"
	}
	if a.Color == "" {
		a.Color = "yellow"
	}
	if a.Source == "" {
		a.Source = "web"
	}
	if a.Percentage < 0 {
		a.Percentage = 0
	}
	if a.Percentage > 1 {
		a.Percentage = 1
	}
}

// SaveAnnotation inserts (ID 0) or updates one of a person's annotations. With
// an external id an existing one from the same source is updated instead
// (re-syncs from KOReader or a clippings file); created is false then.
func (s *Store) SaveAnnotation(ctx context.Context, uid int64, a *Annotation) (created bool, err error) {
	cleanAnnotation(a)
	now := Now()
	if a.CreatedAt == "" {
		a.CreatedAt = now
	}
	a.UpdatedAt = now
	if a.ID == 0 && a.ExternalID != "" {
		var id int64
		err := s.DB.QueryRowContext(ctx, `SELECT id FROM annotations WHERE user_id = ? AND book_id = ? AND source = ? AND external_id = ?`, uid, a.BookID, a.Source, a.ExternalID).Scan(&id)
		if err == nil {
			a.ID = id
		} else if err != sql.ErrNoRows {
			return false, err
		}
	}
	if a.ID > 0 {
		res, err := s.DB.ExecContext(ctx, `UPDATE annotations SET kind = ?, text = ?, note = ?, color = ?, locator = ?, chapter = ?, percentage = ?, page = ?, file_id = ?, updated_at = ?
			WHERE id = ? AND user_id = ?`, a.Kind, a.Text, a.Note, a.Color, a.Locator, a.Chapter, a.Percentage, a.Page, nullI(&a.FileID), a.UpdatedAt, a.ID, uid)
		if err != nil {
			return false, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return false, ErrNotFound
		}
		return false, nil
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO annotations (user_id, book_id, file_id, kind, text, note, color, locator, chapter, percentage, page, source, external_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, uid, a.BookID, nullI(&a.FileID), a.Kind, a.Text, a.Note, a.Color, a.Locator, a.Chapter, a.Percentage, a.Page, a.Source, a.ExternalID, a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return false, err
	}
	a.ID, _ = res.LastInsertId()
	return true, nil
}

// DeleteAnnotation removes one of a person's annotations.
func (s *Store) DeleteAnnotation(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM annotations WHERE id = ? AND user_id = ?`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountAnnotations counts a person's annotations of a book.
func (s *Store) CountAnnotations(ctx context.Context, uid, bookID int64) int {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM annotations WHERE user_id = ? AND book_id = ?`, uid, bookID).Scan(&n)
	return n
}

// ============ KOReader sync positions ============

// GetKosync returns the position a device last sent for a document.
func (s *Store) GetKosync(ctx context.Context, uid int64, document string) (*KosyncProgress, error) {
	var p KosyncProgress
	var book, file sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT document, book_id, file_id, progress, percentage, device, device_id, timestamp FROM kosync_progress WHERE user_id = ? AND document = ?`,
		uid, strings.ToLower(document)).Scan(&p.Document, &book, &file, &p.Progress, &p.Percentage, &p.Device, &p.DeviceID, &p.Timestamp)
	if err != nil {
		return nil, notFound(err)
	}
	p.BookID, p.FileID = book.Int64, file.Int64
	return &p, nil
}

// SaveKosync stores a document's position exactly as the device sent it.
func (s *Store) SaveKosync(ctx context.Context, uid int64, p *KosyncProgress) error {
	p.Document = strings.ToLower(p.Document)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO kosync_progress (user_id, document, book_id, file_id, progress, percentage, device, device_id, timestamp)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, document) DO UPDATE SET book_id = COALESCE(excluded.book_id, kosync_progress.book_id), file_id = COALESCE(excluded.file_id, kosync_progress.file_id),
		  progress = excluded.progress, percentage = excluded.percentage, device = excluded.device, device_id = excluded.device_id, timestamp = excluded.timestamp`,
		uid, p.Document, nullI(&p.BookID), nullI(&p.FileID), p.Progress, p.Percentage, p.Device, p.DeviceID, p.Timestamp)
	return err
}

// KosyncOfBook returns every document position a person has for a book's files.
func (s *Store) KosyncOfBook(ctx context.Context, uid, bookID int64) ([]KosyncProgress, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT document, COALESCE(book_id, 0), COALESCE(file_id, 0), progress, percentage, device, device_id, timestamp
		FROM kosync_progress WHERE user_id = ? AND book_id = ? ORDER BY timestamp DESC`, uid, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KosyncProgress
	for rows.Next() {
		var p KosyncProgress
		if err := rows.Scan(&p.Document, &p.BookID, &p.FileID, &p.Progress, &p.Percentage, &p.Device, &p.DeviceID, &p.Timestamp); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
