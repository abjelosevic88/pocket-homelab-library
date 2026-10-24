package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// ============ writes (the scanner, metadata edits) ============

// BookMeta is everything about a book that comes from its files or is edited
// by hand; SaveBookMeta writes it with authors, series and tags.
type BookMeta struct {
	Title            string
	SortTitle        string
	Subtitle         string
	Authors          []string
	Description      string
	Language         string
	Publisher        string
	Published        string
	ISBN             string
	Identifiers      map[string]string
	Series           string
	SeriesIndex      float64
	Tags             []string
	Pages            int
	Words            int
	Kind             string // book | comic | manga
	ReadingDirection string // ltr | rtl
	AgeRating        string
	MetaSource       string
}

// BookFields are the field names that can be locked (edited by hand).
var BookFields = []string{"title", "sort_title", "subtitle", "authors", "description", "language", "publisher", "published", "isbn", "identifiers", "series", "series_index", "tags", "pages", "kind", "reading_direction", "age_rating", "cover"}

// MetaOf returns the editable metadata of a loaded book.
func MetaOf(b *Book) BookMeta {
	m := BookMeta{Title: b.Title, SortTitle: b.SortTitle, Subtitle: b.Subtitle, Description: b.Description, Language: b.Language,
		Publisher: b.Publisher, Published: b.Published, ISBN: b.ISBN, Identifiers: b.Identifiers, Series: b.Series, SeriesIndex: b.SeriesIndex,
		Tags: b.Tags, Pages: b.Pages, Words: b.Words, Kind: b.Kind, ReadingDirection: b.ReadingDirection, AgeRating: b.AgeRating, MetaSource: b.MetaSource}
	for _, a := range b.Authors {
		m.Authors = append(m.Authors, a.Name)
	}
	return m
}

// SortKey makes a title sortable: articles at the start moved away ("The Hobbit" → "Hobbit, The").
func SortKey(title string) string {
	t := strings.TrimSpace(title)
	low := strings.ToLower(t)
	for _, a := range []string{"the ", "a ", "an ", "der ", "die ", "das ", "le ", "la ", "les ", "el ", "il "} {
		if strings.HasPrefix(low, a) && len(t) > len(a) {
			return strings.TrimSpace(t[len(a):]) + ", " + strings.TrimSpace(t[:len(a)])
		}
	}
	return t
}

// AuthorSortKey is "Last, First" for "First Last" (names with a comma stay).
func AuthorSortKey(name string) string {
	n := strings.TrimSpace(name)
	if n == "" || strings.Contains(n, ",") {
		return n
	}
	parts := strings.Fields(n)
	if len(parts) < 2 {
		return n
	}
	last := parts[len(parts)-1]
	switch strings.ToLower(strings.TrimRight(last, ".")) {
	case "jr", "sr", "ii", "iii", "iv":
		if len(parts) >= 3 {
			return parts[len(parts)-2] + ", " + strings.Join(parts[:len(parts)-2], " ") + " " + last
		}
	}
	return last + ", " + strings.Join(parts[:len(parts)-1], " ")
}

func cleanList(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		v = strings.TrimSpace(v)
		k := strings.ToLower(v)
		if v == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, v)
	}
	return out
}

func (m *BookMeta) clean() {
	m.Title = strings.TrimSpace(m.Title)
	if m.SortTitle == "" {
		m.SortTitle = SortKey(m.Title)
	}
	m.Authors = cleanList(m.Authors)
	m.Tags = cleanList(m.Tags)
	m.Series = strings.TrimSpace(m.Series)
	if m.Series == "" {
		m.SeriesIndex = 0
	}
	switch m.Kind {
	case "book", "comic", "manga":
	default:
		m.Kind = "book"
	}
	if m.ReadingDirection != "rtl" {
		m.ReadingDirection = "ltr"
	}
	if m.Identifiers == nil {
		m.Identifiers = map[string]string{}
	}
	if m.MetaSource == "" {
		m.MetaSource = "file"
	}
}

// CreateBook inserts a book in a library with its metadata and returns its id.
func (s *Store) CreateBook(ctx context.Context, libraryID int64, groupKey string, m BookMeta) (int64, error) {
	m.clean()
	ids, _ := json.Marshal(m.Identifiers)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	seriesID, err := ensureSeries(ctx, tx, m.Series)
	if err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO books (library_id, group_key, title, sort_title, subtitle, description, language, publisher, published, isbn, identifiers,
		series_id, series_index, pages, words, kind, reading_direction, age_rating, meta_source) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		libraryID, groupKey, m.Title, m.SortTitle, m.Subtitle, m.Description, m.Language, m.Publisher, m.Published, m.ISBN, string(ids),
		seriesID, m.SeriesIndex, m.Pages, m.Words, m.Kind, m.ReadingDirection, m.AgeRating, m.MetaSource)
	if err != nil {
		return 0, conflict(err)
	}
	id, _ := res.LastInsertId()
	if err := setAuthors(ctx, tx, id, m.Authors); err != nil {
		return 0, err
	}
	if err := setTags(ctx, tx, id, m.Tags); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, s.RefreshFTS(ctx, id)
}

