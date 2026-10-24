package store

import (
	"context"
	"encoding/json"
)

// DefaultTitle is the app title until someone changes it.
const DefaultTitle = "Pocket Library"

// Settings are the box-wide settings plus one person's preferences.
type Settings struct {
	// ---- box-wide (admins) ----
	Title         string `json:"title"`
	SetupComplete bool   `json:"setup_complete"`

	// Mail for Send to Kindle. The password is sealed (DATA_DIR/secret.key) and never sent to browsers.
	SMTPHost     string `json:"smtp_host"`
	SMTPPort     int    `json:"smtp_port"`
	SMTPTLS      string `json:"smtp_tls"` // starttls | tls | none
	SMTPUser     string `json:"smtp_user"`
	SMTPPassword string `json:"smtp_password"` // sealed
	SMTPFrom     string `json:"smtp_from"`
	SMTPFromName string `json:"smtp_from_name"`

	// Before an EPUB is mailed to a Kindle, fix what Amazon rejects (language,
	// XML declaration, broken links, stray images).
	KindleFixEPUB bool `json:"kindle_fix_epub"`
	// Downloads and sends carry the metadata as edited here (title, authors,
	// series, cover) written into the EPUB; the file on disk is never touched.
	EmbedMetadata bool `json:"embed_metadata"`
	// Metadata lookups.
	GoogleBooksKey string `json:"google_books_key"` // sealed
	// Catalogue for e-readers.
	OPDSEnabled   bool `json:"opds_enabled"`
	KosyncEnabled bool `json:"kosync_enabled"`
	// The ingest folder: dropped files are filed into the uploads library.
	IngestEnabled bool `json:"ingest_enabled"`

	// ---- personal ----
	DateFormat string `json:"date_format"`
	TimeFormat string `json:"time_format"`

	// The web reader, as JSON the reader owns (fonts, size, spacing, theme, flow…).
	ReaderPrefs string `json:"reader_prefs"`
	ComicPrefs  string `json:"comic_prefs"`

	HiddenLibraries string `json:"hidden_libraries"` // comma list of library ids left out of Home and browsing
	DefaultDevice   int64  `json:"default_device"`   // the device "Send" goes to first
	LibraryView     string `json:"library_view"`     // grid | list
	CardSize        string `json:"card_size"`        // s | m | l

	// Goals.
	DailyGoal  int `json:"daily_goal"`  // minutes a day (0 = none)
	YearlyGoal int `json:"yearly_goal"` // books a year (0 = none)

	// Pushes.
	NotifyURL      string `json:"notify_url"`
	NotifyFormat   string `json:"notify_format"` // apprise | ntfy | json
	NotifyFinished bool   `json:"notify_finished"`
	NotifySent     bool   `json:"notify_sent"`   // a send to a device failed or a pull was fetched
	NotifyAdded    bool   `json:"notify_added"`  // new books in the library (daily digest)
	NotifyWeekly   bool   `json:"notify_weekly"` // Monday recap
}

// boxKeys are the settings shared by the whole box (admin-only); every other
// key is personal and lives in user_settings.
var boxKeys = map[string]bool{
	"title": true, "setup_complete": true,
	"smtp_host": true, "smtp_port": true, "smtp_tls": true, "smtp_user": true, "smtp_password": true, "smtp_from": true, "smtp_from_name": true,
	"kindle_fix_epub": true, "embed_metadata": true, "google_books_key": true,
	"opds_enabled": true, "kosync_enabled": true, "ingest_enabled": true,
}

// IsBoxKey reports whether a setting is box-wide.
func IsBoxKey(k string) bool { return boxKeys[k] }

// defaults returns the settings a person starts with.
func (s *Store) defaults() Settings {
	return Settings{Title: DefaultTitle, DateFormat: "auto", TimeFormat: "auto",
		SMTPPort: 587, SMTPTLS: "starttls", KindleFixEPUB: true, EmbedMetadata: true,
		OPDSEnabled: true, KosyncEnabled: true, IngestEnabled: true,
		LibraryView: "grid", CardSize: "m",
		NotifyFormat: "apprise", NotifySent: true}
}

// Every setting is stored as its JSON value under its JSON name, so adding a
// field to Settings needs no code here. Unknown keys are ignored.
func toKV(st Settings) map[string]json.RawMessage {
	b, _ := json.Marshal(st)
	var kv map[string]json.RawMessage
	_ = json.Unmarshal(b, &kv)
	return kv
}

func (s *Store) readKV(ctx context.Context, q string, args ...any) (map[string]json.RawMessage, error) {
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]json.RawMessage{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		if json.Valid([]byte(v)) {
			out[k] = json.RawMessage(v)
		}
	}
	return out, rows.Err()
}

// GetSettings loads the box-wide settings plus the personal settings of one
// user (uid 0 = box-wide only, personal fields at their defaults).
func (s *Store) GetSettings(ctx context.Context, uid int64) (Settings, error) {
	kv := toKV(s.defaults())
	box, err := s.readKV(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return s.defaults(), err
	}
	for k, v := range box {
		if boxKeys[k] {
			kv[k] = v
		}
	}
	if uid > 0 {
		mine, err := s.readKV(ctx, `SELECT key, value FROM user_settings WHERE user_id = ?`, uid)
		if err != nil {
			return s.defaults(), err
		}
		for k, v := range mine {
			if !boxKeys[k] {
				kv[k] = v
			}
		}
	}
	b, _ := json.Marshal(kv)
	st := s.defaults()
	if err := json.Unmarshal(b, &st); err != nil {
		return s.defaults(), err
	}
	if st.Title == "" {
		st.Title = DefaultTitle
	}
	return st, nil
}

// SaveBoxSettings persists the box-wide settings.
func (s *Store) SaveBoxSettings(ctx context.Context, st Settings) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for k, v := range toKV(st) {
		if !boxKeys[k] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, string(v)); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// SaveUserSettings persists one user's personal settings.
func (s *Store) SaveUserSettings(ctx context.Context, uid int64, st Settings) error {
	if uid <= 0 {
		return ErrNotFound
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for k, v := range toKV(st) {
		if boxKeys[k] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_settings (user_id, key, value) VALUES (?, ?, ?) ON CONFLICT(user_id, key) DO UPDATE SET value = excluded.value`, uid, k, string(v)); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// SettingsUsers lists every account (candidates for background work).
func (s *Store) SettingsUsers(ctx context.Context) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM users ORDER BY id`)
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
