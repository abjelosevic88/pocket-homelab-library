package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-library/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

// ---- settings ----

func mask(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "••••"
	}
	return s[:4] + "••••" + s[len(s)-4:]
}

// publicSettings is what one person sees: their settings with secrets masked;
// the box-wide mail account only for admins.
func (s *Server) publicSettings(ctx context.Context, u *store.User, st store.Settings) map[string]any {
	admin := u != nil && u.Role == "admin"
	m := map[string]any{
		"core_configured": s.core.Configured(), "core_registered": s.coreStatus()["registered"],
		"title": st.Title, "setup_complete": st.SetupComplete, "base_url": s.cfg.BaseURL,
		"is_admin": admin, "can_edit": u.Editor(),
		"kindle_fix_epub": st.KindleFixEPUB, "embed_metadata": st.EmbedMetadata,
		"opds_enabled": st.OPDSEnabled, "kosync_enabled": st.KosyncEnabled, "ingest_enabled": st.IngestEnabled,
		"ingest_dir": "", "convert_available": s.cfg.ConvertPath() != "",
		"smtp_configured": st.SMTPHost != "" && st.SMTPFrom != "",
		"date_format": st.DateFormat, "time_format": st.TimeFormat,
		"reader_prefs": st.ReaderPrefs, "comic_prefs": st.ComicPrefs,
		"hidden_libraries": st.HiddenLibraries, "default_device": st.DefaultDevice,
		"library_view": st.LibraryView, "card_size": st.CardSize,
		"daily_goal": st.DailyGoal, "yearly_goal": st.YearlyGoal,
		"notify_url": st.NotifyURL, "notify_format": st.NotifyFormat, "notify_finished": st.NotifyFinished,
		"notify_sent": st.NotifySent, "notify_added": st.NotifyAdded, "notify_weekly": st.NotifyWeekly, "notify_configured": st.NotifyURL != "",
		"smtp_host": "", "smtp_port": st.SMTPPort, "smtp_tls": st.SMTPTLS, "smtp_user": "", "smtp_password": "", "smtp_from": "", "smtp_from_name": "",
		"google_books_key": "",
	}
	if admin {
		m["ingest_dir"] = s.cfg.IngestDir
		m["smtp_host"], m["smtp_user"], m["smtp_from"], m["smtp_from_name"] = st.SMTPHost, st.SMTPUser, st.SMTPFrom, st.SMTPFromName
		if st.SMTPPassword != "" {
			m["smtp_password"] = "••••••••"
		}
		if st.GoogleBooksKey != "" {
			if k, err := s.open(st.GoogleBooksKey); err == nil {
				m["google_books_key"] = mask(k)
			}
		}
	}
	return m
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.GetSettings(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), userFrom(r.Context()), st))
}

// handlePutSettings saves the caller's personal settings. Box-wide settings
// are only taken from an admin and silently ignored for everyone else.
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	me := userFrom(r.Context())
	isAdmin := me.Role == "admin"
	cur, err := s.store.GetSettings(r.Context(), me.ID)
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	var in store.Settings
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	oneOf := func(k string, dst *string, v string, allowed ...string) {
		if !keys[k] {
			return
		}
		for _, a := range allowed {
			if v == a {
				*dst = v
				return
			}
		}
	}
	intIn := func(k string, dst *int, v, lo, hi int) {
		if keys[k] && v >= lo && v <= hi {
			*dst = v
		}
	}
	text := func(k string, dst *string, v string) {
		if keys[k] {
			*dst = strings.TrimSpace(v)
		}
	}
	boolIn := func(k string, dst *bool, v bool) {
		if keys[k] {
			*dst = v
		}
	}
	boxChanged := false
	if isAdmin {
		for k := range keys {
			if store.IsBoxKey(k) {
				boxChanged = true
			}
		}
		if keys["title"] {
			cur.Title = strings.TrimSpace(in.Title)
			if cur.Title == "" {
				cur.Title = store.DefaultTitle
			}
		}
		text("smtp_host", &cur.SMTPHost, in.SMTPHost)
		intIn("smtp_port", &cur.SMTPPort, in.SMTPPort, 1, 65535)
		oneOf("smtp_tls", &cur.SMTPTLS, in.SMTPTLS, "starttls", "tls", "none")
		text("smtp_user", &cur.SMTPUser, in.SMTPUser)
		text("smtp_from", &cur.SMTPFrom, in.SMTPFrom)
		text("smtp_from_name", &cur.SMTPFromName, in.SMTPFromName)
		// a masked value coming back means "unchanged"
		if keys["smtp_password"] && !strings.Contains(in.SMTPPassword, "••") {
			cur.SMTPPassword = ""
			if in.SMTPPassword != "" {
				if cur.SMTPPassword, err = s.seal(in.SMTPPassword); err != nil {
					s.fail(w, err, "seal")
					return
				}
			}
		}
		if keys["google_books_key"] && !strings.Contains(in.GoogleBooksKey, "••") {
			cur.GoogleBooksKey = ""
			if k := strings.TrimSpace(in.GoogleBooksKey); k != "" {
				if cur.GoogleBooksKey, err = s.seal(k); err != nil {
					s.fail(w, err, "seal")
					return
				}
			}
		}
		boolIn("kindle_fix_epub", &cur.KindleFixEPUB, in.KindleFixEPUB)
		boolIn("embed_metadata", &cur.EmbedMetadata, in.EmbedMetadata)
		boolIn("opds_enabled", &cur.OPDSEnabled, in.OPDSEnabled)
		boolIn("kosync_enabled", &cur.KosyncEnabled, in.KosyncEnabled)
		boolIn("ingest_enabled", &cur.IngestEnabled, in.IngestEnabled)
	}
	text("date_format", &cur.DateFormat, in.DateFormat)
	text("time_format", &cur.TimeFormat, in.TimeFormat)
	if keys["reader_prefs"] && len(in.ReaderPrefs) < 16<<10 {
		cur.ReaderPrefs = in.ReaderPrefs
	}
	if keys["comic_prefs"] && len(in.ComicPrefs) < 16<<10 {
		cur.ComicPrefs = in.ComicPrefs
	}
	text("hidden_libraries", &cur.HiddenLibraries, in.HiddenLibraries)
	if keys["default_device"] {
		cur.DefaultDevice = in.DefaultDevice
	}
	oneOf("library_view", &cur.LibraryView, in.LibraryView, "grid", "list")
	oneOf("card_size", &cur.CardSize, in.CardSize, "s", "m", "l")
	intIn("daily_goal", &cur.DailyGoal, in.DailyGoal, 0, 600)
	intIn("yearly_goal", &cur.YearlyGoal, in.YearlyGoal, 0, 1000)
	text("notify_url", &cur.NotifyURL, in.NotifyURL)
	if keys["notify_format"] && in.NotifyFormat != "" {
		oneOf("notify_format", &cur.NotifyFormat, in.NotifyFormat, "apprise", "ntfy", "json")
	}
	boolIn("notify_finished", &cur.NotifyFinished, in.NotifyFinished)
	boolIn("notify_sent", &cur.NotifySent, in.NotifySent)
	boolIn("notify_added", &cur.NotifyAdded, in.NotifyAdded)
	boolIn("notify_weekly", &cur.NotifyWeekly, in.NotifyWeekly)
	if err := s.store.SaveUserSettings(r.Context(), me.ID, cur); err != nil {
		s.fail(w, err, "save settings")
		return
	}
	if isAdmin && boxChanged {
		if err := s.store.SaveBoxSettings(r.Context(), cur); err != nil {
			s.fail(w, err, "save settings")
			return
		}
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), me, cur))
}