// SaveBookMeta overwrites a book's metadata (callers decide what locked fields keep).
func (s *Store) SaveBookMeta(ctx context.Context, id int64, m BookMeta) error {
	m.clean()
	ids, _ := json.Marshal(m.Identifiers)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	seriesID, err := ensureSeries(ctx, tx, m.Series)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE books SET title = ?, sort_title = ?, subtitle = ?, description = ?, language = ?, publisher = ?, published = ?, isbn = ?, identifiers = ?,
		series_id = ?, series_index = ?, pages = ?, words = ?, kind = ?, reading_direction = ?, age_rating = ?, meta_source = ?, updated_at = ? WHERE id = ?`,
		m.Title, m.SortTitle, m.Subtitle, m.Description, m.Language, m.Publisher, m.Published, m.ISBN, string(ids),
		seriesID, m.SeriesIndex, m.Pages, m.Words, m.Kind, m.ReadingDirection, m.AgeRating, m.MetaSource, Now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if err := setAuthors(ctx, tx, id, m.Authors); err != nil {
		return err
	}
	if err := setTags(ctx, tx, id, m.Tags); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.RefreshFTS(ctx, id)
}

// SetLocked records which fields were edited by hand.
func (s *Store) SetLocked(ctx context.Context, id int64, fields []string) error {
	b, _ := json.Marshal(cleanList(fields))
	_, err := s.DB.ExecContext(ctx, `UPDATE books SET locked = ? WHERE id = ?`, string(b), id)
	return err
}

// SetBookCover sets the cover file name (” = none) and its average colour.
func (s *Store) SetBookCover(ctx context.Context, id int64, name, color string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE books SET cover = ?, cover_color = ?, updated_at = ? WHERE id = ?`, name, color, Now(), id)
	return err
}

// SetBookMissing marks a book whose files are all gone (or back).
func (s *Store) SetBookMissing(ctx context.Context, id int64, missing bool) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE books SET missing = ? WHERE id = ?`, boolInt(missing), id)
	return err
}

// MoveBook moves a book into another group (files renamed or moved on disk).
func (s *Store) MoveBook(ctx context.Context, id, libraryID int64, groupKey string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE books SET library_id = ?, group_key = ? WHERE id = ?`, libraryID, groupKey, id)
	return conflict(err)
}

// DeleteBook removes a book from the catalogue (everyone's progress in it too).
func (s *Store) DeleteBook(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM books WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM books_fts WHERE rowid = ?`, id)
	return nil
}

// PruneOrphans removes authors, series and tags no book uses any more.
func (s *Store) PruneOrphans(ctx context.Context) error {
	for _, q := range []string{
		`DELETE FROM authors WHERE id NOT IN (SELECT author_id FROM book_authors)`,
		`DELETE FROM series WHERE id NOT IN (SELECT series_id FROM books WHERE series_id IS NOT NULL)`,
	} {
		if _, err := s.DB.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

func ensureSeries(ctx context.Context, tx *sql.Tx, name string) (any, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM series WHERE name = ?`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO series (name, sort_name) VALUES (?, ?)`, name, SortKey(name))
	if err != nil {
		return nil, err
	}
	id, _ = res.LastInsertId()
	return id, nil
}

func ensureAuthor(ctx context.Context, tx *sql.Tx, name string) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM authors WHERE name = ?`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO authors (name, sort_name) VALUES (?, ?)`, name, AuthorSortKey(name))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func setAuthors(ctx context.Context, tx *sql.Tx, bookID int64, names []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM book_authors WHERE book_id = ?`, bookID); err != nil {
		return err
	}
	for i, n := range names {
		aid, err := ensureAuthor(ctx, tx, n)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO book_authors (book_id, author_id, position) VALUES (?, ?, ?)`, bookID, aid, i); err != nil {
			return err
		}
	}
	return nil
}

func setTags(ctx context.Context, tx *sql.Tx, bookID int64, tags []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM book_tags WHERE book_id = ?`, bookID); err != nil {
		return err
	}
	for _, t := range tags {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO book_tags (book_id, tag) VALUES (?, ?)`, bookID, t); err != nil {
			return err
		}
	}
	return nil
}

// RefreshFTS rewrites a book's search row.
func (s *Store) RefreshFTS(ctx context.Context, id int64) error {
	var title, subtitle, desc, publisher, isbn, series string
	err := s.DB.QueryRowContext(ctx, `SELECT b.title, b.subtitle, b.description, b.publisher, b.isbn, COALESCE(se.name, '')
		FROM books b LEFT JOIN series se ON se.id = b.series_id WHERE b.id = ?`, id).Scan(&title, &subtitle, &desc, &publisher, &isbn, &series)
	if err != nil {
		return notFound(err)
	}
	authors, _ := s.queryStrings(ctx, `SELECT a.name FROM book_authors ba JOIN authors a ON a.id = ba.author_id WHERE ba.book_id = ? ORDER BY ba.position`, id)
	tags, _ := s.queryStrings(ctx, `SELECT tag FROM book_tags WHERE book_id = ?`, id)
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM books_fts WHERE rowid = ?`, id); err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO books_fts (rowid, title, authors, series, tags, description, publisher, isbn) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, strings.TrimSpace(title+" "+subtitle), strings.Join(authors, " "), series, strings.Join(tags, " "), stripTags(desc), publisher, isbn)
	return err
}

