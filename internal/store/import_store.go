package store

import (
	"context"
	"strings"
)

// Lookups for importing from other apps (Calibre-Web, Kavita), which know
// books by their file paths and Calibre ids.

// FileByPath returns the file at a path.
func (s *Store) FileByPath(ctx context.Context, path string) (*File, error) {
	return scanFile(s.DB.QueryRowContext(ctx, `SELECT `+fileCols+` FROM files WHERE path = ?`, path))
}

// FilesBySuffix returns files whose path ends in suffix (e.g. "/Author/Title.epub").
func (s *Store) FilesBySuffix(ctx context.Context, suffix string, limit int) ([]File, error) {
	if limit <= 0 {
		limit = 10
	}
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(suffix)
	return s.listFiles(ctx, `WHERE path LIKE ? ESCAPE '\' AND missing = 0 ORDER BY id LIMIT ?`, "%"+esc, limit)
}

// BooksByIdentifier returns the books carrying an identifier (calibre, uuid, isbn, …).
func (s *Store) BooksByIdentifier(ctx context.Context, key, value string) ([]int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if key == "isbn" {
		return s.queryIDs(ctx, `SELECT id FROM books WHERE (isbn = ? OR json_extract(identifiers, '$.isbn') = ?) AND missing = 0`, value, value)
	}
	return s.queryIDs(ctx, `SELECT id FROM books WHERE CAST(json_extract(identifiers, '$.'||?) AS TEXT) = ? AND missing = 0`, key, value)
}