// handleIntegrationTest checks a connection with the caller's own saved
// settings (or the URL typed into the form).
func (s *Server) handleIntegrationTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Which  string `json:"which"`
		URL    string `json:"url"`
		Key    string `json:"key"`
		Format string `json:"format"`
		To     string `json:"to"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	u := userFrom(r.Context())
	st, _ := s.store.GetSettings(r.Context(), u.ID)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	urlOr := func(saved string) string {
		if strings.TrimSpace(in.URL) == "" {
			return saved
		}
		return strings.TrimRight(strings.TrimSpace(in.URL), "/")
	}
	var msg string
	var err error
	switch in.Which {
	case "notify":
		f := in.Format
		if f == "" {
			f = st.NotifyFormat
		}
		n := notify.New(urlOr(st.NotifyURL), f)
		err = n.Send(ctx, notify.Message{Title: "Pocket Library", Body: "Notifications work. Finished books, sends to your devices and new books will come here.", Priority: "info", Tags: []string{"books"}})
		msg = "Test message sent via " + n.Format
	case "smtp":
		if u.Role != "admin" {
			writeErr(w, http.StatusForbidden, "only an admin can do that")
			return
		}
		to := strings.TrimSpace(in.To)
		if to == "" {
			to = u.Email
		}
		err = s.testMail(ctx, to)
		msg = "Test mail sent to " + to
	default:
		writeErr(w, http.StatusBadRequest, "unknown integration")
		return
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	var size int64
	if fi, err := os.Stat(s.cfg.DBPath); err == nil {
		size = fi.Size()
	}
	me := userFrom(r.Context())
	counts := s.store.CountsFor(r.Context(), me.ID)
	for k, v := range s.store.CatalogCounts(r.Context()) {
		counts[k] = v
	}
	info := map[string]any{
		"version": Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"core":      s.coreStatus(),
		"auth_mode": s.cfg.AuthMode, "db_bytes": size,
		"uptime": time.Since(s.started).Round(time.Second).String(), "timezone": time.Now().Location().String(),
		"counts": counts, "convert": s.cfg.ConvertPath() != "", "base_url": s.cfg.BaseURL,
	}
	if me.Role == "admin" {
		info["data_dir"], info["db_path"], info["ingest_dir"], info["convert_path"] = s.cfg.DataDir, s.cfg.DBPath, s.cfg.IngestDir, s.cfg.ConvertPath()
	}
	writeJSON(w, http.StatusOK, info)
}

// handleBackup downloads the whole database, so it is a box operation for an
// admin only (everyone can export their own data as JSON).
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	dir := filepath.Join(s.cfg.DataDir, "backups")
	name := fmt.Sprintf("pocket-library-%s.db", time.Now().Format("20060102-150405"))
	dst := filepath.Join(dir, name)
	if err := s.store.Backup(r.Context(), dst); err != nil {
		s.fail(w, err, "backup")
		return
	}
	defer os.Remove(dst)
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(w, r, dst)
}
