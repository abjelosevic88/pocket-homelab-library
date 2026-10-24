package server

// Libraries: scanning folders, uploads, the ingest folder, metadata lookups,
// covers and duplicates. One scan runs at a time; the scheduler asks for a
// library's scan when its interval is up, people ask with the Scan button.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-library/internal/metadata"
	"github.com/abjelosevic88/pocket-homelab-library/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-library/internal/scanner"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

// ScanStatus is the scan running now (or the last one).
type ScanStatus struct {
	Running   bool     `json:"running"`
	LibraryID int64    `json:"library_id"`
	Library   string   `json:"library"`
	Phase     string   `json:"phase"`
	Done      int      `json:"done"`
	Total     int      `json:"total"`
	Added     int      `json:"added"`
	Updated   int      `json:"updated"`
	Removed   int      `json:"removed"`
	Errors    []string `json:"errors"`
	StartedAt string   `json:"started_at"`
}

// jobStatus is a background job's progress (metadata fill).
type jobStatus struct {
	Running bool   `json:"running"`
	Done    int    `json:"done"`
	Total   int    `json:"total"`
	Filled  int    `json:"filled"`
	Error   string `json:"error,omitempty"`
}

type scanRequest struct {
	libraryID int64 // 0 = all
	full      bool
}

type scanState struct {
	mu      sync.Mutex
	status  ScanStatus
	queued  map[int64]bool // library ids waiting (0 = all)
	kick    chan scanRequest
	fetch   jobStatus
	meta    *metadata.Client
	scanner *scanner.Scanner

	ingestMu   sync.Mutex
	ingestSeen map[string]ingestEntry // path → what it looked like last time
	ingestBad  map[string]bool        // unsupported files already logged

	writeMu sync.Mutex // uploads, ingest and scans don't file into a folder at the same time
}

func newScanState() *scanState {
	return &scanState{queued: map[int64]bool{}, kick: make(chan scanRequest, 64), ingestSeen: map[string]ingestEntry{}, ingestBad: map[string]bool{}, status: ScanStatus{Errors: []string{}}}
}

func (s *Server) startScan(ctx context.Context) {
	s.scan.meta = metadata.New(s.cfg.UserAgent)
	s.scan.scanner = &scanner.Scanner{Store: s.store, Log: s.log, CoversDir: s.cfg.CoversDir}
	go s.scanWorker(ctx)
	go s.scanScheduler(ctx)
	go s.hashWorker(ctx)
	go s.enrichWorker(ctx)
	if s.cfg.IngestDir != "" {
		go s.ingestWorker(ctx)
	}
}

// scanning reports whether a library is being scanned right now.
func (s *Server) scanning(libraryID int64) bool {
	s.scan.mu.Lock()
	defer s.scan.mu.Unlock()
	return s.scan.status.Running && (s.scan.status.LibraryID == libraryID || libraryID == 0)
}

// kickScan asks for a scan of one library (0 = all) soon.
func (s *Server) kickScan(libraryID int64) { s.requestScan(libraryID, false) }

func (s *Server) requestScan(libraryID int64, full bool) {
	s.scan.mu.Lock()
	if s.scan.queued[libraryID] && !full {
		s.scan.mu.Unlock()
		return
	}
	s.scan.queued[libraryID] = true
	s.scan.mu.Unlock()
	select {
	case s.scan.kick <- scanRequest{libraryID: libraryID, full: full}:
	default:
	}
}

// scanWorker runs the scans one after the other.
func (s *Server) scanWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-s.scan.kick:
			s.scan.mu.Lock()
			delete(s.scan.queued, req.libraryID)
			s.scan.mu.Unlock()
			libs, err := s.store.ListLibraries(ctx)
			if err != nil {
				s.log.Warn("scan: libraries", "err", err)
				continue
			}
			for i := range libs {
				if req.libraryID == 0 || libs[i].ID == req.libraryID {
					s.runScan(ctx, &libs[i], req.full)
				}
			}
		}
	}
}