func stripTags(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
			b.WriteByte(' ')
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *Store) queryStrings(ctx context.Context, q string, args ...any) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ============ files ============

const fileCols = `id, book_id, library_id, path, format, size, mtime, partial_md5, name_md5, sha256, pages, missing, added_at`

func scanFile(row interface{ Scan(...any) error }) (*File, error) {
	var f File
	if err := row.Scan(&f.ID, &f.BookID, &f.LibraryID, &f.Path, &f.Format, &f.Size, &f.MTime, &f.PartialMD5, &f.NameMD5, &f.SHA256, &f.Pages, &f.Missing, &f.AddedAt); err != nil {
		return nil, notFound(err)
	}
	f.Name = filepath.Base(f.Path)
	return &f, nil
}

func (s *Store) listFiles(ctx context.Context, q string, args ...any) ([]File, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+fileCols+` FROM files `+q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []File
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

// UpsertFile inserts or updates a file by path (ID set on return).
func (s *Store) UpsertFile(ctx context.Context, f *File) error {
	err := s.DB.QueryRowContext(ctx, `INSERT INTO files (book_id, library_id, path, format, size, mtime, partial_md5, name_md5, sha256, pages, missing, scanned_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)
		ON CONFLICT(path) DO UPDATE SET book_id = excluded.book_id, library_id = excluded.library_id, format = excluded.format, size = excluded.size,
		  mtime = excluded.mtime, partial_md5 = excluded.partial_md5, name_md5 = excluded.name_md5,
		  sha256 = CASE WHEN files.size = excluded.size AND files.mtime = excluded.mtime THEN files.sha256 ELSE excluded.sha256 END,
		  pages = excluded.pages, missing = 0, scanned_at = excluded.scanned_at
		RETURNING id`,
		f.BookID, f.LibraryID, f.Path, f.Format, f.Size, f.MTime, f.PartialMD5, f.NameMD5, f.SHA256, f.Pages, Now()).Scan(&f.ID)
	return err
}

// TouchFile records that a scan saw an unchanged file.
func (s *Store) TouchFile(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE files SET missing = 0, scanned_at = ? WHERE id = ?`, Now(), id)
	return err
}

// SetFileMissing marks a file gone from disk (or back).
func (s *Store) SetFileMissing(ctx context.Context, id int64, missing bool) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE files SET missing = ? WHERE id = ?`, boolInt(missing), id)
	return err
}

// SetFileHash stores a file's whole-file SHA-256.
func (s *Store) SetFileHash(ctx context.Context, id int64, sha string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE files SET sha256 = ? WHERE id = ?`, sha, id)
	return err
}

// DeleteFile removes a file row.
func (s *Store) DeleteFile(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM files WHERE id = ?`, id)
	return err
}

// GetFile returns one file.
func (s *Store) GetFile(ctx context.Context, id int64) (*File, error) {
	return scanFile(s.DB.QueryRowContext(ctx, `SELECT `+fileCols+` FROM files WHERE id = ?`, id))
}

// FilesOfBook returns a book's files, best format first.
func (s *Store) FilesOfBook(ctx context.Context, bookID int64) ([]File, error) {
	fs, err := s.listFiles(ctx, `WHERE book_id = ? ORDER BY missing, id`, bookID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(fs, func(i, j int) bool { return FormatRank(fs[i].Format) < FormatRank(fs[j].Format) })
	return fs, nil
}

// FilesInLibrary returns every file of a library by path (for incremental scans).
func (s *Store) FilesInLibrary(ctx context.Context, libraryID int64) (map[string]File, error) {
	fs, err := s.listFiles(ctx, `WHERE library_id = ?`, libraryID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]File, len(fs))
	for _, f := range fs {
		out[f.Path] = f
	}
	return out, nil
}

// FilesByHash finds files by KOReader's document hash: the partial MD5 of
// the contents or the MD5 of the file name.
func (s *Store) FilesByHash(ctx context.Context, hash string) ([]File, error) {
	hash = strings.ToLower(hash)
	return s.listFiles(ctx, `WHERE (partial_md5 = ? OR name_md5 = ?) AND missing = 0 ORDER BY partial_md5 = ? DESC, id`, hash, hash, hash)
}

// FilesWithoutHash returns files that still need their whole-file hash.
func (s *Store) FilesWithoutHash(ctx context.Context, limit int) ([]File, error) {
	return s.listFiles(ctx, `WHERE sha256 = '' AND missing = 0 ORDER BY id LIMIT ?`, limit)
}

// FormatRank orders formats for reading and sending: EPUB first.
func FormatRank(f string) int {
	switch f {
	case "epub":
		return 0
	case "kepub":
		return 1
	case "azw3":
		return 2
	case "mobi", "azw", "prc":
		return 3
	case "pdf":
		return 4
	case "cbz", "cbr", "cb7", "cbt":
		return 5
	case "fb2", "fbz":
		return 6
	}
	return 9
}

