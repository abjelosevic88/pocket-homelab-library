package store

import "context"

// SessionWithBook is a reading session with its book's title and cover, for lists.
type SessionWithBook struct {
	ReadingSession
	BookTitle string `json:"book_title"`
	BookCover string `json:"book_cover"`
}

// SessionsWithBooks is Sessions with each book's title and cover.
func (s *Store) SessionsWithBooks(ctx context.Context, uid int64, f SessionFilter) ([]SessionWithBook, error) {
	list, err := s.Sessions(ctx, uid, f)
	if err != nil {
		return nil, err
	}
	out := make([]SessionWithBook, len(list))
	ids := map[int64]bool{}
	var want []int64
	for i, r := range list {
		out[i].ReadingSession = r
		if !ids[r.BookID] {
			ids[r.BookID] = true
			want = append(want, r.BookID)
		}
	}
	if len(want) == 0 {
		return out, nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, title, cover FROM books WHERE id IN `+inInts(want))
	if err != nil {
		return nil, err
	}
	type tc struct{ title, cover string }
	info := map[int64]tc{}
	for rows.Next() {
		var id int64
		var t, c string
		if err := rows.Scan(&id, &t, &c); err != nil {
			rows.Close()
			return nil, err
		}
		if c != "" {
			c = CoverURL(id, c)
		}
		info[id] = tc{t, c}
	}
	rows.Close()
	for i := range out {
		x := info[out[i].BookID]
		out[i].BookTitle, out[i].BookCover = x.title, x.cover
	}
	return out, rows.Err()
}

// BooksInLibraries reports which of the books are in one of the libraries.
func (s *Store) BooksInLibraries(ctx context.Context, books, libraries []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	if len(books) == 0 {
		return out, nil
	}
	ids, err := s.queryIDs(ctx, `SELECT id FROM books WHERE id IN `+inInts(books)+` AND library_id IN `+inInts(libraries))
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}
