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
	// for choosing which of two copies to keep
	HasISBN bool   `json:"-"`
	HasDesc bool   `json:"-"`
	Formats string `json:"-"` // comma-separated
	Used    bool   `json:"-"` // someone's status, place or highlights
}

// ReviewBooks lists every book (not comics or manga) in these libraries with
// its authors and first file, for the review page.
func (s *Store) ReviewBooks(ctx context.Context, libraries []int64) ([]ReviewBook, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT b.id, b.library_id, b.title, b.cover, b.ereader_off,
		COALESCE((SELECT GROUP_CONCAT(a.name, char(31)) FROM book_authors ba JOIN authors a ON a.id = ba.author_id WHERE ba.book_id = b.id), ''),
		COALESCE((SELECT f.path FROM files f WHERE f.book_id = b.id AND f.missing = 0 ORDER BY f.id LIMIT 1), ''),
		b.isbn != '', b.description != '',
		COALESCE((SELECT GROUP_CONCAT(f.format) FROM files f WHERE f.book_id = b.id AND f.missing = 0), ''),
		EXISTS (SELECT 1 FROM user_books ub WHERE ub.book_id = b.id AND (ub.status != '' OR ub.percentage > 0))
		  OR EXISTS (SELECT 1 FROM annotations an WHERE an.book_id = b.id)
		FROM books b WHERE b.missing = 0 AND b.kind = 'book' AND b.library_id IN `+inInts(libraries)+` ORDER BY b.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReviewBook
	for rows.Next() {
		var b ReviewBook
		var authors, path string
		if err := rows.Scan(&b.ID, &b.LibraryID, &b.Title, &b.Cover, &b.EReaderOff, &authors, &path, &b.HasISBN, &b.HasDesc, &b.Formats, &b.Used); err != nil {
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