// RefreshBookFormatState re-derives a book's missing flag from its files.
func (s *Store) RefreshBookFormatState(ctx context.Context, bookID int64) error {
	var live int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM files WHERE book_id = ? AND missing = 0`, bookID).Scan(&live); err != nil {
		return err
	}
	return s.SetBookMissing(ctx, bookID, live == 0)
}

// BookByGroup finds the book a file belongs to by its group key.
func (s *Store) BookByGroup(ctx context.Context, libraryID int64, groupKey string) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM books WHERE library_id = ? AND group_key = ?`, libraryID, groupKey).Scan(&id)
	return id, notFound(err)
}

// ============ reads ============

// BookQuery filters and sorts the catalogue (the API's query parameters,
// and a smart shelf's saved filter).
type BookQuery struct {
	Q          string  `json:"q,omitempty"`
	Library    int64   `json:"library,omitempty"`
	Author     int64   `json:"author,omitempty"`
	Series     int64   `json:"series,omitempty"`
	Tag        string  `json:"tag,omitempty"`
	Format     string  `json:"format,omitempty"`
	Language   string  `json:"language,omitempty"`
	Publisher  string  `json:"publisher,omitempty"`
	Status     string  `json:"status,omitempty"` // want | reading | read | dnf | unread | any
	Favorite   bool    `json:"favorite,omitempty"`
	RatingMin  int     `json:"rating_min,omitempty"`
	Kind       string  `json:"kind,omitempty"`
	AddedSince string  `json:"added_since,omitempty"`
	Missing    string  `json:"missing,omitempty"` // cover | description | isbn | series | files
	Sort       string  `json:"sort,omitempty"`
	Order      string  `json:"order,omitempty"`
	Offset     int     `json:"offset,omitempty"`
	Limit      int     `json:"limit,omitempty"`
	Shelf      int64   `json:"-"` // books of a hand-picked shelf
	IDs        []int64 `json:"-"`
	// Scope: the person asking and the libraries they may open (hidden
	// libraries already removed unless one is asked for by id).
	UserID     int64   `json:"-"`
	Libraries  []int64 `json:"-"`
	ShowHidden bool    `json:"-"` // include books the person hid
	Seed       int64   `json:"-"` // random order seed
}

// FTSQuery turns typed words into an FTS5 prefix query ("lord rin" → "lord"* "rin"*).
func FTSQuery(q string) string {
	var parts []string
	for _, w := range strings.FieldsFunc(q, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		parts = append(parts, `"`+w+`"*`)
	}
	return strings.Join(parts, " ")
}

func inInts(ids []int64) string {
	if len(ids) == 0 {
		return "(NULL)"
	}
	var b strings.Builder
	b.WriteByte('(')
	for i, id := range ids {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%d", id)
	}
	b.WriteByte(')')
	return b.String()
}

// where builds the WHERE clause and its arguments for a query.
func (q *BookQuery) where() (string, []any) {
	conds := []string{"b.library_id IN " + inInts(q.Libraries)}
	var args []any
	if q.Missing != "files" {
		conds = append(conds, "b.missing = 0")
	}
	if !q.ShowHidden && q.Status != "any" {
		conds = append(conds, "COALESCE(ub.hidden, 0) = 0")
	}
	if fq := FTSQuery(q.Q); fq != "" {
		conds = append(conds, "b.id IN (SELECT rowid FROM books_fts WHERE books_fts MATCH ?)")
		args = append(args, fq)
	}
	if q.Library > 0 {
		conds = append(conds, "b.library_id = ?")
		args = append(args, q.Library)
	}
	if q.Author > 0 {
		conds = append(conds, "b.id IN (SELECT book_id FROM book_authors WHERE author_id = ?)")
		args = append(args, q.Author)
	}
	if q.Series > 0 {
		conds = append(conds, "b.series_id = ?")
		args = append(args, q.Series)
	}
	if q.Tag != "" {
		conds = append(conds, "b.id IN (SELECT book_id FROM book_tags WHERE tag = ?)")
		args = append(args, q.Tag)
	}
	if q.Format != "" {
		conds = append(conds, "b.id IN (SELECT book_id FROM files WHERE format = ? AND missing = 0)")
		args = append(args, strings.ToLower(q.Format))
	}
	if q.Language != "" {
		conds = append(conds, "b.language = ?")
		args = append(args, q.Language)
	}
	if q.Publisher != "" {
		conds = append(conds, "b.publisher = ?")
		args = append(args, q.Publisher)
	}
	if q.Kind != "" {
		conds = append(conds, "b.kind = ?")
		args = append(args, q.Kind)
	}
	if q.AddedSince != "" {
		conds = append(conds, "b.added_at >= ?")
		args = append(args, q.AddedSince)
	}
	switch q.Status {
	case "want", "reading", "read", "dnf":
		conds = append(conds, "ub.status = ?")
		args = append(args, q.Status)
	case "unread":
		conds = append(conds, "COALESCE(ub.status, '') IN ('', 'want') AND COALESCE(ub.percentage, 0) = 0 AND COALESCE(ub.times_read, 0) = 0")
	}
	if q.Favorite {
		conds = append(conds, "ub.favorite = 1")
	}
	if q.RatingMin > 0 {
		conds = append(conds, "ub.rating >= ?")
		args = append(args, q.RatingMin)
	}
	switch q.Missing {
	case "cover":
		conds = append(conds, "b.cover = ''")
	case "description":
		conds = append(conds, "b.description = ''")
	case "isbn":
		conds = append(conds, "b.isbn = ''")
	case "series":
		conds = append(conds, "b.series_id IS NULL")
	case "files":
		conds = append(conds, "b.missing = 1")
	}
	if q.Shelf > 0 {
		conds = append(conds, "b.id IN (SELECT book_id FROM shelf_books WHERE shelf_id = ?)")
		args = append(args, q.Shelf)
	}
	if len(q.IDs) > 0 {
		conds = append(conds, "b.id IN "+inInts(q.IDs))
	}
	return strings.Join(conds, " AND "), args
}

