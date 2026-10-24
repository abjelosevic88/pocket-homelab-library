package store

import (
	"context"
	"database/sql"
	"strings"
)

// Queries for scanning, covers, metadata lookups and duplicates.

// BookCoverFile returns a book's stored cover file name and its library.
func (s *Store) BookCoverFile(ctx context.Context, id int64) (name string, libraryID int64, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT cover, library_id FROM books WHERE id = ?`, id).Scan(&name, &libraryID)
	return name, libraryID, notFound(err)
}

// BookIDByPath returns the book a file path belongs to.
func (s *Store) BookIDByPath(ctx context.Context, path string) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT book_id FROM files WHERE path = ?`, path).Scan(&id)
	return id, notFound(err)
}

// CountFilesWithoutHash counts files still waiting for their whole-file hash.
func (s *Store) CountFilesWithoutHash(ctx context.Context) int {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM files WHERE sha256 = '' AND missing = 0`).Scan(&n)
	return n
}

// CountAuthorsToEnrich counts authors not looked up yet.
func (s *Store) CountAuthorsToEnrich(ctx context.Context) int {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM authors WHERE enriched_at = ''`).Scan(&n)
	return n
}

// ResetAuthorEnrichment makes an author be looked up again.
func (s *Store) ResetAuthorEnrichment(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE authors SET enriched_at = '' WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AuthorName returns an author's name.
func (s *Store) AuthorName(ctx context.Context, id int64) (string, error) {
	var n string
	err := s.DB.QueryRowContext(ctx, `SELECT name FROM authors WHERE id = ?`, id).Scan(&n)
	return n, notFound(err)
}

// BooksMissingMeta returns books (present on disk) without a description or
// a cover, optionally in one library, oldest first.
func (s *Store) BooksMissingMeta(ctx context.Context, libraryID int64, limit int) ([]int64, error) {
	q := `SELECT id FROM books WHERE missing = 0 AND (description = '' OR cover = '')`
	args := []any{}
	if libraryID > 0 {
		q += ` AND library_id = ?`
		args = append(args, libraryID)
	}
	q += ` ORDER BY id LIMIT ?`
	return s.queryIDs(ctx, q, append(args, limit)...)
}

// DupFileBooks returns groups of books whose files have the same contents.
func (s *Store) DupFileBooks(ctx context.Context, libraries []int64, limit int) ([][]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT f.sha256, GROUP_CONCAT(DISTINCT f.book_id) FROM files f JOIN books b ON b.id = f.book_id
		WHERE f.sha256 <> '' AND f.missing = 0 AND b.library_id IN `+inInts(libraries)+`
		GROUP BY f.sha256 HAVING COUNT(DISTINCT f.book_id) > 1 LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	return scanIDGroups(rows)
}

// DupISBNBooks returns groups of books sharing an ISBN.
func (s *Store) DupISBNBooks(ctx context.Context, libraries []int64, limit int) ([][]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT isbn, GROUP_CONCAT(id) FROM books WHERE isbn <> '' AND missing = 0 AND library_id IN `+inInts(libraries)+`
		GROUP BY isbn HAVING COUNT(1) > 1 LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	return scanIDGroups(rows)
}

func scanIDGroups(rows *sql.Rows) ([][]int64, error) {
	defer rows.Close()
	var out [][]int64
	for rows.Next() {
		var k, list string
		if err := rows.Scan(&k, &list); err != nil {
			return nil, err
		}
		var ids []int64
		for _, p := range strings.Split(list, ",") {
			var id int64
			for _, c := range p {
				id = id*10 + int64(c-'0')
			}
			if id > 0 {
				ids = append(ids, id)
			}
		}
		out = append(out, ids)
	}
	return out, rows.Err()
}

// TitleKey is a book's title and first author, for finding the same title twice.
type TitleKey struct {
	ID        int64
	LibraryID int64
	Title     string
	Author    string
}

// TitleKeys lists every present book's title and first author.
func (s *Store) TitleKeys(ctx context.Context, libraries []int64) ([]TitleKey, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT b.id, b.library_id, b.title, COALESCE((SELECT a.name FROM book_authors ba JOIN authors a ON a.id = ba.author_id WHERE ba.book_id = b.id ORDER BY ba.position LIMIT 1), '')
		FROM books b WHERE b.missing = 0 AND b.library_id IN `+inInts(libraries))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TitleKey
	for rows.Next() {
		var k TitleKey
		if err := rows.Scan(&k.ID, &k.LibraryID, &k.Title, &k.Author); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// LibrariesToScan lists every library (id, interval in minutes, last scan).
func (s *Store) LibrariesToScan(ctx context.Context) ([]Library, error) {
	return s.ListLibraries(ctx)
}
