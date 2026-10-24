// Package server wires the HTTP API, auth and the embedded SPA.
package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/abjelosevic88/pocket-homelab-library/internal/config"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// Version is injected at build time.
var Version = "dev"

// Server holds dependencies. Each area keeps its own state in its own struct
// (scan.go, devices.go, reader.go, kosync.go), so they stay apart.
type Server struct {
	cfg      *config.Config
	store    *store.Store
	log      *slog.Logger
	webFS    fs.FS
	started  time.Time
	requests atomic.Int64
	errors   atomic.Int64
	http     *http.Client // outbound lookups (metadata, Core links)

	oidcMu sync.Mutex
	oidc   *oidcProvider // for the issuer in use; rebuilt when it changes

	core    *core.Client
	coreMu  sync.Mutex
	coreOK  bool
	coreErr string

	scan    *scanState    // libraries: scanning, uploads, ingest, metadata (scan.go)
	devices *deviceState  // devices, Send to Kindle, conversions, imports (devices.go)
	reader  *readerState  // the web reader: files, pages, progress, sessions (reader.go)
	sync    *kosyncState  // KOReader sync, OPDS, the KOReader plugin, CrossPoint (kosync.go)
}

// New creates a server.
func New(cfg *config.Config, st *store.Store, log *slog.Logger, webFS fs.FS) *Server {
	s := &Server{cfg: cfg, store: st, log: log, webFS: webFS, started: time.Now(), oidc: &oidcProvider{},
		http: &http.Client{Timeout: 30 * time.Second},
		core: core.New(cfg.CoreURL, core.Options{DB: st.DB, TokenFile: cfg.DataDir + "/core.token", UserAgent: cfg.UserAgent})}
	s.scan = newScanState()
	s.devices = newDeviceState()
	s.reader = newReaderState()
	s.sync = newKosyncState()
	return s
}

// Bootstrap seeds the first admin (if configured), the libraries and the
// owner's mail and Kindle from the environment, then starts the background work.
func (s *Server) Bootstrap(ctx context.Context) error {
	if err := s.core.Start(ctx); err != nil {
		return err
	}
	if err := s.loadSecretKey(); err != nil {
		return err
	}
	for _, d := range []string{s.cfg.CoversDir, s.cfg.CacheDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("data dir: %w", err)
		}
	}
	n, err := s.store.CountUsers(ctx)
	if err != nil {
		return err
	}
	if n == 0 && s.cfg.AuthMode == "local" && s.cfg.AdminEmail != "" && s.cfg.AdminPassword != "" {
		if len(s.cfg.AdminPassword) < 8 {
			return errors.New("ADMIN_PASSWORD must be at least 8 characters")
		}
		h, _ := bcrypt.GenerateFromPassword([]byte(s.cfg.AdminPassword), bcrypt.DefaultCost)
		u := &store.User{Email: s.cfg.AdminEmail, Name: s.cfg.AdminName, PasswordHash: string(h), Role: "admin"}
		if err := s.store.CreateUser(ctx, u); err != nil {
			return err
		}
		st, _ := s.store.GetSettings(ctx, 0)
		st.SetupComplete = true
		if err := s.store.SaveBoxSettings(ctx, st); err != nil {
			return err
		}
		s.log.Info("admin user created from environment", "email", u.Email)
	}
	if err := s.seedBox(ctx); err != nil {
		return err
	}
	if owner := s.store.OwnerID(ctx); owner > 0 {
		if err := s.ownerReady(ctx, owner); err != nil {
			return err
		}
	}
	_ = s.store.PurgeSessions(ctx)
	go s.janitor(ctx)
	s.startScan(ctx)
	s.startDevices(ctx)
	s.startReader(ctx)
	s.startKosync(ctx)
	if s.core.Configured() {
		go s.coreLoop(ctx)
	}
	return nil
}

// seedBox copies box-wide things from the environment once: the libraries
// (only while there are none) and the mail account.
func (s *Server) seedBox(ctx context.Context) error {
	once := func(key string) bool {
		if _, _, done := s.store.CacheGet(ctx, "seeded:box:"+key); done {
			return false
		}
		_ = s.store.CachePut(ctx, "seeded:box:"+key, "1")
		return true
	}
	if libs := s.cfg.SeedLibraries(); len(libs) > 0 {
		if n, _ := s.store.CountLibraries(ctx); n == 0 && once("libraries") {
			for i, l := range libs {
				lib := &store.Library{Name: l.Name, Kind: l.Kind, Paths: l.Paths, Access: "all", ScanEvery: 60, Position: i, Uploads: i == 0,
					UploadTemplate: "{author}/{author} - {title}"}
				if err := s.store.CreateLibrary(ctx, lib); err != nil {
					return err
				}
				s.log.Info("library created from environment", "name", lib.Name, "kind", lib.Kind, "paths", lib.Paths)
			}
		}
	}
	if s.cfg.SMTPHost != "" && once("smtp") {
		st, err := s.store.GetSettings(ctx, 0)
		if err != nil {
			return err
		}
		if st.SMTPHost == "" {
			st.SMTPHost, st.SMTPPort, st.SMTPTLS, st.SMTPUser = s.cfg.SMTPHost, s.cfg.SMTPPort, s.cfg.SMTPTLS, s.cfg.SMTPUser
			st.SMTPFrom, st.SMTPFromName = s.cfg.SMTPFrom, s.cfg.SMTPFromName
			if s.cfg.SMTPPassword != "" {
				if st.SMTPPassword, err = s.seal(s.cfg.SMTPPassword); err != nil {
					return err
				}
			}
			if err := s.store.SaveBoxSettings(ctx, st); err != nil {
				return err
			}
			s.log.Info("mail account copied from the environment", "host", st.SMTPHost)
		}
	}
	if s.cfg.GoogleBooksKey != "" && once("google") {
		st, err := s.store.GetSettings(ctx, 0)
		if err == nil && st.GoogleBooksKey == "" {
			if st.GoogleBooksKey, err = s.seal(s.cfg.GoogleBooksKey); err == nil {
				_ = s.store.SaveBoxSettings(ctx, st)
			}
		}
	}
	return nil
}