func (q *BookQuery) orderBy() string {
	// def is whether the sort runs newest/highest first when no order is asked for
	dir := func(def bool) string {
		desc := def
		switch strings.ToLower(q.Order) {
		case "asc":
			desc = false
		case "desc":
			desc = true
		}
		if desc {
			return " DESC"
		}
		return ""
	}
	authorSort := `(SELECT a.sort_name FROM book_authors ba JOIN authors a ON a.id = ba.author_id WHERE ba.book_id = b.id ORDER BY ba.position LIMIT 1)`
	switch q.Sort {
	case "author":
		return "COALESCE(" + authorSort + ", '~') COLLATE NOCASE" + dir(false) + ", se.sort_name COLLATE NOCASE, b.series_index, b.sort_title COLLATE NOCASE"
	case "added":
		return "b.added_at" + dir(true) + ", b.id" + dir(true)
	case "published":
		return "b.published = ''," + " b.published" + dir(true) + ", b.sort_title COLLATE NOCASE"
	case "series":
		return "se.sort_name IS NULL, se.sort_name COLLATE NOCASE" + dir(false) + ", b.series_index, b.sort_title COLLATE NOCASE"
	case "rating":
		return "COALESCE(ub.rating, 0)" + dir(true) + ", b.sort_title COLLATE NOCASE"
	case "last_read":
		return "COALESCE(ub.last_read_at, '')" + dir(true) + ", b.sort_title COLLATE NOCASE"
	case "pages":
		return "b.pages" + dir(true) + ", b.sort_title COLLATE NOCASE"
	case "shelf":
		return fmt.Sprintf("(SELECT position FROM shelf_books WHERE shelf_id = %d AND book_id = b.id)", q.Shelf) + dir(false) + ", b.id"
	case "random":
		seed := q.Seed
		if seed == 0 {
			seed = 7
		}
		// stable for a seed, so paging through a shuffle shows each book once
		return fmt.Sprintf("(((b.id + %d) * 2654435761) %% 4294967291), b.id", seed%1000003)
	}
	return "b.sort_title COLLATE NOCASE" + dir(false) + ", b.id"
}

const bookCols = `b.id, b.library_id, b.group_key, b.title, b.sort_title, b.subtitle, b.language, b.publisher, b.published, b.isbn, b.identifiers,
	COALESCE(b.series_id, 0), COALESCE(se.name, ''), b.series_index, b.pages, b.words, b.kind, b.reading_direction, b.age_rating,
	b.cover, b.cover_color, b.locked, b.meta_source, b.missing, b.added_at, b.updated_at,
	ub.status, ub.rating, ub.favorite, ub.hidden, ub.percentage, ub.locator, ub.file_id, ub.last_read_at, ub.last_device, ub.started_at, ub.finished_at, ub.times_read, ub.updated_at`

const bookFrom = ` FROM books b LEFT JOIN series se ON se.id = b.series_id LEFT JOIN user_books ub ON ub.book_id = b.id AND ub.user_id = ? `

func scanBook(row interface{ Scan(...any) error }) (*Book, error) {
	var b Book
	var ids, locked string
	var st struct {
		status, locator, lastRead, lastDevice, started, finished, updated sql.NullString
		rating, timesRead, fileID                                         sql.NullInt64
		fav, hidden                                                       sql.NullBool
		pct                                                               sql.NullFloat64
	}
	if err := row.Scan(&b.ID, &b.LibraryID, &b.GroupKey, &b.Title, &b.SortTitle, &b.Subtitle, &b.Language, &b.Publisher, &b.Published, &b.ISBN, &ids,
		&b.SeriesID, &b.Series, &b.SeriesIndex, &b.Pages, &b.Words, &b.Kind, &b.ReadingDirection, &b.AgeRating,
		&b.Cover, &b.CoverColor, &locked, &b.MetaSource, &b.Missing, &b.AddedAt, &b.UpdatedAt,
		&st.status, &st.rating, &st.fav, &st.hidden, &st.pct, &st.locator, &st.fileID, &st.lastRead, &st.lastDevice, &st.started, &st.finished, &st.timesRead, &st.updated); err != nil {
		return nil, notFound(err)
	}
	_ = json.Unmarshal([]byte(ids), &b.Identifiers)
	if b.Identifiers == nil {
		b.Identifiers = map[string]string{}
	}
	_ = json.Unmarshal([]byte(locked), &b.Locked)
	if b.Locked == nil {
		b.Locked = []string{}
	}
	if b.Cover != "" {
		b.Cover = CoverURL(b.ID, b.Cover)
	}
	if st.updated.Valid {
		b.State = &UserBook{Status: st.status.String, Rating: int(st.rating.Int64), Favorite: st.fav.Bool, Hidden: st.hidden.Bool, Percentage: st.pct.Float64,
			Locator: st.locator.String, FileID: st.fileID.Int64, LastReadAt: st.lastRead.String, LastDevice: st.lastDevice.String,
			StartedAt: st.started.String, FinishedAt: st.finished.String, TimesRead: int(st.timesRead.Int64), UpdatedAt: st.updated.String}
	}
	b.Authors, b.Tags, b.Formats = []AuthorRef{}, []string{}, []string{}
	return &b, nil
}

