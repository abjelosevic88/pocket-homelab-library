package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// GetUserBook returns where a person stands with a book (zero value when nowhere yet).
func (s *Store) GetUserBook(ctx context.Context, uid, bookID int64) (*UserBook, error) {
	var ub UserBook
	var fileID sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT status, rating, favorite, hidden, percentage, locator, file_id, last_read_at, last_device, started_at, finished_at, times_read, updated_at
		FROM user_books WHERE user_id = ? AND book_id = ?`, uid, bookID).Scan(&ub.Status, &ub.Rating, &ub.Favorite, &ub.Hidden, &ub.Percentage, &ub.Locator,
		&fileID, &ub.LastReadAt, &ub.LastDevice, &ub.StartedAt, &ub.FinishedAt, &ub.TimesRead, &ub.UpdatedAt)
	if err == sql.ErrNoRows {
		return &UserBook{}, nil
	}
	ub.FileID = fileID.Int64
	return &ub, err
}

func (s *Store) saveUserBook(ctx context.Context, uid, bookID int64, ub *UserBook) error {
	ub.UpdatedAt = Now()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO user_books (user_id, book_id, status, rating, favorite, hidden, percentage, locator, file_id, last_read_at, last_device, started_at, finished_at, times_read, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, book_id) DO UPDATE SET status = excluded.status, rating = excluded.rating, favorite = excluded.favorite, hidden = excluded.hidden,
		  percentage = excluded.percentage, locator = excluded.locator, file_id = excluded.file_id, last_read_at = excluded.last_read_at, last_device = excluded.last_device,
		  started_at = excluded.started_at, finished_at = excluded.finished_at, times_read = excluded.times_read, updated_at = excluded.updated_at`,
		uid, bookID, ub.Status, ub.Rating, boolInt(ub.Favorite), boolInt(ub.Hidden), ub.Percentage, ub.Locator, nullI(&ub.FileID),
		ub.LastReadAt, ub.LastDevice, ub.StartedAt, ub.FinishedAt, ub.TimesRead, ub.UpdatedAt)
	return err
}

// StatusChange tells the caller what a change of status or progress did.
type StatusChange struct {
	Started  bool // the book went to "reading"
	Finished bool // a read was recorded
}

// SetStatus moves a book to a status. Finishing (read/dnf) records a read on
// the day given (” = today); starting stamps started_at.
func (s *Store) SetStatus(ctx context.Context, uid, bookID int64, status, day string) (*UserBook, StatusChange, error) {
	var ch StatusChange
	ub, err := s.GetUserBook(ctx, uid, bookID)
	if err != nil {
		return nil, ch, err
	}
	switch status {
	case "", "want", "reading", "read", "dnf":
	default:
		return nil, ch, ErrNotFound
	}
	prev := ub.Status
	now := Now()
	ub.Status = status
	switch status {
	case "reading":
		if prev != "reading" {
			ub.StartedAt, ub.FinishedAt = now, ""
			ch.Started = true
			if prev == "read" || prev == "dnf" {
				ub.Percentage, ub.Locator = 0, ""
			}
		}
	case "read", "dnf":
		if prev != status {
			finished := now
			if day != "" && day != Today() {
				finished = day + "T12:00:00Z"
			}
			if day == "" {
				day = Today()
			}
			ub.FinishedAt = finished
			if status == "read" {
				ub.TimesRead++
				ub.Percentage = 1
			}
			if _, err := s.DB.ExecContext(ctx, `INSERT INTO reads (user_id, book_id, status, started_at, finished_at, day) VALUES (?, ?, ?, ?, ?, ?)`,
				uid, bookID, status, ub.StartedAt, finished, day); err != nil {
				return nil, ch, err
			}
			ch.Finished = true
		}
	}
	return ub, ch, s.saveUserBook(ctx, uid, bookID, ub)
}

// UpdateUserBook changes rating, favourite and hidden.
func (s *Store) UpdateUserBook(ctx context.Context, uid, bookID int64, rating *int, favorite, hidden *bool) (*UserBook, error) {
	ub, err := s.GetUserBook(ctx, uid, bookID)
	if err != nil {
		return nil, err
	}
	if rating != nil && *rating >= 0 && *rating <= 10 {
		ub.Rating = *rating
	}
	if favorite != nil {
		ub.Favorite = *favorite
	}
	if hidden != nil {
		ub.Hidden = *hidden
	}
	return ub, s.saveUserBook(ctx, uid, bookID, ub)
}

// Progress is a reading position report.
type Progress struct {
	FileID     int64
	Percentage float64 // 0..1
	Locator    string  // JSON
	Device     string
	At         time.Time // when it was read there ('' = now)
	// AutoFinish marks the book read when the position reaches the end (≥ 0.995).
	AutoFinish bool
}

