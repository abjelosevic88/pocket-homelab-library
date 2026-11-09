package store

import "context"

// Shared author/series records must not be edited through a different person's
// private books. Individual book metadata can still be edited by its owner.
func (s *Store) OtherPrivateAuthor(ctx context.Context, authorID, uid int64) (bool, error) {
	var found bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM book_authors ba JOIN books b ON b.id=ba.book_id JOIN libraries l ON l.id=b.library_id WHERE ba.author_id=? AND l.owner_user_id IS NOT NULL AND l.owner_user_id<>?)`, authorID, uid).Scan(&found)
	return found, err
}

func (s *Store) OtherPrivateSeries(ctx context.Context, seriesID, uid int64) (bool, error) {
	var found bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM books b JOIN libraries l ON l.id=b.library_id WHERE b.series_id=? AND l.owner_user_id IS NOT NULL AND l.owner_user_id<>?)`, seriesID, uid).Scan(&found)
	return found, err
}

// LinkedBookImport is a person's durable receipt; SHA-256 is computed by Library.
type LinkedBookImport struct {
	UserID         int64  `json:"-"`
	SHA256         string `json:"sha256"`
	BookID         int64  `json:"book_id"`
	FileID         int64  `json:"file_id"`
	ShelfID        int64  `json:"collection_id"`
	SourceApp      string `json:"source_app"`
	SourceURL      string `json:"source_url"`
	OriginalName   string `json:"original_name"`
	IdempotencyKey string `json:"-"`
	CreatedAt      string `json:"imported_at"`
}

func (s *Store) ImportedLibrary(ctx context.Context, uid int64) (*Library, error) {
	var id int64
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM libraries WHERE owner_user_id=?`, uid).Scan(&id); err != nil {
		return nil, notFound(err)
	}
	return s.GetLibrary(ctx, id)
}

func (s *Store) LinkedImport(ctx context.Context, uid int64, sha string) (*LinkedBookImport, error) {
	var v LinkedBookImport
	err := s.DB.QueryRowContext(ctx, `SELECT user_id,sha256,book_id,file_id,COALESCE(shelf_id,0),source_app,source_url,original_name,idempotency_key,created_at FROM linked_book_imports WHERE user_id=? AND sha256=?`, uid, sha).
		Scan(&v.UserID, &v.SHA256, &v.BookID, &v.FileID, &v.ShelfID, &v.SourceApp, &v.SourceURL, &v.OriginalName, &v.IdempotencyKey, &v.CreatedAt)
	return &v, notFound(err)
}

func (s *Store) BookImportProvenance(ctx context.Context, uid, bookID int64) (*LinkedBookImport, error) {
	var sha string
	if err := s.DB.QueryRowContext(ctx, `SELECT sha256 FROM linked_book_imports WHERE user_id=? AND book_id=?`, uid, bookID).Scan(&sha); err != nil {
		return nil, notFound(err)
	}
	return s.LinkedImport(ctx, uid, sha)
}

func (s *Store) SaveLinkedImport(ctx context.Context, v *LinkedBookImport) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO linked_book_imports(user_id,sha256,book_id,file_id,shelf_id,source_app,source_url,original_name,idempotency_key,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(user_id,sha256) DO UPDATE SET book_id=excluded.book_id,file_id=excluded.file_id,shelf_id=excluded.shelf_id`,
		v.UserID, v.SHA256, v.BookID, v.FileID, v.ShelfID, v.SourceApp, v.SourceURL, v.OriginalName, v.IdempotencyKey, v.CreatedAt)
	return err
}