// CoverURL is where a book's cover is served; the file name busts caches when it changes.
func CoverURL(bookID int64, name string) string {
	v := strings.TrimSuffix(name, filepath.Ext(name))
	if i := strings.LastIndexByte(v, '-'); i >= 0 {
		v = v[i+1:]
	}
	return fmt.Sprintf("/api/v1/books/%d/cover?v=%s", bookID, v)
}

// ListBooks returns one page of books for a query, and how many match.
func (s *Store) ListBooks(ctx context.Context, q BookQuery) ([]Book, int, error) {
	where, args := q.where()
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1)`+bookFrom+`WHERE `+where, append([]any{q.UserID}, args...)...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 60
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+bookCols+bookFrom+`WHERE `+where+` ORDER BY `+q.orderBy()+` LIMIT ? OFFSET ?`,
		append(append([]any{q.UserID}, args...), limit, max(q.Offset, 0))...)
	if err != nil {
		return nil, 0, err
	}
	var out []Book
	for rows.Next() {
		b, err := scanBook(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		out = append(out, *b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if err := s.hydrate(ctx, out); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// hydrate fills authors, tags and formats of a page of books in three queries.
func (s *Store) hydrate(ctx context.Context, books []Book) error {
	if len(books) == 0 {
		return nil
	}
	idx := map[int64]int{}
	ids := make([]int64, len(books))
	for i, b := range books {
		idx[b.ID] = i
		ids[i] = b.ID
	}
	in := inInts(ids)
	rows, err := s.DB.QueryContext(ctx, `SELECT ba.book_id, a.id, a.name FROM book_authors ba JOIN authors a ON a.id = ba.author_id WHERE ba.book_id IN `+in+` ORDER BY ba.book_id, ba.position`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var bid int64
		var a AuthorRef
		if err := rows.Scan(&bid, &a.ID, &a.Name); err != nil {
			rows.Close()
			return err
		}
		books[idx[bid]].Authors = append(books[idx[bid]].Authors, a)
	}
	rows.Close()
	rows, err = s.DB.QueryContext(ctx, `SELECT book_id, tag FROM book_tags WHERE book_id IN `+in+` ORDER BY tag COLLATE NOCASE`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var bid int64
		var t string
		if err := rows.Scan(&bid, &t); err != nil {
			rows.Close()
			return err
		}
		books[idx[bid]].Tags = append(books[idx[bid]].Tags, t)
	}
	rows.Close()
	rows, err = s.DB.QueryContext(ctx, `SELECT DISTINCT book_id, format FROM files WHERE missing = 0 AND book_id IN `+in)
	if err != nil {
		return err
	}
	for rows.Next() {
		var bid int64
		var f string
		if err := rows.Scan(&bid, &f); err != nil {
			rows.Close()
			return err
		}
		books[idx[bid]].Formats = append(books[idx[bid]].Formats, f)
	}
	rows.Close()
	for i := range books {
		sort.SliceStable(books[i].Formats, func(a, c int) bool { return FormatRank(books[i].Formats[a]) < FormatRank(books[i].Formats[c]) })
	}
	return nil
}

// GetBook returns one book with description, files and the person's state,
// if it is in one of their libraries.
func (s *Store) GetBook(ctx context.Context, uid, id int64, libraries []int64) (*Book, error) {
	b, err := scanBook(s.DB.QueryRowContext(ctx, `SELECT `+bookCols+bookFrom+`WHERE b.id = ? AND b.library_id IN `+inInts(libraries), uid, id))
	if err != nil {
		return nil, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT description FROM books WHERE id = ?`, id).Scan(&b.Description); err != nil {
		return nil, err
	}
	one := []Book{*b}
	if err := s.hydrate(ctx, one); err != nil {
		return nil, err
	}
	*b = one[0]
	if b.Files, err = s.FilesOfBook(ctx, id); err != nil {
		return nil, err
	}
	return b, nil
}