// SetProgress records a position. A book not started yet moves to reading.
// An older report than the one stored (from another device) is ignored and
// reported as stale.
func (s *Store) SetProgress(ctx context.Context, uid, bookID int64, p Progress) (*UserBook, StatusChange, bool, error) {
	var ch StatusChange
	ub, err := s.GetUserBook(ctx, uid, bookID)
	if err != nil {
		return nil, ch, false, err
	}
	at := p.At
	if at.IsZero() {
		at = time.Now()
	}
	stamp := at.UTC().Format(time.RFC3339Nano)
	if ub.LastReadAt != "" && stamp < ub.LastReadAt {
		return ub, ch, true, nil
	}
	if p.Percentage < 0 {
		p.Percentage = 0
	}
	if p.Percentage > 1 {
		p.Percentage = 1
	}
	ub.Percentage, ub.Locator, ub.LastReadAt = p.Percentage, p.Locator, stamp
	if p.FileID > 0 {
		ub.FileID = p.FileID
	}
	if p.Device != "" {
		ub.LastDevice = p.Device
	}
	if (ub.Status == "" || ub.Status == "want") && p.Percentage > 0 {
		ub.Status, ub.StartedAt = "reading", stamp
		ch.Started = true
	}
	if err := s.saveUserBook(ctx, uid, bookID, ub); err != nil {
		return nil, ch, false, err
	}
	if p.AutoFinish && p.Percentage >= 0.995 && ub.Status == "reading" {
		var fch StatusChange
		ub, fch, err = s.SetStatus(ctx, uid, bookID, "read", at.Format("2006-01-02"))
		ch.Finished = fch.Finished
	}
	return ub, ch, false, err
}

// Reads lists a person's finished reads (newest first), optionally between two days.
func (s *Store) Reads(ctx context.Context, uid int64, bookID int64, from, to string) ([]Read, error) {
	q := `SELECT id, book_id, status, started_at, finished_at, day FROM reads WHERE user_id = ?`
	args := []any{uid}
	if bookID > 0 {
		q += ` AND book_id = ?`
		args = append(args, bookID)
	}
	if from != "" {
		q += ` AND day >= ?`
		args = append(args, from)
	}
	if to != "" {
		q += ` AND day <= ?`
		args = append(args, to)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY finished_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Read{}
	for rows.Next() {
		var r Read
		if err := rows.Scan(&r.ID, &r.BookID, &r.Status, &r.StartedAt, &r.FinishedAt, &r.Day); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteRead removes one of a person's reads.
func (s *Store) DeleteRead(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM reads WHERE id = ? AND user_id = ?`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ============ shelves ============

const shelfCols = `id, name, description, icon, kind, filter, ordered, opds, position, created_at, updated_at,
	(SELECT COUNT(1) FROM shelf_books sb WHERE sb.shelf_id = shelves.id)`

func scanShelf(row interface{ Scan(...any) error }) (*Shelf, error) {
	var sh Shelf
	if err := row.Scan(&sh.ID, &sh.Name, &sh.Description, &sh.Icon, &sh.Kind, &sh.Filter, &sh.Ordered, &sh.OPDS, &sh.Position, &sh.CreatedAt, &sh.UpdatedAt, &sh.Books); err != nil {
		return nil, notFound(err)
	}
	sh.Covers = []string{}
	return &sh, nil
}

// ListShelves returns a person's shelves in their order.
func (s *Store) ListShelves(ctx context.Context, uid int64) ([]Shelf, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+shelfCols+` FROM shelves WHERE user_id = ? ORDER BY position, name COLLATE NOCASE`, uid)
	if err != nil {
		return nil, err
	}
	var out []Shelf
	for rows.Next() {
		sh, err := scanShelf(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, *sh)
	}
	rows.Close()
	for i := range out {
		if out[i].Kind == "manual" {
			out[i].Covers = s.shelfCovers(ctx, out[i].ID)
		}
	}
	if out == nil {
		out = []Shelf{}
	}
	return out, rows.Err()
}

func (s *Store) shelfCovers(ctx context.Context, shelfID int64) []string {
	rows, err := s.DB.QueryContext(ctx, `SELECT b.id, b.cover FROM shelf_books sb JOIN books b ON b.id = sb.book_id WHERE sb.shelf_id = ? AND b.cover <> '' ORDER BY sb.position, sb.added_at DESC LIMIT 3`, shelfID)
	if err != nil {
		return []string{}
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id int64
		var c string
		if rows.Scan(&id, &c) == nil {
			out = append(out, CoverURL(id, c))
		}
	}
	return out
}

// GetShelf returns one of a person's shelves.
func (s *Store) GetShelf(ctx context.Context, uid, id int64) (*Shelf, error) {
	sh, err := scanShelf(s.DB.QueryRowContext(ctx, `SELECT `+shelfCols+` FROM shelves WHERE id = ? AND user_id = ?`, id, uid))
	if err != nil {
		return nil, err
	}
	if sh.Kind == "manual" {
		sh.Covers = s.shelfCovers(ctx, id)
	}
	return sh, nil
}

func cleanShelf(sh *Shelf) {
	sh.Name = strings.TrimSpace(sh.Name)
	if sh.Kind != "smart" {
		sh.Kind = "manual"
	}
	if sh.Filter == "" {
		sh.Filter = "{}"
	}
}

// CreateShelf adds a shelf for a person.
func (s *Store) CreateShelf(ctx context.Context, uid int64, sh *Shelf) error {
	cleanShelf(sh)
	var pos int
	_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), 0) + 1 FROM shelves WHERE user_id = ?`, uid).Scan(&pos)
	res, err := s.DB.ExecContext(ctx, `INSERT INTO shelves (user_id, name, description, icon, kind, filter, ordered, opds, position) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uid, sh.Name, sh.Description, sh.Icon, sh.Kind, sh.Filter, boolInt(sh.Ordered), boolInt(sh.OPDS), pos)
	if err != nil {
		return conflict(err)
	}
	sh.ID, _ = res.LastInsertId()
	sh.Position = pos
	return nil
}

// UpdateShelf saves a person's shelf.
func (s *Store) UpdateShelf(ctx context.Context, uid int64, sh *Shelf) error {
	cleanShelf(sh)
	res, err := s.DB.ExecContext(ctx, `UPDATE shelves SET name = ?, description = ?, icon = ?, filter = ?, ordered = ?, opds = ?, updated_at = ? WHERE id = ? AND user_id = ?`,
		sh.Name, sh.Description, sh.Icon, sh.Filter, boolInt(sh.Ordered), boolInt(sh.OPDS), Now(), sh.ID, uid)
	if err != nil {
		return conflict(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteShelf removes a person's shelf (the books stay).
func (s *Store) DeleteShelf(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM shelves WHERE id = ? AND user_id = ?`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ReorderShelves sets the order of a person's shelves.
func (s *Store) ReorderShelves(ctx context.Context, uid int64, ids []int64) error {
	for i, id := range ids {
		if _, err := s.DB.ExecContext(ctx, `UPDATE shelves SET position = ? WHERE id = ? AND user_id = ?`, i+1, id, uid); err != nil {
			return err
		}
	}
	return nil
}

// AddToShelf puts books on a person's hand-picked shelf (at the end).
func (s *Store) AddToShelf(ctx context.Context, uid, shelfID int64, bookIDs []int64) error {
	sh, err := s.GetShelf(ctx, uid, shelfID)
	if err != nil {
		return err
	}
	if sh.Kind != "manual" {
		return ErrNotFound
	}
	var pos int
	_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), 0) FROM shelf_books WHERE shelf_id = ?`, shelfID).Scan(&pos)
	for _, b := range bookIDs {
		pos++
		if _, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO shelf_books (shelf_id, book_id, position) VALUES (?, ?, ?)`, shelfID, b, pos); err != nil {
			return err
		}
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE shelves SET updated_at = ? WHERE id = ?`, Now(), shelfID)
	return nil
}

// RemoveFromShelf takes books off a person's shelf.
func (s *Store) RemoveFromShelf(ctx context.Context, uid, shelfID int64, bookIDs []int64) error {
	if _, err := s.GetShelf(ctx, uid, shelfID); err != nil {
		return err
	}
	for _, b := range bookIDs {
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM shelf_books WHERE shelf_id = ? AND book_id = ?`, shelfID, b); err != nil {
			return err
		}
	}
	return nil
}

