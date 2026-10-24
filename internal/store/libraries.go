package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// CountLibraries returns how many libraries exist.
func (s *Store) CountLibraries(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM libraries`).Scan(&n)
	return n, err
}

func cleanLibrary(l *Library) {
	l.Name = strings.TrimSpace(l.Name)
	switch l.Kind {
	case "books", "comics", "manga":
	default:
		l.Kind = "books"
	}
	if l.Access != "people" {
		l.Access = "all"
	}
	var ps []string
	for _, p := range l.Paths {
		if p = strings.TrimRight(strings.TrimSpace(p), "/"); p != "" {
			ps = append(ps, p)
		}
	}
	l.Paths = ps
	if strings.TrimSpace(l.UploadTemplate) == "" {
		l.UploadTemplate = "{author}/{author} - {title}"
	}
	if l.ScanEvery < 0 {
		l.ScanEvery = 0
	}
}

// CreateLibrary inserts a library (and its people). Only one library takes uploads.
func (s *Store) CreateLibrary(ctx context.Context, l *Library) error {
	cleanLibrary(l)
	paths, _ := json.Marshal(l.Paths)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if l.Uploads {
		if _, err := tx.ExecContext(ctx, `UPDATE libraries SET uploads = 0`); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO libraries (name, kind, paths, access, uploads, upload_template, scan_every, position) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		l.Name, l.Kind, string(paths), l.Access, boolInt(l.Uploads), l.UploadTemplate, l.ScanEvery, l.Position)
	if err != nil {
		return conflict(err)
	}
	l.ID, _ = res.LastInsertId()
	if err := setLibraryPeople(ctx, tx, l.ID, l.People); err != nil {
		return err
	}
	return tx.Commit()
}

func setLibraryPeople(ctx context.Context, tx *sql.Tx, id int64, people []int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM library_people WHERE library_id = ?`, id); err != nil {
		return err
	}
	for _, uid := range people {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO library_people (library_id, user_id) VALUES (?, ?)`, id, uid); err != nil {
			return err
		}
	}
	return nil
}

// UpdateLibrary saves a library's settings and people.
func (s *Store) UpdateLibrary(ctx context.Context, l *Library) error {
	cleanLibrary(l)
	paths, _ := json.Marshal(l.Paths)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if l.Uploads {
		if _, err := tx.ExecContext(ctx, `UPDATE libraries SET uploads = 0 WHERE id <> ?`, l.ID); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE libraries SET name = ?, kind = ?, paths = ?, access = ?, uploads = ?, upload_template = ?, scan_every = ?, position = ? WHERE id = ?`,
		l.Name, l.Kind, string(paths), l.Access, boolInt(l.Uploads), l.UploadTemplate, l.ScanEvery, l.Position, l.ID)
	if err != nil {
		return conflict(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if err := setLibraryPeople(ctx, tx, l.ID, l.People); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteLibrary removes a library and its catalogue (books, files, everyone's
// progress in them). The files on disk are not touched.
func (s *Store) DeleteLibrary(ctx context.Context, id int64) error {
	ids, err := s.queryIDs(ctx, `SELECT id FROM books WHERE library_id = ?`, id)
	if err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM libraries WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	for _, b := range ids {
		_, _ = s.DB.ExecContext(ctx, `DELETE FROM books_fts WHERE rowid = ?`, b)
	}
	_ = s.PruneOrphans(ctx)
	return nil
}

const libCols = `id, name, kind, paths, access, uploads, upload_template, scan_every, position, last_scan_at, last_scan_ms, last_scan_error, created_at,
	(SELECT COUNT(1) FROM books b WHERE b.library_id = libraries.id AND b.missing = 0)`

func scanLibrary(row interface{ Scan(...any) error }) (*Library, error) {
	var l Library
	var paths string
	if err := row.Scan(&l.ID, &l.Name, &l.Kind, &paths, &l.Access, &l.Uploads, &l.UploadTemplate, &l.ScanEvery, &l.Position,
		&l.LastScanAt, &l.LastScanMS, &l.LastScanError, &l.CreatedAt, &l.Books); err != nil {
		return nil, notFound(err)
	}
	_ = json.Unmarshal([]byte(paths), &l.Paths)
	if l.Paths == nil {
		l.Paths = []string{}
	}
	l.People = []int64{}
	return &l, nil
}

// GetLibrary returns one library with its people.
func (s *Store) GetLibrary(ctx context.Context, id int64) (*Library, error) {
	l, err := scanLibrary(s.DB.QueryRowContext(ctx, `SELECT `+libCols+` FROM libraries WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	l.People, _ = s.queryIDs(ctx, `SELECT user_id FROM library_people WHERE library_id = ? ORDER BY user_id`, id)
	if l.People == nil {
		l.People = []int64{}
	}
	return l, nil
}

// ListLibraries returns every library in order, with people.
func (s *Store) ListLibraries(ctx context.Context) ([]Library, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+libCols+` FROM libraries ORDER BY position, id`)
	if err != nil {
		return nil, err
	}
	var out []Library
	for rows.Next() {
		l, err := scanLibrary(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, *l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].People, _ = s.queryIDs(ctx, `SELECT user_id FROM library_people WHERE library_id = ? ORDER BY user_id`, out[i].ID)
		if out[i].People == nil {
			out[i].People = []int64{}
		}
	}
	return out, nil
}

// LibrariesFor returns the libraries a person may open: every library for an
// admin, else those open to all plus those they were given.
func (s *Store) LibrariesFor(ctx context.Context, u *User) ([]Library, error) {
	all, err := s.ListLibraries(ctx)
	if err != nil {
		return nil, err
	}
	if u != nil && u.Role == "admin" {
		return all, nil
	}
	var out []Library
	for _, l := range all {
		if l.Access == "all" {
			out = append(out, l)
			continue
		}
		for _, p := range l.People {
			if u != nil && p == u.ID {
				out = append(out, l)
				break
			}
		}
	}
	return out, nil
}

// LibraryIDsFor is LibrariesFor as ids.
func (s *Store) LibraryIDsFor(ctx context.Context, u *User) ([]int64, error) {
	libs, err := s.LibrariesFor(ctx, u)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(libs))
	for _, l := range libs {
		ids = append(ids, l.ID)
	}
	return ids, nil
}

// UploadLibrary returns the library uploads are filed into (the first one when none is marked).
func (s *Store) UploadLibrary(ctx context.Context) (*Library, error) {
	var id int64
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM libraries ORDER BY uploads DESC, position, id LIMIT 1`).Scan(&id); err != nil {
		return nil, notFound(err)
	}
	return s.GetLibrary(ctx, id)
}

// SetScanResult records how a library's last scan went.
func (s *Store) SetScanResult(ctx context.Context, id int64, at string, ms int64, errText string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE libraries SET last_scan_at = ?, last_scan_ms = ?, last_scan_error = ? WHERE id = ?`, at, ms, errText, id)
	return err
}

// CatalogCounts are box-wide catalogue numbers for the About page.
func (s *Store) CatalogCounts(ctx context.Context) map[string]int {
	out := map[string]int{}
	for k, q := range map[string]string{
		"libraries": `SELECT COUNT(1) FROM libraries`,
		"books":     `SELECT COUNT(1) FROM books WHERE missing = 0`,
		"files":     `SELECT COUNT(1) FROM files WHERE missing = 0`,
		"authors":   `SELECT COUNT(1) FROM authors`,
		"series":    `SELECT COUNT(1) FROM series`,
	} {
		var n int
		if err := s.DB.QueryRowContext(ctx, q).Scan(&n); err == nil {
			out[k] = n
		}
	}
	return out
}

func (s *Store) queryIDs(ctx context.Context, q string, args ...any) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