// GetBookAny loads a book regardless of libraries (background work, devices).
func (s *Store) GetBookAny(ctx context.Context, id int64) (*Book, error) {
	var lib int64
	if err := s.DB.QueryRowContext(ctx, `SELECT library_id FROM books WHERE id = ?`, id).Scan(&lib); err != nil {
		return nil, notFound(err)
	}
	return s.GetBook(ctx, 0, id, []int64{lib})
}

// ============ authors, series, tags ============

// ListAuthors lists authors with books in the libraries, optionally matching q.
func (s *Store) ListAuthors(ctx context.Context, libraries []int64, q string, offset, limit int) ([]Author, int, error) {
	where := `a.id IN (SELECT ba.author_id FROM book_authors ba JOIN books b ON b.id = ba.book_id WHERE b.missing = 0 AND b.library_id IN ` + inInts(libraries) + `)`
	var args []any
	if q = strings.TrimSpace(q); q != "" {
		where += ` AND a.name LIKE ?`
		args = append(args, "%"+q+"%")
	}
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM authors a WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT a.id, a.name, a.sort_name, a.photo,
		(SELECT COUNT(1) FROM book_authors ba JOIN books b ON b.id = ba.book_id WHERE ba.author_id = a.id AND b.missing = 0 AND b.library_id IN `+inInts(libraries)+`)
		FROM authors a WHERE `+where+` ORDER BY a.sort_name COLLATE NOCASE LIMIT ? OFFSET ?`, append(args, limit, max(offset, 0))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Author{}
	for rows.Next() {
		var a Author
		if err := rows.Scan(&a.ID, &a.Name, &a.SortName, &a.Photo, &a.Books); err != nil {
			return nil, 0, err
		}
		if a.Photo != "" {
			a.Photo = fmt.Sprintf("/api/v1/authors/%d/photo?v=%s", a.ID, strings.TrimSuffix(a.Photo, filepath.Ext(a.Photo)))
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

// GetAuthor returns an author with bio and links.
func (s *Store) GetAuthor(ctx context.Context, id int64, libraries []int64) (*Author, error) {
	var a Author
	var links string
	err := s.DB.QueryRowContext(ctx, `SELECT a.id, a.name, a.sort_name, a.bio, a.photo, a.links,
		(SELECT COUNT(1) FROM book_authors ba JOIN books b ON b.id = ba.book_id WHERE ba.author_id = a.id AND b.missing = 0 AND b.library_id IN `+inInts(libraries)+`)
		FROM authors a WHERE a.id = ?`, id).Scan(&a.ID, &a.Name, &a.SortName, &a.Bio, &a.Photo, &links, &a.Books)
	if err != nil {
		return nil, notFound(err)
	}
	_ = json.Unmarshal([]byte(links), &a.Links)
	if a.Photo != "" {
		a.Photo = fmt.Sprintf("/api/v1/authors/%d/photo?v=%s", a.ID, strings.TrimSuffix(a.Photo, filepath.Ext(a.Photo)))
	}
	return &a, nil
}

// AuthorPhotoFile returns the stored photo file name of an author.
func (s *Store) AuthorPhotoFile(ctx context.Context, id int64) (string, error) {
	var p string
	err := s.DB.QueryRowContext(ctx, `SELECT photo FROM authors WHERE id = ?`, id).Scan(&p)
	return p, notFound(err)
}

// SetAuthorInfo stores what was learned about an author.
func (s *Store) SetAuthorInfo(ctx context.Context, id int64, bio, photo, olKey string, links map[string]string) error {
	b, _ := json.Marshal(links)
	_, err := s.DB.ExecContext(ctx, `UPDATE authors SET bio = ?, photo = ?, ol_key = ?, links = ?, enriched_at = ? WHERE id = ?`, bio, photo, olKey, string(b), Now(), id)
	return err
}

// RenameAuthor renames (or merges into an existing) author.
func (s *Store) RenameAuthor(ctx context.Context, id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	var other int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM authors WHERE name = ? AND id <> ?`, name, id).Scan(&other)
	if err == nil {
		// merge: move the books over
		if _, err := s.DB.ExecContext(ctx, `UPDATE OR IGNORE book_authors SET author_id = ? WHERE author_id = ?`, other, id); err != nil {
			return err
		}
		_, _ = s.DB.ExecContext(ctx, `DELETE FROM authors WHERE id = ?`, id)
	} else {
		if _, err := s.DB.ExecContext(ctx, `UPDATE authors SET name = ?, sort_name = ? WHERE id = ?`, name, AuthorSortKey(name), id); err != nil {
			return err
		}
		other = id
	}
	ids, _ := s.queryIDs(ctx, `SELECT book_id FROM book_authors WHERE author_id = ?`, other)
	for _, b := range ids {
		_ = s.RefreshFTS(ctx, b)
	}
	return nil
}

