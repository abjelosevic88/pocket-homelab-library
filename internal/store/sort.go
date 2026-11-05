package store

import (
	"context"
	"database/sql"
	"strings"
)

// SortItem is a book Pocket AI sorts (or sorted): where it should live and why.
type SortItem struct {
	BookID      int64    `json:"book_id"`
	Status      string   `json:"status"` // pending | suggested | applied | kept | failed
	Target      string   `json:"target"` // books | tech | review
	Reason      string   `json:"reason"`
	Confidence  float64  `json:"confidence"`
	Source      string   `json:"source"`
	RequestedBy int64    `json:"-"`
	Error       string   `json:"error,omitempty"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
	Title       string   `json:"title"`
	Authors     []string `json:"authors"`
	Cover       string   `json:"cover"`
	LibraryID   int64    `json:"library_id"`
}

// QueueSort asks for books to be sorted. again: sort them again even when
// they were sorted before (a person asked); otherwise a book is queued once.
func (s *Store) QueueSort(ctx context.Context, ids []int64, source string, by int64, again bool) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	q := `INSERT INTO book_sort (book_id, source, requested_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT(book_id) DO NOTHING`
	if again {
		q = `INSERT INTO book_sort (book_id, source, requested_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(book_id) DO UPDATE SET status = 'pending', source = excluded.source, requested_by = excluded.requested_by, error = '', updated_at = excluded.updated_at`
	}
	now := Now()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, q, id, source, nullI(&by), now, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const sortCols = `bs.book_id, bs.status, bs.target, bs.reason, bs.confidence, bs.source, COALESCE(bs.requested_by, 0), bs.error, bs.created_at, bs.updated_at,
	b.title, b.cover, b.library_id,
	COALESCE((SELECT GROUP_CONCAT(a.name, char(31)) FROM book_authors ba JOIN authors a ON a.id = ba.author_id WHERE ba.book_id = b.id), '')`

func scanSort(rows *sql.Rows) ([]SortItem, error) {
	defer rows.Close()
	out := []SortItem{}
	for rows.Next() {
		var it SortItem
		var authors string
		if err := rows.Scan(&it.BookID, &it.Status, &it.Target, &it.Reason, &it.Confidence, &it.Source, &it.RequestedBy, &it.Error, &it.CreatedAt, &it.UpdatedAt,
			&it.Title, &it.Cover, &it.LibraryID, &authors); err != nil {
			return nil, err
		}
		it.Authors = []string{}
		if authors != "" {
			it.Authors = strings.Split(authors, "\x1f")
		}
		if it.Cover != "" {
			it.Cover = CoverURL(it.BookID, it.Cover)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// PendingSort is the next books to sort, oldest first.
func (s *Store) PendingSort(ctx context.Context, limit int) ([]SortItem, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+sortCols+` FROM book_sort bs JOIN books b ON b.id = bs.book_id
		WHERE bs.status = 'pending' AND b.missing = 0 ORDER BY bs.updated_at, bs.book_id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	return scanSort(rows)
}

// SaveSort records Pocket AI's answer (or why there is none) for a book.
func (s *Store) SaveSort(ctx context.Context, id int64, status, target, reason string, confidence float64, errText string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE book_sort SET status = ?, target = ?, reason = ?, confidence = ?, error = ?, updated_at = ? WHERE book_id = ?`,
		status, target, reason, confidence, errText, Now(), id)
	return err
}

// SetSortStatus marks books applied or kept where they are.
func (s *Store) SetSortStatus(ctx context.Context, ids []int64, status string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE book_sort SET status = ?, updated_at = ? WHERE book_id IN `+inInts(ids), status, Now())
	return err
}

// ListSort lists sorted books in these statuses and libraries, newest first, with counts per status.
func (s *Store) ListSort(ctx context.Context, statuses []string, libraries []int64, offset, limit int) ([]SortItem, int, map[string]int, error) {
	in := "''"
	args := []any{}
	for i, st := range statuses {
		if i == 0 {
			in = "?"
		} else {
			in += ", ?"
		}
		args = append(args, st)
	}
	where := `b.missing = 0 AND b.library_id IN ` + inInts(libraries)
	counts := map[string]int{}
	rows, err := s.DB.QueryContext(ctx, `SELECT bs.status, COUNT(1) FROM book_sort bs JOIN books b ON b.id = bs.book_id WHERE `+where+` GROUP BY bs.status`)
	if err != nil {
		return nil, 0, nil, err
	}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err == nil {
			counts[st] = n
		}
	}
	rows.Close()
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM book_sort bs JOIN books b ON b.id = bs.book_id WHERE `+where+` AND bs.status IN (`+in+`)`, args...).Scan(&total); err != nil {
		return nil, 0, nil, err
	}
	rows, err = s.DB.QueryContext(ctx, `SELECT `+sortCols+` FROM book_sort bs JOIN books b ON b.id = bs.book_id WHERE `+where+` AND bs.status IN (`+in+`)
		ORDER BY bs.updated_at DESC, bs.book_id DESC LIMIT ? OFFSET ?`, append(args, limit, max(offset, 0))...)
	if err != nil {
		return nil, 0, nil, err
	}
	items, err := scanSort(rows)
	return items, total, counts, err
}

// GetSort is one book's sorting.
func (s *Store) GetSort(ctx context.Context, id int64) (*SortItem, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+sortCols+` FROM book_sort bs JOIN books b ON b.id = bs.book_id WHERE bs.book_id = ?`, id)
	if err != nil {
		return nil, err
	}
	items, err := scanSort(rows)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, ErrNotFound
	}
	return &items[0], nil
}
