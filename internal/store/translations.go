package store

import (
	"context"
	"encoding/json"
)

// ============ translations ============

// Translation is one book being (or having been) translated into Serbian.
type Translation struct {
	ID              int64    `json:"id"`
	UserID          int64    `json:"-"`
	SourceBookID    int64    `json:"source_book_id"`
	SourceName      string   `json:"source_name"`
	SourceSize      int64    `json:"source_size"`
	Title           string   `json:"title"`
	Author          string   `json:"author"`
	TitleTranslated string   `json:"title_translated"`
	Script          string   `json:"script"`
	Model           string   `json:"model"`
	Notes           string   `json:"notes"`
	Status          string   `json:"status"`
	Step            string   `json:"step"`
	Units           int      `json:"units"`
	Chars           int      `json:"chars"`
	Batches         int      `json:"batches"`
	DoneBatches     int      `json:"done_batches"`
	CachedBatches   int      `json:"cached_batches"`
	Warnings        []string `json:"warnings"`
	Error           string   `json:"error"`
	ResultBookID    int64    `json:"result_book_id"`
	CreatedAt       string   `json:"created_at"`
	StartedAt       string   `json:"started_at"`
	FinishedAt      string   `json:"finished_at"`
}

const translationCols = `id, user_id, COALESCE(source_book_id, 0), source_name, source_size, title, author, title_translated, script, model, notes,
	status, step, units, chars, batches, done_batches, cached_batches, warnings, error, COALESCE(result_book_id, 0), created_at, started_at, finished_at`

func scanTranslation(row interface{ Scan(...any) error }) (*Translation, error) {
	var t Translation
	var warnings string
	if err := row.Scan(&t.ID, &t.UserID, &t.SourceBookID, &t.SourceName, &t.SourceSize, &t.Title, &t.Author, &t.TitleTranslated, &t.Script, &t.Model, &t.Notes,
		&t.Status, &t.Step, &t.Units, &t.Chars, &t.Batches, &t.DoneBatches, &t.CachedBatches, &warnings, &t.Error, &t.ResultBookID, &t.CreatedAt, &t.StartedAt, &t.FinishedAt); err != nil {
		return nil, notFound(err)
	}
	_ = json.Unmarshal([]byte(warnings), &t.Warnings)
	if t.Warnings == nil {
		t.Warnings = []string{}
	}
	return &t, nil
}

func nullID(id int64) any {
	if id <= 0 {
		return nil
	}
	return id
}

// CreateTranslation queues a translation.
func (s *Store) CreateTranslation(ctx context.Context, t *Translation) error {
	t.Status, t.CreatedAt = "queued", Now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO translations (user_id, source_book_id, source_name, source_size, title, author, script, model, notes, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, t.UserID, nullID(t.SourceBookID), t.SourceName, t.SourceSize, t.Title, t.Author, t.Script, t.Model, t.Notes, t.Status, t.CreatedAt)
	if err != nil {
		return err
	}
	t.ID, err = res.LastInsertId()
	return err
}

// ListTranslations returns a person's translations, newest first; bookID > 0
// keeps the ones made from that book.
func (s *Store) ListTranslations(ctx context.Context, uid, bookID int64) ([]Translation, error) {
	q := `SELECT ` + translationCols + ` FROM translations WHERE user_id = ?`
	args := []any{uid}
	if bookID > 0 {
		q += ` AND (source_book_id = ? OR result_book_id = ?)`
		args = append(args, bookID, bookID)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY id DESC LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Translation{}
	for rows.Next() {
		t, err := scanTranslation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// GetTranslation returns one of a person's translations (uid 0 = anyone's, for the worker).
func (s *Store) GetTranslation(ctx context.Context, uid, id int64) (*Translation, error) {
	if uid == 0 {
		return scanTranslation(s.DB.QueryRowContext(ctx, `SELECT `+translationCols+` FROM translations WHERE id = ?`, id))
	}
	return scanTranslation(s.DB.QueryRowContext(ctx, `SELECT `+translationCols+` FROM translations WHERE id = ? AND user_id = ?`, id, uid))
}

// NextTranslation is the oldest queued job.
func (s *Store) NextTranslation(ctx context.Context) (*Translation, error) {
	return scanTranslation(s.DB.QueryRowContext(ctx, `SELECT `+translationCols+` FROM translations WHERE status = 'queued' ORDER BY id LIMIT 1`))
}

// RequeueRunningTranslations puts jobs a restart interrupted back in the queue.
func (s *Store) RequeueRunningTranslations(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE translations SET status = 'queued', step = '' WHERE status = 'running'`)
	return err
}

// StartTranslation marks a job running.
func (s *Store) StartTranslation(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE translations SET status = 'running', step = 'start', error = '', warnings = '[]', started_at = ?, finished_at = '' WHERE id = ?`, Now(), id)
	return err
}

// TranslationProgress stores where a running job is.
func (s *Store) TranslationProgress(ctx context.Context, id int64, step string, units, chars, batches, done, cached int) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE translations SET step = ?, units = ?, chars = ?, batches = ?, done_batches = ?, cached_batches = ? WHERE id = ? AND status = 'running'`,
		step, units, chars, batches, done, cached, id)
	return err
}

// TranslationStep stores which step a running job is at.
func (s *Store) TranslationStep(ctx context.Context, id int64, step string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE translations SET step = ? WHERE id = ? AND status = 'running'`, step, id)
	return err
}

// FinishTranslation stores how a job ended.
func (s *Store) FinishTranslation(ctx context.Context, t *Translation) error {
	w, _ := json.Marshal(t.Warnings)
	if t.Warnings == nil {
		w = []byte("[]")
	}
	t.FinishedAt = Now()
	_, err := s.DB.ExecContext(ctx, `UPDATE translations SET status = ?, step = ?, title = ?, author = ?, title_translated = ?, warnings = ?, error = ?, result_book_id = ?, finished_at = ? WHERE id = ?`,
		t.Status, t.Step, t.Title, t.Author, t.TitleTranslated, string(w), t.Error, nullID(t.ResultBookID), t.FinishedAt, t.ID)
	return err
}

// SetTranslationStatus changes a job's status (queue again, cancel).
func (s *Store) SetTranslationStatus(ctx context.Context, id int64, status string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE translations SET status = ?, step = '', finished_at = CASE WHEN ? IN ('cancelled') THEN ? ELSE '' END WHERE id = ?`, status, status, Now(), id)
	return err
}

// DeleteTranslation removes a job row.
func (s *Store) DeleteTranslation(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM translations WHERE id = ?`, id)
	return err
}