// AuthorsToEnrich returns authors not looked up yet.
func (s *Store) AuthorsToEnrich(ctx context.Context, limit int) ([]Author, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name FROM authors WHERE enriched_at = '' ORDER BY id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Author
	for rows.Next() {
		var a Author
		if err := rows.Scan(&a.ID, &a.Name); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListSeries lists series with books in the libraries.
func (s *Store) ListSeries(ctx context.Context, uid int64, libraries []int64, q string, offset, limit int) ([]Series, int, error) {
	inLib := `b.missing = 0 AND b.library_id IN ` + inInts(libraries)
	where := `se.id IN (SELECT b.series_id FROM books b WHERE ` + inLib + `)`
	var args []any
	if q = strings.TrimSpace(q); q != "" {
		where += ` AND se.name LIKE ?`
		args = append(args, "%"+q+"%")
	}
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM series se WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT se.id, se.name, se.sort_name,
		(SELECT COUNT(1) FROM books b WHERE b.series_id = se.id AND `+inLib+`),
		(SELECT COUNT(1) FROM books b JOIN user_books ub ON ub.book_id = b.id AND ub.user_id = ? WHERE b.series_id = se.id AND ub.status = 'read' AND `+inLib+`)
		FROM series se WHERE `+where+` ORDER BY se.sort_name COLLATE NOCASE LIMIT ? OFFSET ?`, append(append([]any{uid}, args...), limit, max(offset, 0))...)
	if err != nil {
		return nil, 0, err
	}
	out := []Series{}
	for rows.Next() {
		var se Series
		if err := rows.Scan(&se.ID, &se.Name, &se.SortName, &se.Books, &se.Read); err != nil {
			rows.Close()
			return nil, 0, err
		}
		out = append(out, se)
	}
	rows.Close()
	for i := range out {
		out[i].Covers = s.seriesCovers(ctx, out[i].ID, libraries)
	}
	return out, total, rows.Err()
}

func (s *Store) seriesCovers(ctx context.Context, seriesID int64, libraries []int64) []string {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, cover FROM books b WHERE series_id = ? AND cover <> '' AND missing = 0 AND library_id IN `+inInts(libraries)+` ORDER BY series_index, sort_title LIMIT 3`, seriesID)
	if err != nil {
		return []string{}
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id int64
		var c string
		if rows.Scan(&id, &c) == nil {
			out = append(out, CoverURL(id, c))
		}
	}
	return out
}

// GetSeries returns one series.
func (s *Store) GetSeries(ctx context.Context, uid, id int64, libraries []int64) (*Series, error) {
	var se Series
	err := s.DB.QueryRowContext(ctx, `SELECT id, name, sort_name, description FROM series WHERE id = ?`, id).Scan(&se.ID, &se.Name, &se.SortName, &se.Description)
	if err != nil {
		return nil, notFound(err)
	}
	inLib := `b.missing = 0 AND b.library_id IN ` + inInts(libraries)
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1), COALESCE(SUM(CASE WHEN ub.status = 'read' THEN 1 ELSE 0 END), 0) FROM books b LEFT JOIN user_books ub ON ub.book_id = b.id AND ub.user_id = ? WHERE b.series_id = ? AND `+inLib, uid, id).Scan(&se.Books, &se.Read)
	se.Covers = s.seriesCovers(ctx, id, libraries)
	return &se, nil
}

// Tag is a tag with how many books carry it.
type Tag struct {
	Name  string `json:"name"`
	Books int    `json:"books"`
}

// ListTags lists tags in the libraries, most used first.
func (s *Store) ListTags(ctx context.Context, libraries []int64, q string, limit int) ([]Tag, error) {
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	args := []any{}
	cond := ""
	if q = strings.TrimSpace(q); q != "" {
		cond = ` AND t.tag LIKE ?`
		args = append(args, "%"+q+"%")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT t.tag, COUNT(1) FROM book_tags t JOIN books b ON b.id = t.book_id WHERE b.missing = 0 AND b.library_id IN `+inInts(libraries)+cond+
		` GROUP BY t.tag COLLATE NOCASE ORDER BY COUNT(1) DESC, t.tag COLLATE NOCASE LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Tag{}
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.Name, &t.Books); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Facets lists the distinct languages, formats and publishers in the libraries (filter menus).
func (s *Store) Facets(ctx context.Context, libraries []int64) (map[string][]Tag, error) {
	in := inInts(libraries)
	out := map[string][]Tag{}
	for name, q := range map[string]string{
		"languages":  `SELECT language, COUNT(1) FROM books b WHERE missing = 0 AND language <> '' AND library_id IN ` + in + ` GROUP BY language ORDER BY COUNT(1) DESC LIMIT 50`,
		"formats":    `SELECT f.format, COUNT(DISTINCT f.book_id) FROM files f JOIN books b ON b.id = f.book_id WHERE f.missing = 0 AND b.library_id IN ` + in + ` GROUP BY f.format ORDER BY COUNT(1) DESC`,
		"publishers": `SELECT publisher, COUNT(1) FROM books b WHERE missing = 0 AND publisher <> '' AND library_id IN ` + in + ` GROUP BY publisher ORDER BY COUNT(1) DESC LIMIT 100`,
	} {
		rows, err := s.DB.QueryContext(ctx, q)
		if err != nil {
			return nil, err
		}
		list := []Tag{}
		for rows.Next() {
			var t Tag
			if err := rows.Scan(&t.Name, &t.Books); err != nil {
				rows.Close()
				return nil, err
			}
			list = append(list, t)
		}
		rows.Close()
		out[name] = list
	}
	return out, nil
}
