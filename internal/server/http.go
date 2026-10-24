package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

// Router builds the chi router. Each area registers its own routes:
//
//	routesCatalog  (catalog.go)  libraries, books, authors, series, tags, shelves, home, search, stats
//	routesScan     (scan.go)     scanning, uploads, the ingest folder, metadata lookups, covers, duplicates
//	routesReader   (reader.go)   file contents, comic pages, progress, annotations, reading sessions
//	routesDevices  (devices.go)  devices, Send to Kindle / to a device, downloads, conversions, imports
//	routesSync     (kosync.go)   at the root: /opds, /kosync, /koreader (plugin API), CrossPoint clippings
//
// `api` is behind sign-in with compression and a timeout; `raw` is behind
// sign-in without either (files, covers, page images, uploads).
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	if s.cfg.TrustProxy {
		r.Use(middleware.RealIP)
	}
	r.Use(middleware.RequestID)
	r.Use(s.logging)
	r.Use(middleware.Recoverer)

	r.Get("/pocket-ask.js", s.core.AskWidget("library"))
	r.Get("/healthz", s.handleHealth)
	r.Get("/readyz", s.handleReady)
	r.Get("/api/health", s.handleHealth)
	if s.cfg.MetricsEnabled {
		r.Get("/metrics", s.handleMetrics)
	}

	r.Route("/api/v1", func(r chi.Router) {
		r.Group(func(raw chi.Router) {
			raw.Use(s.requireAuth)
			s.rawCatalog(raw)
			s.rawScan(raw)
			s.rawReader(raw)
			s.rawDevices(raw)
		})

		r.Group(func(r chi.Router) {
			r.Use(middleware.Compress(5))
			r.Use(middleware.Timeout(120 * time.Second))
			r.Post("/auth/login", s.handleLogin)
			r.Post("/auth/setup", s.handleSetup)
			r.Get("/auth/status", s.handleAuthStatus)
			r.Get("/auth/oidc/start", s.handleOIDCStart)
			r.Get("/auth/oidc/callback", s.handleOIDCCallback)

			r.Group(func(api chi.Router) {
				api.Use(s.requireAuth)
				api.Post("/auth/logout", s.handleLogout)
				api.Get("/auth/me", s.handleMe)
				api.Put("/auth/me", s.handleUpdateMe)
				api.Post("/client-error", s.handleClientError)

				api.Get("/users", s.handleListUsers)
				api.Post("/users", s.handleCreateUser)
				api.Put("/users/{id}", s.handleUpdateUser)
				api.Delete("/users/{id}", s.handleDeleteUser)

				api.Get("/settings", s.handleGetSettings)
				api.Put("/settings", s.handlePutSettings)
				api.Get("/settings/system", s.handleSystemInfo)
				api.Post("/integrations/test", s.handleIntegrationTest)

				api.Get("/tokens", s.handleListTokens)
				api.Post("/tokens", s.handleCreateToken)
				api.Delete("/tokens/{id}", s.handleDeleteToken)

				api.Get("/backup", s.handleBackup)
				api.Get("/export.json", s.handleExportJSON)
				api.Post("/import", s.handleImportJSON)

				s.routesCatalog(api)
				s.routesScan(api)
				s.routesReader(api)
				s.routesDevices(api)
			})
		})
	})

	// e-readers: OPDS, KOReader sync, the KOReader plugin, CrossPoint
	s.routesSync(r)

	r.NotFound(s.handleSPA)
	return r
}

// ---------- helpers ----------

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		s.requests.Add(1)
		if ww.Status() >= 500 {
			s.errors.Add(1)
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			return
		}
		s.log.Debug("http", "method", r.Method, "path", r.URL.Path, "status", ww.Status(), "dur", time.Since(start).String(), "ip", r.RemoteAddr)
	})
}

type apiError struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiError{Error: msg})
}

func (s *Server) fail(w http.ResponseWriter, err error, context string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
		return
	case errors.Is(err, store.ErrConflict), errors.Is(err, core.ErrOperationConflict):
		writeErr(w, http.StatusConflict, "already exists")
		return
	}
	s.log.Error(context, "err", err)
	writeErr(w, http.StatusInternalServerError, context+": "+err.Error())
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 32<<20))
	return dec.Decode(v)
}

// decodeKeys decodes JSON into v and also reports which top-level keys were
// present, so PUT handlers can tell "not sent" from "sent empty".
func decodeKeys(r *http.Request, v any) (map[string]bool, error) {
	b, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	keys := make(map[string]bool, len(raw))
	for k := range raw {
		keys[k] = true
	}
	return keys, json.Unmarshal(b, v)
}

func idParam(r *http.Request, name string) int64 {
	n, _ := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	return n
}

func queryInt(r *http.Request, name string) int {
	n, _ := strconv.Atoi(r.URL.Query().Get(name))
	return n
}

func queryInt64(r *http.Request, name string) int64 {
	n, _ := strconv.ParseInt(r.URL.Query().Get(name), 10, 64)
	return n
}

func queryBool(r *http.Request, name string) bool {
	v := strings.ToLower(r.URL.Query().Get(name))
	return v == "1" || v == "true" || v == "yes"
}

func validDate(s string) bool {
	if s == "" {
		return true
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ---------- health / metrics ----------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"core_outbox": s.coreOutboxHealth(r.Context()), "status": "ok", "version": Version, "uptime": time.Since(s.started).Round(time.Second).String()})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DB.PingContext(r.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	counts := s.store.Counts(ctx)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintf(w, "# HELP pocket_library_info Build info.\n# TYPE pocket_library_info gauge\npocket_library_info{version=%q} 1\n", Version)
	fmt.Fprintf(w, "# HELP pocket_library_uptime_seconds Process uptime.\n# TYPE pocket_library_uptime_seconds gauge\npocket_library_uptime_seconds %d\n", int(time.Since(s.started).Seconds()))
	fmt.Fprintf(w, "# HELP pocket_library_http_requests_total HTTP requests served.\n# TYPE pocket_library_http_requests_total counter\npocket_library_http_requests_total %d\n", s.requests.Load())
	fmt.Fprintf(w, "# HELP pocket_library_http_errors_total HTTP 5xx responses.\n# TYPE pocket_library_http_errors_total counter\npocket_library_http_errors_total %d\n", s.errors.Load())
	fmt.Fprintf(w, "# HELP pocket_library_rows Rows per table.\n# TYPE pocket_library_rows gauge\n")
	for k, v := range counts {
		fmt.Fprintf(w, "pocket_library_rows{table=%q} %d\n", k, v)
	}
}

// ---------- static / SPA ----------

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if s.webFS == nil {
		http.Error(w, "web UI not built (run `make web`)", http.StatusNotFound)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if f, err := s.webFS.Open(path); err == nil {
		f.Close()
		if strings.HasPrefix(path, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.FileServerFS(s.webFS).ServeHTTP(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	r.URL.Path = "/"
	http.FileServerFS(s.webFS).ServeHTTP(w, r)
}

// handleClientError logs a crash the web app caught (ErrorBoundary).
func (s *Server) handleClientError(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Where   string `json:"where"`
		Message string `json:"message"`
		Stack   string `json:"stack"`
		URL     string `json:"url"`
	}
	_ = decode(r, &in)
	s.log.Warn("web app error", "where", trim(in.Where, 60), "message", trim(in.Message, 500), "url", trim(in.URL, 200), "stack", trim(in.Stack, 2000))
	w.WriteHeader(http.StatusNoContent)
}
