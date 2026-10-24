package store

import (
	"context"
	"encoding/json"
	"strings"
)

// ============ devices ============

const deviceCols = `d.id, d.kind, d.name, d.email, d.address, d.format, COALESCE(d.token_id, 0), d.settings, d.is_default, d.last_seen_at, d.created_at,
	(SELECT COUNT(1) FROM sends se WHERE se.device_id = d.id AND se.status = 'queued' AND se.method = 'pull')`

func scanDevice(row interface{ Scan(...any) error }) (*Device, error) {
	var d Device
	var settings string
	if err := row.Scan(&d.ID, &d.Kind, &d.Name, &d.Email, &d.Address, &d.Format, &d.TokenID, &settings, &d.IsDefault, &d.LastSeenAt, &d.CreatedAt, &d.Pending); err != nil {
		return nil, notFound(err)
	}
	_ = json.Unmarshal([]byte(settings), &d.Settings)
	if d.Settings == nil {
		d.Settings = map[string]any{}
	}
	return &d, nil
}

// ListDevices returns a person's devices, the default first.
func (s *Store) ListDevices(ctx context.Context, uid int64) ([]Device, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+deviceCols+` FROM devices d WHERE d.user_id = ? ORDER BY d.is_default DESC, d.id`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// GetDevice returns one of a person's devices.
func (s *Store) GetDevice(ctx context.Context, uid, id int64) (*Device, error) {
	return scanDevice(s.DB.QueryRowContext(ctx, `SELECT `+deviceCols+` FROM devices d WHERE d.id = ? AND d.user_id = ?`, id, uid))
}

func deviceSettingsJSON(d *Device) string {
	if d.Settings == nil {
		d.Settings = map[string]any{}
	}
	b, _ := json.Marshal(d.Settings)
	return string(b)
}

// CreateDevice adds a device for a person. The first device is the default.
func (s *Store) CreateDevice(ctx context.Context, uid int64, d *Device) error {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM devices WHERE user_id = ?`, uid).Scan(&n)
	if n == 0 {
		d.IsDefault = true
	}
	if d.IsDefault {
		if _, err := s.DB.ExecContext(ctx, `UPDATE devices SET is_default = 0 WHERE user_id = ?`, uid); err != nil {
			return err
		}
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO devices (user_id, kind, name, email, address, format, token_id, settings, is_default) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uid, d.Kind, strings.TrimSpace(d.Name), strings.TrimSpace(d.Email), strings.TrimSpace(d.Address), d.Format, nullI(&d.TokenID), deviceSettingsJSON(d), boolInt(d.IsDefault))
	if err != nil {
		return err
	}
	d.ID, _ = res.LastInsertId()
	d.CreatedAt = Now()
	return nil
}

// UpdateDevice saves a person's device (name, address, format, settings).
func (s *Store) UpdateDevice(ctx context.Context, uid int64, d *Device) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE devices SET name = ?, email = ?, address = ?, format = ?, token_id = ?, settings = ? WHERE id = ? AND user_id = ?`,
		strings.TrimSpace(d.Name), strings.TrimSpace(d.Email), strings.TrimSpace(d.Address), d.Format, nullI(&d.TokenID), deviceSettingsJSON(d), d.ID, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetDefaultDevice makes one of a person's devices the default.
func (s *Store) SetDefaultDevice(ctx context.Context, uid, id int64) error {
	if _, err := s.GetDevice(ctx, uid, id); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE devices SET is_default = CASE WHEN id = ? THEN 1 ELSE 0 END WHERE user_id = ?`, id, uid); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteDevice removes a person's device and its app password. Another
// device becomes the default when the default goes.
func (s *Store) DeleteDevice(ctx context.Context, uid, id int64) error {
	d, err := s.GetDevice(ctx, uid, id)
	if err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM devices WHERE id = ? AND user_id = ?`, id, uid); err != nil {
		return err
	}
	if d.TokenID > 0 {
		_, _ = s.DB.ExecContext(ctx, `DELETE FROM api_tokens WHERE id = ? AND user_id = ?`, d.TokenID, uid)
	}
	if d.IsDefault {
		_, _ = s.DB.ExecContext(ctx, `UPDATE devices SET is_default = 1 WHERE id = (SELECT id FROM devices WHERE user_id = ? ORDER BY id LIMIT 1)`, uid)
	}
	return nil
}

// DefaultDevice returns the person's default device (or their only one).
func (s *Store) DefaultDevice(ctx context.Context, uid int64) (*Device, error) {
	return scanDevice(s.DB.QueryRowContext(ctx, `SELECT `+deviceCols+` FROM devices d WHERE d.user_id = ? ORDER BY d.is_default DESC, d.id LIMIT 1`, uid))
}

// DeviceByEmail finds a person's device that mails to an address.
func (s *Store) DeviceByEmail(ctx context.Context, uid int64, email string) (*Device, error) {
	return scanDevice(s.DB.QueryRowContext(ctx, `SELECT `+deviceCols+` FROM devices d WHERE d.user_id = ? AND lower(d.email) = lower(?) LIMIT 1`, uid, strings.TrimSpace(email)))
}

// ============ sends ============

const sendCols = `se.id, COALESCE(se.device_id, 0), se.device_name, COALESCE(se.book_id, 0), COALESCE(se.file_id, 0), se.title, se.format, se.method, se.status, se.error, se.bytes, se.created_at, se.sent_at,
	COALESCE((SELECT b.cover FROM books b WHERE b.id = se.book_id), '')`

func scanSend(row interface{ Scan(...any) error }) (*Send, int64, error) {
	var x Send
	var uid int64
	var cover string
	if err := row.Scan(&uid, &x.ID, &x.DeviceID, &x.DeviceName, &x.BookID, &x.FileID, &x.Title, &x.Format, &x.Method, &x.Status, &x.Error, &x.Bytes, &x.CreatedAt, &x.SentAt, &cover); err != nil {
		return nil, 0, notFound(err)
	}
	if cover != "" && x.BookID > 0 {
		x.Cover = CoverURL(x.BookID, cover)
	}
	return &x, uid, nil
}

// CreateSend records a book on its way to a device.
func (s *Store) CreateSend(ctx context.Context, uid int64, x *Send) error {
	if x.Status == "" {
		x.Status = "queued"
	}
	x.CreatedAt = Now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO sends (user_id, device_id, device_name, book_id, file_id, title, format, method, status, error, bytes, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, uid, nullI(&x.DeviceID), x.DeviceName, nullI(&x.BookID), nullI(&x.FileID), x.Title, x.Format, x.Method, x.Status, x.Error, x.Bytes, x.CreatedAt)
	if err != nil {
		return err
	}
	x.ID, _ = res.LastInsertId()
	return nil
}

// GetSend returns one of a person's sends.
func (s *Store) GetSend(ctx context.Context, uid, id int64) (*Send, error) {
	x, _, err := scanSend(s.DB.QueryRowContext(ctx, `SELECT se.user_id, `+sendCols+` FROM sends se WHERE se.id = ? AND se.user_id = ?`, id, uid))
	return x, err
}

// ListSends returns a person's sends, newest first (optionally of one book).
func (s *Store) ListSends(ctx context.Context, uid, bookID int64, limit int) ([]Send, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT se.user_id, ` + sendCols + ` FROM sends se WHERE se.user_id = ?`
	args := []any{uid}
	if bookID > 0 {
		q += ` AND se.book_id = ?`
		args = append(args, bookID)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY se.id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Send{}
	for rows.Next() {
		x, _, err := scanSend(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *x)
	}
	return out, rows.Err()
}

// NextSend returns the oldest send the server delivers itself (mail or push)
// that is waiting, with the person it belongs to.
func (s *Store) NextSend(ctx context.Context) (*Send, int64, error) {
	return scanSend(s.DB.QueryRowContext(ctx, `SELECT se.user_id, `+sendCols+` FROM sends se WHERE se.status = 'queued' AND se.method IN ('email', 'push') ORDER BY se.id LIMIT 1`))
}

// SetSendState updates a send's progress.
func (s *Store) SetSendState(ctx context.Context, id int64, method, status, errText string, bytes int64) error {
	sentAt := ""
	if status == "sent" {
		sentAt = Now()
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE sends SET method = ?, status = ?, error = ?, bytes = CASE WHEN ? > 0 THEN ? ELSE bytes END, sent_at = ? WHERE id = ?`,
		method, status, errText, bytes, bytes, sentAt, id)
	return err
}

// RequeueStuckSends puts sends that were being delivered when the server
// stopped back in the queue.
func (s *Store) RequeueStuckSends(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE sends SET status = 'queued' WHERE status = 'sending'`)
	return err
}

// CancelSend cancels one of a person's waiting sends.
func (s *Store) CancelSend(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE sends SET status = 'cancelled' WHERE id = ? AND user_id = ? AND status = 'queued'`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := s.GetSend(ctx, uid, id); err != nil {
			return err
		}
		return ErrConflict
	}
	return nil
}
