package server

// Uploads and the ingest folder: new files are filed into the uploads
// library by its template ("{author}/{author} - {title}") and read at once.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/abjelosevic88/pocket-homelab-library/internal/formats"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

// ingestEntry is what a file in the ingest folder looked like at a poll.
type ingestEntry struct {
	size  int64
	mtime time.Time
	since time.Time // unchanged since
}

func dirOf(p string) string { return filepath.Dir(p) }

var badPathChars = regexp.MustCompile(`[/\\:*?"<>|\x00-\x1f]+`)

// safeComponent makes a string usable as one folder or file name.
func safeComponent(s string) string {
	s = badPathChars.ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(s), " ")
	s = strings.Trim(s, ". ")
	for len(s) > 120 {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	s = strings.TrimSpace(s)
	if s == "" {
		s = "Unknown"
	}
	return s
}

// templatePath fills a library's upload template for a book.
func templatePath(tmpl string, m *formats.Meta, original string) string {
	if strings.TrimSpace(tmpl) == "" {
		tmpl = "{author}/{author} - {title}"
	}
	author := "Unknown"
	if len(m.Authors) > 0 {
		author = m.Authors[0]
	}
	title := m.Title
	if title == "" {
		title = formats.BaseName(original)
	}
	year := ""
	if len(m.Published) >= 4 {
		year = m.Published[:4]
	}
	idx := ""
	if m.Series != "" && m.SeriesIndex > 0 {
		idx = strconv.FormatFloat(m.SeriesIndex, 'f', -1, 64)
	}
	vals := map[string]string{"author": author, "title": title, "series": m.Series, "series_index": idx, "year": year, "language": m.Language}
	var parts []string
	for _, comp := range strings.Split(tmpl, "/") {
		out := comp
		for k, v := range vals {
			out = strings.ReplaceAll(out, "{"+k+"}", v)
		}
		out = strings.TrimSpace(strings.Trim(strings.TrimSpace(out), "-–#"))
		if out == "" || strings.Trim(out, " -–()[]#") == "" {
			continue // an empty series etc.
		}
		parts = append(parts, safeComponent(out))
	}
	if len(parts) == 0 {
		parts = []string{safeComponent(title)}
	}
	return filepath.Join(parts...)
}

// bookExt is the file's extension as the library keeps it.
func bookExt(name string) string {
	low := strings.ToLower(name)
	for _, e := range []string{".kepub.epub", ".fb2.zip"} {
		if strings.HasSuffix(low, e) {
			return e
		}
	}
	return strings.ToLower(filepath.Ext(name))
}

// fileInto moves (or copies) a book file into a library by its template and
// returns where it landed; an existing file is never overwritten.
func (s *Server) fileInto(lib *store.Library, src, original string, move bool) (string, error) {
	if len(lib.Paths) == 0 {
		return "", errors.New("the library has no folder")
	}
	m, err := formats.Read(src, false)
	if err != nil {
		m = &formats.Meta{}
	}
	rel := templatePath(lib.UploadTemplate, m, original)
	ext := bookExt(original)
	dest := filepath.Join(lib.Paths[0], rel+ext)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	for i := 2; ; i++ {
		if _, err := os.Lstat(dest); errors.Is(err, os.ErrNotExist) {
			break
		}
		dest = filepath.Join(lib.Paths[0], fmt.Sprintf("%s (%d)%s", rel, i, ext))
		if i > 99 {
			return "", errors.New("too many files with that name")
		}
	}
	if move {
		if err := os.Rename(src, dest); err == nil {
			return dest, nil
		}
	}
	if err := copyFile(src, dest); err != nil {
		_ = os.Remove(dest)
		return "", err
	}
	if move {
		_ = os.Remove(src)
	}
	return dest, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// registerFiles reads freshly filed files into the catalogue and returns their books.
func (s *Server) registerFiles(ctx context.Context, lib *store.Library, paths []string) []int64 {
	dirs := map[string]bool{}
	var list []string
	for _, p := range paths {
		if !dirs[filepath.Dir(p)] {
			dirs[filepath.Dir(p)] = true
			list = append(list, filepath.Dir(p))
		}
	}
	if _, err := s.scan.scanner.ScanDirs(ctx, lib, list, false); err != nil {
		s.log.Warn("register uploads", "err", err)
	}
	var ids []int64
	seen := map[int64]bool{}
	for _, p := range paths {
		if id, err := s.store.BookIDByPath(ctx, p); err == nil && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

type skipped struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// handleUploadBooks takes book files (multipart "files", optional
// "library_id"), files them into the library and reads them at once.
func (s *Server) handleUploadBooks(w http.ResponseWriter, r *http.Request) {
	if !s.requireEditor(w, r) {
		return
	}
	ctx := r.Context()
	u := userFrom(ctx)
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBookUploadBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "expected a multipart upload")
		return
	}
	tmpDir := filepath.Join(s.cfg.CacheDir, "uploads")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		s.fail(w, err, "upload")
		return
	}
	type staged struct{ path, name string }
	var files []staged
	var skip []skipped
	defer func() {
		for _, f := range files {
			_ = os.Remove(f.path)
		}
	}()
	libID := int64(0)
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the upload is larger than %d MB", s.cfg.MaxBookUploadBytes>>20))
				return
			}
			writeErr(w, http.StatusBadRequest, "upload: "+err.Error())
			return
		}
		switch part.FormName() {
		case "library_id":
			b, _ := io.ReadAll(io.LimitReader(part, 32))
			libID, _ = strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		case "files", "file":
			name := filepath.Base(part.FileName())
			if formats.Detect(name) == "" {
				skip = append(skip, skipped{Name: name, Reason: "not a book format"})
				_, _ = io.Copy(io.Discard, part)
				continue
			}
			p, err := stagePart(tmpDir, part, bookExt(name))
			if err != nil {
				var mbe *http.MaxBytesError
				if errors.As(err, &mbe) {
					writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the upload is larger than %d MB", s.cfg.MaxBookUploadBytes>>20))
					return
				}
				s.fail(w, err, "upload")
				return
			}
			files = append(files, staged{path: p, name: name})
		default:
			_, _ = io.Copy(io.Discard, part)
		}
		part.Close()
	}
	var lib *store.Library
	if libID > 0 {
		allowed := false
		for _, id := range s.allowedLibraries(ctx, u) {
			allowed = allowed || id == libID
		}
		if !allowed {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		lib, err = s.store.GetLibrary(ctx, libID)
	} else {
		lib, err = s.store.UploadLibrary(ctx)
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, "there is no library to upload into")
		return
	}
	s.scan.writeMu.Lock()
	defer s.scan.writeMu.Unlock()
	var landed []string
	for _, f := range files {
		dest, err := s.fileInto(lib, f.path, f.name, true)
		if err != nil {
			skip = append(skip, skipped{Name: f.name, Reason: err.Error()})
			continue
		}
		landed = append(landed, dest)
		s.log.Info("book uploaded", "by", u.Email, "library", lib.Name, "path", dest)
	}
	ids := s.registerFiles(ctx, lib, landed)
	if skip == nil {
		skip = []skipped{}
	}
	if ids == nil {
		ids = []int64{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"books": ids, "skipped": skip})
}