// scanScheduler asks for each library's scan when its interval is up (and
// for libraries never scanned, shortly after the start).
func (s *Server) scanScheduler(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(15 * time.Second):
	}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		libs, err := s.store.ListLibraries(ctx)
		if err == nil {
			for _, l := range libs {
				if len(l.Paths) == 0 {
					continue
				}
				if l.LastScanAt == "" {
					s.kickScan(l.ID)
					continue
				}
				if s.cfg.ScanEvery <= 0 || l.ScanEvery <= 0 {
					continue
				}
				every := max(time.Duration(l.ScanEvery)*time.Minute, 5*time.Minute)
				if last, err := time.Parse(time.RFC3339Nano, l.LastScanAt); err == nil && time.Since(last) >= every {
					s.kickScan(l.ID)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) runScan(ctx context.Context, lib *store.Library, full bool) {
	start := time.Now()
	s.scan.mu.Lock()
	s.scan.status = ScanStatus{Running: true, LibraryID: lib.ID, Library: lib.Name, Phase: "listing", Errors: []string{}, StartedAt: store.Now()}
	s.scan.mu.Unlock()
	s.scan.writeMu.Lock()
	res, err := s.scan.scanner.Scan(ctx, lib, full, func(p scanner.Progress) {
		s.scan.mu.Lock()
		st := &s.scan.status
		st.Phase, st.Done, st.Total, st.Added, st.Updated, st.Removed = p.Phase, p.Done, p.Total, p.Added, p.Updated, p.Removed
		s.scan.mu.Unlock()
	})
	s.scan.writeMu.Unlock()
	ms := time.Since(start).Milliseconds()
	errText := ""
	if err != nil {
		errText = err.Error()
		s.log.Warn("scan failed", "library", lib.Name, "err", err)
	} else {
		s.log.Info("scan done", "library", lib.Name, "files", res.Files, "books", res.Books, "added", len(res.Added), "updated", res.Updated, "removed", res.Removed, "ms", ms)
	}
	_ = s.store.SetScanResult(ctx, lib.ID, store.Now(), ms, errText)
	s.scan.mu.Lock()
	s.scan.status.Running = false
	s.scan.status.Phase = "done"
	if res != nil {
		s.scan.status.Errors = append([]string{}, res.Errors...)
		s.scan.status.Added = len(res.Added)
	}
	if err != nil {
		s.scan.status.Errors = append(s.scan.status.Errors, errText)
	}
	s.scan.mu.Unlock()
	if res != nil && len(res.Added) > 0 {
		s.notifyAdded(ctx, lib, res.Added)
	}
}

// notifyAdded pushes "new in the library" to the people who asked for it, at
// most once a day each.
func (s *Server) notifyAdded(ctx context.Context, lib *store.Library, ids []int64) {
	users, err := s.store.ListUsers(ctx)
	if err != nil {
		return
	}
	day := store.Today()
	for i := range users {
		u := &users[i]
		st, err := s.store.GetSettings(ctx, u.ID)
		if err != nil || !st.NotifyAdded || st.NotifyURL == "" {
			continue
		}
		allowed := false
		for _, id := range s.allowedLibraries(ctx, u) {
			if id == lib.ID {
				allowed = true
			}
		}
		key := fmt.Sprintf("notify:added:%d:%s", u.ID, day)
		if _, _, sent := s.store.CacheGet(ctx, key); !allowed || sent {
			continue
		}
		books, _, err := s.store.ListBooks(ctx, store.BookQuery{UserID: u.ID, Libraries: []int64{lib.ID}, IDs: ids, Sort: "added", Order: "desc", Limit: 8})
		if err != nil || len(books) == 0 {
			continue
		}
		var lines []string
		for _, b := range books {
			line := "• " + b.Title
			if len(b.Authors) > 0 {
				line += " — " + b.Authors[0].Name
			}
			lines = append(lines, line)
		}
		if len(ids) > len(books) {
			lines = append(lines, fmt.Sprintf("and %d more", len(ids)-len(books)))
		}
		msg := notify.Message{Title: fmt.Sprintf("%d new in %s", len(ids), lib.Name), Body: strings.Join(lines, "\n"), Priority: "info", Tags: []string{"books"}}
		if s.cfg.BaseURL != "" {
			msg.Link = s.cfg.BaseURL + "/library?sort=added&order=desc"
		}
		if err := notify.New(st.NotifyURL, st.NotifyFormat).Send(ctx, msg); err == nil {
			_ = s.store.CachePut(ctx, key, "1")
		}
	}
}

// hashWorker fills the files' whole-file hashes slowly (for duplicates).
func (s *Server) hashWorker(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Minute):
	}
	for {
		files, err := s.store.FilesWithoutHash(ctx, 20)
		wait := 5 * time.Minute
		if err == nil && len(files) > 0 {
			for _, f := range files {
				if ctx.Err() != nil {
					return
				}
				sum, err := scanner.Hash(f.Path)
				if err != nil {
					sum = "-" // unreadable now: not asked again until the file changes
				}
				_ = s.store.SetFileHash(ctx, f.ID, sum)
			}
			wait = 2 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// ---- endpoints ----

func (s *Server) routesScan(api chi.Router) {
	api.Get("/scan", s.handleScanStatus)
	api.Post("/scan", s.handleScanAll)
	api.Post("/libraries/{id}/scan", s.handleScanLibrary)
	api.Post("/books/{id}/rescan", s.handleRescanBook)
	api.Delete("/books/{id}", s.handleDeleteBook)
	api.Get("/books/{id}/metadata/search", s.handleMetadataSearch)
	api.Post("/books/{id}/metadata/apply", s.handleMetadataApply)
	api.Post("/metadata/fetch-missing", s.handleFetchMissing)
	api.Post("/authors/{id}/enrich", s.handleEnrichAuthor)
	api.Get("/duplicates", s.handleDuplicates)
}

func (s *Server) rawScan(raw chi.Router) {
	raw.Post("/upload", s.handleUploadBooks)
	raw.Get("/books/{id}/cover", s.handleCover)
	raw.Head("/books/{id}/cover", s.handleCover)
	raw.Post("/books/{id}/cover", s.handleSetCover)
	raw.Delete("/books/{id}/cover", s.handleDeleteCover)
	raw.Get("/authors/{id}/photo", s.handleAuthorPhoto)
}

func (s *Server) handleScanStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s.scan.mu.Lock()
	st := s.scan.status
	st.Errors = append([]string{}, st.Errors...)
	fetch := s.scan.fetch
	s.scan.mu.Unlock()
	libs, err := s.store.LibrariesFor(ctx, userFrom(ctx))
	if err != nil {
		s.fail(w, err, "libraries")
		return
	}
	type libState struct {
		ID            int64  `json:"id"`
		Scanning      bool   `json:"scanning"`
		LastScanAt    string `json:"last_scan_at"`
		LastScanMS    int64  `json:"last_scan_ms"`
		LastScanError string `json:"last_scan_error"`
	}
	out := []libState{}
	for _, l := range libs {
		out = append(out, libState{ID: l.ID, Scanning: st.Running && st.LibraryID == l.ID, LastScanAt: l.LastScanAt, LastScanMS: l.LastScanMS, LastScanError: l.LastScanError})
	}
	resp := map[string]any{"status": st, "libraries": out, "fetch": fetch,
		"hashing": map[string]int{"pending": s.store.CountFilesWithoutHash(ctx)},
		"enrich":  map[string]int{"pending": s.store.CountAuthorsToEnrich(ctx)}}
	if userFrom(ctx).Role != "admin" {
		st.Errors = []string{} // paths are the admin's business
		resp["status"] = st
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleScanAll(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	s.requestScan(0, queryBool(r, "full"))
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (s *Server) handleScanLibrary(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	lib, err := s.store.GetLibrary(r.Context(), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "library")
		return
	}
	s.requestScan(lib.ID, queryBool(r, "full"))
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

// handleRescanBook reads one book's files again (locked fields stay).
func (s *Server) handleRescanBook(w http.ResponseWriter, r *http.Request) {
	if !s.requireEditor(w, r) {
		return
	}
	b := s.bookFor(w, r, idParam(r, "id"))
	if b == nil {
		return
	}
	lib, err := s.store.GetLibrary(r.Context(), b.LibraryID)
	if err != nil {
		s.fail(w, err, "library")
		return
	}
	var dirs []string
	for _, f := range b.Files {
		dirs = append(dirs, dirOf(f.Path))
	}
	s.scan.writeMu.Lock()
	_, err = s.scan.scanner.ScanDirs(r.Context(), lib, dirs, true)
	s.scan.writeMu.Unlock()
	if err != nil && !errors.Is(err, scanner.ErrRootMissing) {
		s.fail(w, err, "rescan")
		return
	}
	if b = s.bookFor(w, r, b.ID); b != nil {
		writeJSON(w, http.StatusOK, b)
	}
}

// handleDuplicates finds books that are probably the same one twice.
func (s *Server) handleDuplicates(w http.ResponseWriter, r *http.Request) {
	if !s.requireEditor(w, r) {
		return
	}
	ctx := r.Context()
	u := userFrom(ctx)
	libs := s.allowedLibraries(ctx, u)
	type dupGroup struct {
		Reason string       `json:"reason"`
		Books  []store.Book `json:"books"`
	}
	out := []dupGroup{}
	reported := map[string]bool{}
	add := func(reason string, ids []int64) {
		if len(ids) < 2 || len(out) >= 200 {
			return
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		key := fmt.Sprint(ids)
		if reported[key] {
			return
		}
		reported[key] = true
		books, _, err := s.store.ListBooks(ctx, store.BookQuery{UserID: u.ID, Libraries: libs, IDs: ids, ShowHidden: true, Sort: "added", Limit: 50})
		if err == nil && len(books) > 1 {
			out = append(out, dupGroup{Reason: reason, Books: books})
		}
	}
	if groups, err := s.store.DupFileBooks(ctx, libs, 200); err == nil {
		for _, g := range groups {
			add("same file", g)
		}
	}
	if groups, err := s.store.DupISBNBooks(ctx, libs, 200); err == nil {
		for _, g := range groups {
			add("same isbn", g)
		}
	}
	if keys, err := s.store.TitleKeys(ctx, libs); err == nil {
		byKey := map[string][]int64{}
		for _, k := range keys {
			t := metadata.Fold(k.Title)
			if t == "" {
				continue
			}
			key := fmt.Sprintf("%d|%s|%s", k.LibraryID, t, metadata.Fold(k.Author))
			byKey[key] = append(byKey[key], k.ID)
		}
		keysSorted := make([]string, 0, len(byKey))
		for k, ids := range byKey {
			if len(ids) > 1 {
				keysSorted = append(keysSorted, k)
			}
		}
		sort.Strings(keysSorted)
		for _, k := range keysSorted {
			add("same title", byKey[k])
		}
	}
	writeJSON(w, http.StatusOK, out)
}