// ownerReady runs once the box owner (the first admin) exists: the
// integrations from the environment are the owner's — seeded into the owner's
// own settings only, never into anyone else's.
func (s *Server) ownerReady(ctx context.Context, owner int64) error {
	st, err := s.store.GetSettings(ctx, owner)
	if err != nil {
		return err
	}
	changed := false
	set := func(name string, dst *string, v string) {
		key := fmt.Sprintf("seeded:%d:%s", owner, name)
		if _, _, done := s.store.CacheGet(ctx, key); done || v == "" {
			return
		}
		if *dst == "" {
			*dst = v
			changed = true
		}
		_ = s.store.CachePut(ctx, key, "1")
	}
	set("NotifyURL", &st.NotifyURL, s.cfg.NotifyURL)
	if s.cfg.NotifyFormat != "" && st.NotifyURL == s.cfg.NotifyURL && st.NotifyFormat != s.cfg.NotifyFormat {
		st.NotifyFormat = s.cfg.NotifyFormat
		changed = true
	}
	if changed {
		if err := s.store.SaveUserSettings(ctx, owner, st); err != nil {
			return err
		}
	}
	if s.cfg.KindleEmail != "" {
		key := fmt.Sprintf("seeded:%d:kindle", owner)
		if _, _, done := s.store.CacheGet(ctx, key); !done {
			if err := s.seedOwnerKindle(ctx, owner, s.cfg.KindleEmail); err != nil {
				s.log.Warn("kindle from the environment", "err", err)
			} else {
				_ = s.store.CachePut(ctx, key, "1")
			}
		}
	}
	return nil
}

// janitor runs the box-wide housekeeping; each area runs its own loops.
func (s *Server) janitor(ctx context.Context) {
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = s.store.PurgeSessions(ctx)
		}
	}
}

// forgetUser drops in-memory state of a deleted account.
func (s *Server) forgetUser(id int64) {
	s.reader.forget(id)
}

// ---- Pocket Core ----

// coreLoop registers with Core on start (retrying until it answers) and
// re-registers every 30 minutes so version and last-seen stay fresh.
func (s *Server) coreLoop(ctx context.Context) {
	wait := 5 * time.Second
	for {
		err := s.registerWithCore(ctx)
		s.coreMu.Lock()
		s.coreOK, s.coreErr = err == nil, ""
		if err != nil {
			s.coreErr = err.Error()
		}
		s.coreMu.Unlock()
		if err != nil {
			s.log.Warn("pocket core: registration failed, retrying", "err", err, "in", wait.String())
		} else {
			wait = 30 * time.Minute
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if err != nil && wait < 5*time.Minute {
			wait *= 2
		}
	}
}

// eventTopics are what Library publishes on the Core bus.
var eventTopics = []string{"library.book_added", "library.started", "library.finished", "library.highlighted", "library.sent"}

func (s *Server) registerWithCore(ctx context.Context) error {
	m := core.Manifest{
		Slug: "library", Name: "Pocket Library",
		Description: "Every ebook, comic and manga in one place: a library that files and fixes your books, reads EPUB, PDF, MOBI, comics and manga in the browser, keeps your place in step with KOReader and CrossPoint, and sends books to your Kindle, Xteink and other e-readers.",
		Icon:        "/favicon.svg", Version: Version,
		URL: s.cfg.CoreAppURL, PublicURL: s.cfg.BaseURL, HealthPath: "/healthz", EnvPrefix: "LIBRARY",
		Publishes: eventTopics,
		Offers:    linkOffers,
		Uses:      linkUses,
	}
	if s.cfg.BaseURL != "" {
		// ask Core for a relying-party client so "Sign in with Pocket Core" needs no configuration
		m.OIDC = &core.OIDCRequest{RedirectURIs: []string{s.oidcCallbackURL()}}
	}
	reg, err := s.core.Register(ctx, s.cfg.CoreRegistrationKey, m)
	if err != nil {
		return err
	}
	if reg.Token != "" {
		s.log.Info("pocket core: registered, app token stored", "core", s.cfg.CoreURL)
	} else {
		s.log.Debug("pocket core: re-registered", "core", s.cfg.CoreURL)
	}
	return nil
}

// coreStatus summarises the Pocket Core link for the About page.
func (s *Server) coreStatus() map[string]any {
	s.coreMu.Lock()
	defer s.coreMu.Unlock()
	return map[string]any{"configured": s.core.Configured(), "url": s.cfg.CoreURL, "registered": s.coreOK, "error": s.coreErr}
}

// coreConnected is "configured AND registered": only then are Core-only choices live.
func (s *Server) coreConnected() bool {
	s.coreMu.Lock()
	defer s.coreMu.Unlock()
	return s.core.Configured() && s.coreOK
}

// publish puts an event on the Core bus for one person (no-op without Core).
func (s *Server) publish(ctx context.Context, u *store.User, topic, subject string, payload map[string]any) {
	if !s.core.Configured() || u == nil {
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["user_email"] = u.Email
	if err := s.core.Publish(ctx, topic, subject, payload); err != nil {
		s.log.Debug("pocket core: publish failed", "topic", topic, "err", err)
	}
}