// ReorderShelf sets the order of books on a person's shelf.
func (s *Store) ReorderShelf(ctx context.Context, uid, shelfID int64, bookIDs []int64) error {
	if _, err := s.GetShelf(ctx, uid, shelfID); err != nil {
		return err
	}
	for i, b := range bookIDs {
		if _, err := s.DB.ExecContext(ctx, `UPDATE shelf_books SET position = ? WHERE shelf_id = ? AND book_id = ?`, i+1, shelfID, b); err != nil {
			return err
		}
	}
	return nil
}

// ShelvesOfBook returns the ids of a person's shelves a book is on.
func (s *Store) ShelvesOfBook(ctx context.Context, uid, bookID int64) ([]int64, error) {
	ids, err := s.queryIDs(ctx, `SELECT sb.shelf_id FROM shelf_books sb JOIN shelves sh ON sh.id = sb.shelf_id WHERE sh.user_id = ? AND sb.book_id = ?`, uid, bookID)
	if ids == nil {
		ids = []int64{}
	}
	return ids, err
}

// ChangedUserBooks returns the books whose state a person changed since a
// moment (” = all), newest first.
func (s *Store) ChangedUserBooks(ctx context.Context, uid int64, since string, limit int) ([]int64, error) {
	ids, err := s.queryIDs(ctx, `SELECT book_id FROM user_books WHERE user_id = ? AND updated_at >= ? ORDER BY updated_at DESC LIMIT ?`, uid, since, limit)
	if ids == nil {
		ids = []int64{}
	}
	return ids, err
}
