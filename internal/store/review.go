package store

import (
	"context"
	"strings"
)

// ReviewBook is a book as the review page weighs it.
type ReviewBook struct {
	ID         int64    `json:"id"`
	LibraryID  int64    `json:"library_id"`
	Title      string   `json:"title"`
	Authors    []string `json:"authors"`
	Cover      string   `json:"cover"` // URL ('' = none)
	EReaderOff bool     `json:"ereader_off"`
	File       string   `json:"file"` // the first file's name
}

// ReviewBooks lists every book (not comics or manga) in these libraries with
// its authors and first file, for the review page.
func (s *Store) ReviewBooks(ctx context.Context, libraries []int64) ([]ReviewBook, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT b.id, b.library_id, b.title, b.cover, b.ereader_off,
		COALESCE((SELECT GROUP_CONCAT(a.name, char(31)) FROM book_authors ba JOIN authors a ON a.id = ba.author_id WHERE ba.book_id = b.id), ''),
		COALESCE((SELECT f.path FROM files f WHERE f.book_id = b.id AND f.missing = 0 ORDER BY f.id LIMIT 1), '')
		FROM books b WHERE b.missing = 0 AND b.kind = 'book' AND b.library_id IN `+inInts(libraries)+` ORDER BY b.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReviewBook
	for rows.Next() {
		var b ReviewBook
		var authors, path string
		if err := rows.Scan(&b.ID, &b.LibraryID, &b.Title, &b.Cover, &b.EReaderOff, &authors, &path); err != nil {
			return nil, err
		}
		b.Authors = []string{}
		if authors != "" {
			b.Authors = strings.Split(authors, "\x1f")
		}
		if b.Cover != "" {
			b.Cover = CoverURL(b.ID, b.Cover)
		}
		if i := strings.LastIndexAny(path, `/\`); i >= 0 {
			path = path[i+1:]
		}
		b.File = path
		out = append(out, b)
	}
	return out, rows.Err()
}
