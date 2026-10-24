package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Queries for e-readers (OPDS, KOReader sync, the KOReader plugin, CrossPoint).

// SyncDeviceByToken returns the person's device whose app password is this token.
func (s *Store) SyncDeviceByToken(ctx context.Context, uid, tokenID int64) (*Device, error) {
	var d Device
	var tok sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT id, kind, name, email, address, format, token_id, is_default, last_seen_at, created_at
		FROM devices WHERE user_id = ? AND token_id = ? ORDER BY id LIMIT 1`, uid, tokenID).
		Scan(&d.ID, &d.Kind, &d.Name, &d.Email, &d.Address, &d.Format, &tok, &d.IsDefault, &d.LastSeenAt, &d.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	d.TokenID = tok.Int64
	return &d, nil
}

// SyncTouchDevice records that a device was just heard from.
func (s *Store) SyncTouchDevice(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE devices SET last_seen_at = ? WHERE id = ?`, Now(), id)
	return err
}

// PendingSend is a book waiting for a device to fetch it.
type PendingSend struct {
	Send
	Path    string
	FileFmt string
	Size    int64
	Authors string
}

// SyncPendingSends lists the person's books waiting to be fetched: by one
// device, or (deviceID 0) by any of their devices of the kinds given.
func (s *Store) SyncPendingSends(ctx context.Context, uid, deviceID int64, kinds ...string) ([]PendingSend, error) {
	q := `SELECT x.id, COALESCE(x.device_id, 0), x.device_name, COALESCE(x.book_id, 0), COALESCE(x.file_id, 0), x.title, x.format, x.method, x.status, x.created_at,
		f.path, f.format, f.size,
		COALESCE((SELECT group_concat(a.name, ', ') FROM book_authors ba JOIN authors a ON a.id = ba.author_id WHERE ba.book_id = x.book_id), '')
		FROM sends x JOIN files f ON f.id = x.file_id LEFT JOIN devices d ON d.id = x.device_id
		WHERE x.user_id = ? AND x.method = 'pull' AND x.status = 'queued' AND f.missing = 0`
	args := []any{uid}
	if deviceID > 0 {
		q += ` AND x.device_id = ?`
		args = append(args, deviceID)
	} else if len(kinds) > 0 {
		q += ` AND d.kind IN (` + strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ",") + `)`
		for _, k := range kinds {
			args = append(args, k)
		}
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY x.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PendingSend{}
	for rows.Next() {
		var p PendingSend
		if err := rows.Scan(&p.ID, &p.DeviceID, &p.DeviceName, &p.BookID, &p.FileID, &p.Title, &p.Format, &p.Method, &p.Status, &p.CreatedAt,
			&p.Path, &p.FileFmt, &p.Size, &p.Authors); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SyncMarkSendFetched marks one of the person's waiting sends as fetched.
func (s *Store) SyncMarkSendFetched(ctx context.Context, uid, sendID int64) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE sends SET status = 'sent', sent_at = ?, error = '' WHERE id = ? AND user_id = ? AND method = 'pull' AND status = 'queued'`, Now(), sendID, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SyncMarkBookFetched marks the waiting sends of a book to a device (0 = any) as fetched.
func (s *Store) SyncMarkBookFetched(ctx context.Context, uid, deviceID, bookID int64) (int64, error) {
	q := `UPDATE sends SET status = 'sent', sent_at = ?, error = '' WHERE user_id = ? AND book_id = ? AND method = 'pull' AND status = 'queued'`
	args := []any{Now(), uid, bookID}
	if deviceID > 0 {
		q += ` AND device_id = ?`
		args = append(args, deviceID)
	}
	res, err := s.DB.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SyncCoverFile returns a book's stored cover file name (” = none).
func (s *Store) SyncCoverFile(ctx context.Context, bookID int64) (string, error) {
	var c string
	err := s.DB.QueryRowContext(ctx, `SELECT cover FROM books WHERE id = ?`, bookID).Scan(&c)
	return c, notFound(err)
}

// SyncDeleteEstimatedSessions removes the person's guessed sessions of a book
// that overlap a stretch of time, before measured ones (KOReader's own
// statistics) replace them.
func (s *Store) SyncDeleteEstimatedSessions(ctx context.Context, uid, bookID int64, from, to time.Time) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM reading_sessions WHERE user_id = ? AND book_id = ? AND estimated = 1 AND source = 'kosync'
		AND ended_at >= ? AND started_at <= ?`, uid, bookID, from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano))
	return err
}

// ---- CrossPoint clippings ----

// Clipping is a CrossPoint clipping as stored.
type Clipping struct {
	Seq          int64
	Document     string
	ClipID       string
	BookID       int64
	AnnotationID int64
	Item         string // JSON as the device sent it (plus note/colour kept)
	Deleted      bool
	UpdatedUnix  int64
}

func scanClipping(row interface{ Scan(...any) error }) (*Clipping, error) {
	var c Clipping
	var book, ann sql.NullInt64
	if err := row.Scan(&c.Seq, &c.Document, &c.ClipID, &book, &ann, &c.Item, &c.Deleted, &c.UpdatedUnix); err != nil {
		return nil, notFound(err)
	}
	c.BookID, c.AnnotationID = book.Int64, ann.Int64
	return &c, nil
}

const clipCols = `seq, document, clip_id, book_id, annotation_id, item, deleted, updated_unix`

// GetClipping returns one stored clipping (or its tombstone).
func (s *Store) GetClipping(ctx context.Context, uid int64, document, clipID string) (*Clipping, error) {
	return scanClipping(s.DB.QueryRowContext(ctx, `SELECT `+clipCols+` FROM crosspoint_clippings WHERE user_id = ? AND document = ? AND clip_id = ?`, uid, document, clipID))
}

// PutClipping stores a clipping (or a tombstone) with a fresh sequence number.
func (s *Store) PutClipping(ctx context.Context, uid int64, c *Clipping) error {
	c.UpdatedUnix = time.Now().Unix()
	res, err := s.DB.ExecContext(ctx, `INSERT OR REPLACE INTO crosspoint_clippings (user_id, document, clip_id, book_id, annotation_id, item, deleted, updated_unix)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, uid, c.Document, c.ClipID, nullI(&c.BookID), nullI(&c.AnnotationID), c.Item, boolInt(c.Deleted), c.UpdatedUnix)
	if err != nil {
		return err
	}
	c.Seq, _ = res.LastInsertId()
	return nil
}

// ClippingsAfter lists a document's clippings changed after a cursor, in order;
// more says whether there are others after the last one returned.
func (s *Store) ClippingsAfter(ctx context.Context, uid int64, document string, cursor int64, limit int) ([]Clipping, bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+clipCols+` FROM crosspoint_clippings WHERE user_id = ? AND document = ? AND seq > ? ORDER BY seq LIMIT ?`, uid, document, cursor, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []Clipping
	for rows.Next() {
		c, err := scanClipping(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, *c)
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, rows.Err()
}

// ClippingsSince lists a document's clippings changed after a unix time (the legacy cursor).
func (s *Store) ClippingsSince(ctx context.Context, uid int64, document string, since int64, limit int) ([]Clipping, bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+clipCols+` FROM crosspoint_clippings WHERE user_id = ? AND document = ? AND updated_unix > ? ORDER BY updated_unix, seq LIMIT ?`, uid, document, since, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []Clipping
	for rows.Next() {
		c, err := scanClipping(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, *c)
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, rows.Err()
}