// stagePart keeps the extension: formats are told apart by it.
func stagePart(dir string, part *multipart.Part, ext string) (string, error) {
	f, err := os.CreateTemp(dir, "up-*"+ext)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, part); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), f.Close()
}

// ---- the ingest folder ----

// ingestWorker files whatever lands in INGEST_DIR (once it stopped changing
// for a minute) into the uploads library.
func (s *Server) ingestWorker(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		st, err := s.store.GetSettings(ctx, 0)
		if err != nil || !st.IngestEnabled {
			continue
		}
		s.ingestOnce(ctx, time.Minute)
	}
}

// ingestOnce takes the files that have been quiet for at least quiet.
func (s *Server) ingestOnce(ctx context.Context, quiet time.Duration) int {
	root := s.cfg.IngestDir
	if root == "" {
		return 0
	}
	now := time.Now()
	var ready []string
	var dirs []string
	s.scan.ingestMu.Lock()
	seen := map[string]bool{}
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root {
				if skipIngest(d.Name()) {
					return filepath.SkipDir
				}
				dirs = append(dirs, p)
			}
			return nil
		}
		if skipIngest(d.Name()) {
			return nil
		}
		if formats.Detect(p) == "" {
			if !s.scan.ingestBad[p] {
				s.scan.ingestBad[p] = true
				s.log.Info("ingest: not a book, left in place", "path", p)
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		seen[p] = true
		prev, ok := s.scan.ingestSeen[p]
		if !ok || prev.size != info.Size() || !prev.mtime.Equal(info.ModTime()) {
			s.scan.ingestSeen[p] = ingestEntry{size: info.Size(), mtime: info.ModTime(), since: now}
			if quiet > 0 {
				return nil
			}
			prev = s.scan.ingestSeen[p]
		}
		if now.Sub(prev.since) >= quiet {
			ready = append(ready, p)
		}
		return nil
	})
	for p := range s.scan.ingestSeen {
		if !seen[p] {
			delete(s.scan.ingestSeen, p)
		}
	}
	s.scan.ingestMu.Unlock()
	if len(ready) == 0 {
		return 0
	}
	lib, err := s.store.UploadLibrary(ctx)
	if err != nil {
		s.log.Warn("ingest: no library to file into")
		return 0
	}
	s.scan.writeMu.Lock()
	var landed []string
	for _, p := range ready {
		dest, err := s.fileInto(lib, p, filepath.Base(p), true)
		if err != nil {
			s.log.Warn("ingest: could not file", "path", p, "err", err)
			continue
		}
		s.log.Info("ingest: filed", "from", p, "to", dest)
		landed = append(landed, dest)
		s.scan.ingestMu.Lock()
		delete(s.scan.ingestSeen, p)
		s.scan.ingestMu.Unlock()
	}
	ids := s.registerFiles(ctx, lib, landed)
	s.scan.writeMu.Unlock()
	// empty folders left behind, deepest first
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Remove(dirs[i]) // only succeeds when empty
	}
	if len(ids) > 0 {
		s.notifyAdded(ctx, lib, ids)
	}
	return len(landed)
}

func skipIngest(name string) bool {
	low := strings.ToLower(name)
	return strings.HasPrefix(name, ".") || name == "@eaDir" || strings.HasSuffix(low, ".part") || strings.HasSuffix(low, ".crdownload") || strings.HasSuffix(low, ".tmp")
}
