package store

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// CountUsers returns the number of users.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM users`).Scan(&n)
	return n, err
}

// CreateUser inserts a user.
func (s *Store) CreateUser(ctx context.Context, u *User) error {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO users (email, name, password_hash, role, can_edit) VALUES (?, ?, ?, ?, ?)`, u.Email, u.Name, u.PasswordHash, u.Role, boolInt(u.CanEdit))
	if err != nil {
		return conflict(err)
	}
	u.ID, _ = res.LastInsertId()
	return nil
}

// UpdateUser updates name/email/password/role.
func (s *Store) UpdateUser(ctx context.Context, u *User) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET email = ?, name = ?, password_hash = ?, role = ?, can_edit = ?, updated_at = ? WHERE id = ?`, u.Email, u.Name, u.PasswordHash, u.Role, boolInt(u.CanEdit), Now(), u.ID)
	return conflict(err)
}

const userCols = `id, email, name, password_hash, role, can_edit, created_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.CanEdit, &u.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	return &u, nil
}

// GetUserByEmail finds a user by email.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE email = ?`, email))
}

// ListUsers returns all users.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// GetUser returns a user by id.
func (s *Store) GetUser(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

// DeleteUser removes a user together with everything they own (their
// reading state, progress, highlights, shelves, devices, settings, sessions, tokens):
// the foreign keys cascade. Content is personal, so nothing is left behind.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// OwnerID returns the box owner: the first admin, or the first account.
func (s *Store) OwnerID(ctx context.Context) int64 {
	var id int64
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE role = 'admin' ORDER BY id LIMIT 1`).Scan(&id); err == nil {
		return id
	}
	_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(MIN(id), 0) FROM users`).Scan(&id)
	return id
}

// CountAdmins returns how many admin accounts exist.
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM users WHERE role = 'admin'`).Scan(&n)
	return n, err
}

// RandomToken returns n random bytes hex encoded.
func RandomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// HashToken returns the sha256 hex digest of a token.
func HashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

// CreateSession creates a session for a user and returns its id.
func (s *Store) CreateSession(ctx context.Context, userID int64, ttl time.Duration) (string, error) {
	id := RandomToken(32)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO sessions (id, user_id, expires_at) VALUES (?, ?, ?)`, HashToken(id), userID, time.Now().Add(ttl).UTC().Format(time.RFC3339))
	return id, err
}

// GetSessionUser resolves a session id to a user.
func (s *Store) GetSessionUser(ctx context.Context, sessionID string) (*User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, `SELECT u.id, u.email, u.name, u.password_hash, u.role, u.can_edit, u.created_at
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.id = ? AND s.expires_at > ?`, HashToken(sessionID), time.Now().UTC().Format(time.RFC3339)))
}

// DeleteSession removes a session.
func (s *Store) DeleteSession(ctx context.Context, sessionID string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, HashToken(sessionID))
	return err
}

// PurgeSessions removes expired sessions.
func (s *Store) PurgeSessions(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, time.Now().UTC().Format(time.RFC3339))
	return err
}

// CreateAPIToken creates a token and returns the plaintext (shown once).
// kind "device" is an app password for an e-reader: OPDS signs in with it as
// a Basic password and KOReader sync with its MD5, so that is kept hashed too.
func (s *Store) CreateAPIToken(ctx context.Context, userID int64, name, kind string) (string, *APIToken, error) {
	if kind != "device" {
		kind = "token"
	}
	plain := "plb_" + RandomToken(24)
	if kind == "device" {
		// shorter and without the prefix: people type it on an e-reader keyboard
		plain = deviceSecret()
	}
	prefix := plain[:min(len(plain), 8)]
	md5h := ""
	if kind == "device" {
		md5h = HashToken(MD5Hex(plain))
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO api_tokens (user_id, name, kind, token_hash, md5_hash, prefix) VALUES (?, ?, ?, ?, ?, ?)`, userID, name, kind, HashToken(plain), md5h, prefix)
	if err != nil {
		return "", nil, err
	}
	id, _ := res.LastInsertId()
	return plain, &APIToken{ID: id, UserID: userID, Name: name, Kind: kind, Prefix: prefix, CreatedAt: Now()}, nil
}

// deviceSecret is an app password that is easy to type on an e-reader:
// four groups of four lower-case letters and digits (~80 bits).
func deviceSecret() string {
	const alphabet = "abcdefghijkmnpqrstuvwxyz23456789"
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	out := make([]byte, 0, 19)
	for i, c := range b {
		if i > 0 && i%4 == 0 {
			out = append(out, '-')
		}
		out = append(out, alphabet[int(c)%len(alphabet)])
	}
	return string(out)
}

// MD5Hex is the hex MD5 of a string (KOReader sends the MD5 of the password).
func MD5Hex(v string) string {
	h := md5.Sum([]byte(v))
	return hex.EncodeToString(h[:])
}

// GetUserByLogin resolves an e-reader sign-in: the account's email (or its
// part before the @) and one of its tokens or app passwords as the password.
// keyIsMD5 means the password arrived as its MD5 (KOReader sync).
func (s *Store) GetUserByLogin(ctx context.Context, username, password string, keyIsMD5 bool) (*User, *APIToken, error) {
	col, h := "t.token_hash", HashToken(password)
	if keyIsMD5 {
		col, h = "t.md5_hash", HashToken(strings.ToLower(password))
	}
	username = strings.ToLower(strings.TrimSpace(username))
	rows, err := s.DB.QueryContext(ctx, `SELECT u.id, u.email, u.name, u.password_hash, u.role, u.can_edit, u.created_at, t.id, t.name, t.kind
		FROM api_tokens t JOIN users u ON u.id = t.user_id WHERE `+col+` = ? AND `+col+` <> ''`, h)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var u User
		var t APIToken
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Role, &u.CanEdit, &u.CreatedAt, &t.ID, &t.Name, &t.Kind); err != nil {
			return nil, nil, err
		}
		local, _, _ := strings.Cut(u.Email, "@")
		if username == u.Email || username == local {
			t.UserID = u.ID
			rows.Close()
			_, _ = s.DB.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, Now(), t.ID)
			return &u, &t, nil
		}
	}
	return nil, nil, ErrNotFound
}

// GetUserByAPIToken resolves a bearer token.
func (s *Store) GetUserByAPIToken(ctx context.Context, token string) (*User, error) {
	h := HashToken(token)
	u, err := scanUser(s.DB.QueryRowContext(ctx, `SELECT u.id, u.email, u.name, u.password_hash, u.role, u.can_edit, u.created_at
		FROM api_tokens t JOIN users u ON u.id = t.user_id WHERE t.token_hash = ?`, h))
	if err != nil {
		return nil, err
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE token_hash = ?`, Now(), h)
	return u, nil
}

// ListAPITokens lists tokens for a user.
func (s *Store) ListAPITokens(ctx context.Context, userID int64) ([]APIToken, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, user_id, name, kind, prefix, last_used_at, created_at FROM api_tokens WHERE user_id = ? ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIToken{}
	for rows.Next() {
		var t APIToken
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.Kind, &t.Prefix, &t.LastUsedAt, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteAPIToken removes a token.
func (s *Store) DeleteAPIToken(ctx context.Context, userID, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM api_tokens WHERE id = ? AND user_id = ?`, id, userID)
	return err
}
