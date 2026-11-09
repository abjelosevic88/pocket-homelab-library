package server

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/abjelosevic88/pocket-homelab-library/internal/formats"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

const balkanCollection = "Balkan Downloads"
const balkanMaxBytes int64 = 100 << 20

var balkanPostPath = regexp.MustCompile(`^/topic/[1-9][0-9]*[^/]*(?:/page-[0-9]+)?/?$`)
var balkanEntry = regexp.MustCompile(`^entry[1-9][0-9]*$`)
var errImportShelfConflict = errors.New("rename the existing smart shelf Balkan Downloads before importing")

type balkanImportResult struct {
	BookID       int64  `json:"book_id"`
	FileID       int64  `json:"file_id"`
	BookURL      string `json:"book_url"`
	Collection   string `json:"collection"`
	CollectionID int64  `json:"collection_id"`
	SHA256       string `json:"sha256"`
	Duplicate    bool   `json:"duplicate"`
	SourceURL    string `json:"source_url"`
}

func canonicalBalkanSource(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") ||
		(u.Hostname() != "www.balkandownload.org" && u.Hostname() != "balkandownload.org") ||
		(u.Port() != "" && u.Port() != "80" && u.Port() != "443") || !balkanPostPath.MatchString(u.Path) || !balkanEntry.MatchString(u.Fragment) {
		return "", errors.New("source_url must identify the original BalkanDownload topic and post")
	}
	u.Scheme, u.Host, u.RawQuery = "https", "www.balkandownload.org", ""
	return u.String(), nil
}

func (s *Server) importRoot() string { return filepath.Join(s.cfg.DataDir, "linked-book-imports") }

func overlappingPaths(a, b string) bool {
	a, b = canonicalDiskPath(a), canonicalDiskPath(b)
	within := func(child, parent string) bool {
		rel, err := filepath.Rel(parent, child)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
	}
	return within(a, b) || within(b, a)
}

func canonicalDiskPath(p string) string {
	p, _ = filepath.Abs(p)
	var tail []string
	for {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				real = filepath.Join(real, tail[i])
			}
			return real
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		tail = append(tail, filepath.Base(p))
		p = parent
	}
}

// The receiver assigns all paths. Shared libraries may never cover this tree.
func (s *Server) privateImportLibrary(ctx context.Context, uid int64) (*store.Library, error) {
	libs, err := s.store.ListLibraries(ctx)
	if err != nil {
		return nil, err
	}
	for _, lib := range libs {
		if lib.OwnerUserID != 0 {
			continue
		}
		for _, path := range lib.Paths {
			if overlappingPaths(path, s.importRoot()) {
				return nil, errors.New("the private import folder overlaps a shared library; update that library before importing")
			}
		}
	}
	root := filepath.Join(s.importRoot(), strconv.FormatInt(uid, 10))
	for _, dir := range []string{s.importRoot(), root} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("the private import folder is not a regular directory")
		}
		if err := os.Chmod(dir, 0700); err != nil {
			return nil, err
		}
	}
	lib, err := s.store.ImportedLibrary(ctx, uid)
	if err == nil {
		if len(lib.Paths) != 1 || filepath.Clean(lib.Paths[0]) != filepath.Clean(root) {
			return nil, errors.New("the private import destination has changed")
		}
		return lib, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	lib = &store.Library{Name: balkanCollection, OwnerUserID: uid, Kind: "books", Paths: []string{root}, Access: "people", People: []int64{uid}, ScanEvery: 0}
	if err := s.store.CreateLibrary(ctx, lib); err != nil {
		return nil, err
	}
	_ = s.store.SetScanResult(ctx, lib.ID, store.Now(), 0, "")
	return lib, nil
}

func importExtension(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".epub", ".pdf", ".mobi", ".azw", ".azw3", ".fb2", ".djvu", ".cbz", ".cbr":
		return ext
	case ".djv":
		return ".djvu"
	}
	return ""
}

// Validate the bytes, independently of the sender's filename or supplied hash.
// Archives are only accepted as EPUB/CBZ/CBR; nothing is extracted on disk.
func verifyImportedBook(path, ext string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, 8192)
	n, err := f.Read(head)
	if err != nil && err != io.EOF {
		return err
	}
	head = head[:n]
	bad := errors.New("file bytes do not match a supported ebook format")
	switch ext {
	case ".pdf":
		if !bytes.HasPrefix(head, []byte("%PDF-")) {
			return bad
		}
	case ".mobi", ".azw", ".azw3":
		if len(head) < 78 || (string(head[60:68]) != "BOOKMOBI" && string(head[60:68]) != "TEXtREAd") {
			return bad
		}
	case ".djvu":
		if !bytes.HasPrefix(head, []byte("AT&TFORM")) {
			return bad
		}
	case ".cbr":
		if !bytes.HasPrefix(head, []byte("Rar!\x1a\x07")) {
			return bad
		}
	case ".fb2":
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		d := xml.NewDecoder(f)
		found := false
		for {
			t, err := d.Token()
			if err == io.EOF {
				if found {
					return nil
				}
				return bad
			}
			if err != nil {
				return bad
			}
			if start, ok := t.(xml.StartElement); ok && !found {
				if start.Name.Local != "FictionBook" {
					return bad
				}
				found = true
			}
		}
	case ".epub", ".cbz":
		zr, err := zip.OpenReader(path)
		if err != nil {
			return bad
		}
		defer zr.Close()
		if len(zr.File) == 0 || len(zr.File) > 4096 {
			return bad
		}
		var total uint64
		valid := false
		for _, z := range zr.File {
			if z.UncompressedSize64 > 256<<20 || total > (256<<20)-z.UncompressedSize64 {
				return bad
			}
			total += z.UncompressedSize64
			if ext == ".epub" && z.Name == "mimetype" && z.UncompressedSize64 < 128 {
				r, err := z.Open()
				if err != nil {
					return bad
				}
				b, err := io.ReadAll(io.LimitReader(r, 128))
				r.Close()
				valid = err == nil && strings.TrimSpace(string(b)) == "application/epub+zip"
			}
			if ext == ".cbz" {
				switch strings.ToLower(filepath.Ext(z.Name)) {
				case ".jpg", ".jpeg", ".png", ".webp":
					valid = true
				}
			}
		}
		if !valid {
			return bad
		}
	default:
		return bad
	}
	return nil
}

func (s *Server) handleBalkanBookImport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	u := userFrom(r.Context())
	if u == nil || linkApp(r.Context()) != "books" {
		writeErr(w, http.StatusForbidden, "a person-authorized Pocket Books library:import link is required")
		return
	}
	select {
	case s.bookImports <- struct{}{}:
		defer func() { <-s.bookImports }()
	default:
		w.Header().Set("Retry-After", "5")
		writeErr(w, 429, "two book imports are already running")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(2 * time.Minute))
	defer controller.SetReadDeadline(time.Time{})
	limit := balkanMaxBytes
	if s.cfg.MaxBookUploadBytes > 0 && s.cfg.MaxBookUploadBytes < limit {
		limit = s.cfg.MaxBookUploadBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit+64<<10)
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, 400, "expected a multipart upload")
		return
	}
	dir, err := os.MkdirTemp("", "pocket-library-import-")
	if err != nil {
		writeErr(w, 500, "could not prepare import")
		return
	}
	defer os.RemoveAll(dir)
	fields := map[string]string{}
	var name, ext, staged, sha string
	var size int64
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			importReadError(w, err)
			return
		}
		field := part.FormName()
		if field == "file" {
			if staged != "" {
				writeErr(w, 400, "send exactly one file")
				return
			}
			_, attrs, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
			name = attrs["filename"]
			if err != nil || name == "" || len(name) > 255 || strings.ContainsAny(name, "/\\") || strings.IndexFunc(name, unicode.IsControl) >= 0 || !utf8.ValidString(name) {
				writeErr(w, 400, "use a plain ebook filename")
				return
			}
			ext = importExtension(name)
			if ext == "" {
				writeErr(w, 400, "unsupported ebook format; archives must stay in Pocket Books")
				return
			}
			staged = filepath.Join(dir, "book"+ext)
			f, err := os.OpenFile(staged, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				writeErr(w, 500, "could not stage import")
				return
			}
			h := sha256.New()
			size, err = io.Copy(io.MultiWriter(f, h), io.LimitReader(part, limit+1))
			if err == nil {
				err = f.Sync()
			}
			closeErr := f.Close()
			if err == nil {
				err = closeErr
			}
			if size > limit {
				writeErr(w, 413, "ebook exceeds the import size limit")
				return
			}
			if err != nil {
				importReadError(w, err)
				return
			}
			sha = hex.EncodeToString(h.Sum(nil))
		} else {
			if field != "title" && field != "source_url" && field != "idempotency_key" {
				writeErr(w, 400, "unexpected import field")
				return
			}
			if _, exists := fields[field]; exists {
				writeErr(w, 400, "duplicate import field")
				return
			}
			b, err := io.ReadAll(io.LimitReader(part, 4097))
			if err != nil {
				importReadError(w, err)
				return
			}
			if len(b) > 4096 || !utf8.Valid(b) || strings.IndexFunc(string(b), unicode.IsControl) >= 0 {
				writeErr(w, 400, "invalid import metadata")
				return
			}
			fields[field] = strings.TrimSpace(string(b))
		}
		part.Close()
	}
	if staged == "" || size == 0 {
		writeErr(w, 400, "send exactly one nonempty ebook file")
		return
	}
	source, err := canonicalBalkanSource(fields["source_url"])
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if utf8.RuneCountInString(fields["title"]) > 500 || len(fields["idempotency_key"]) > 200 {
		writeErr(w, 400, "import metadata is too long")
		return
	}
	if err := verifyImportedBook(staged, ext); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if !s.scan.writeMu.TryLock() {
		w.Header().Set("Retry-After", "5")
		writeErr(w, 429, "the catalogue is busy; retry the import shortly")
		return
	}
	defer s.scan.writeMu.Unlock()
	result, err := s.storeBalkanImport(ctx, u.ID, staged, name, ext, sha, size, source, fields["title"], fields["idempotency_key"])
	if err != nil {
		if errors.Is(err, errImportShelfConflict) {
			writeErr(w, 409, err.Error())
			return
		}
		s.log.Warn("linked book import failed", "user", u.ID, "error", err)
		writeErr(w, 500, "the ebook import could not finish; retrying the same file is safe")
		return
	}
	status := 201
	if result.Duplicate {
		status = 200
	}
	writeJSON(w, status, result)
}

func importReadError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeErr(w, 413, "ebook exceeds the import size limit")
		return
	}
	writeErr(w, 400, "could not read the complete ebook upload")
}

func (s *Server) storeBalkanImport(ctx context.Context, uid int64, staged, name, ext, sha string, size int64, source, title, key string) (*balkanImportResult, error) {
	shelves, err := s.store.ListShelves(ctx, uid)
	if err != nil {
		return nil, err
	}
	var shelf *store.Shelf
	for i := range shelves {
		if shelves[i].Name == balkanCollection {
			if shelves[i].Kind != "manual" {
				return nil, errImportShelfConflict
			}
			shelf = &shelves[i]
			break
		}
	}
	lib, err := s.privateImportLibrary(ctx, uid)
	if err != nil {
		return nil, err
	}
	receipt, prior := s.store.LinkedImport(ctx, uid, sha)
	if prior != nil && !errors.Is(prior, store.ErrNotFound) {
		return nil, prior
	}
	dest := filepath.Join(lib.Paths[0], sha+ext)
	// Recover even if a stop happened before the receipt or catalogue rows were
	// saved, and the retry uses another valid extension for identical bytes.
	for _, candidateExt := range []string{".epub", ".pdf", ".mobi", ".azw", ".azw3", ".fb2", ".djvu", ".cbz", ".cbr"} {
		candidate := filepath.Join(lib.Paths[0], sha+candidateExt)
		if _, err := os.Lstat(candidate); err == nil {
			dest = candidate
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	if info, err := os.Lstat(dest); err == nil {
		if !info.Mode().IsRegular() {
			return nil, errors.New("import destination is not a regular file")
		}
		f, err := os.Open(dest)
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil || hex.EncodeToString(h.Sum(nil)) != sha {
			return nil, errors.New("import destination content changed")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := persistImportedFile(staged, dest); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}
	group := strings.ToLower(strings.TrimSuffix(dest, bookExt(dest)))
	bookID, err := s.store.BookByGroup(ctx, lib.ID, group)
	if errors.Is(err, store.ErrNotFound) {
		if title == "" {
			title = formats.BaseName(name)
		}
		bookID, err = s.store.CreateBook(ctx, lib.ID, group, store.BookMeta{Title: title, Kind: "book", MetaSource: "books"})
		if err == nil {
			err = s.store.SetLocked(ctx, bookID, []string{"title"})
		}
	}
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(dest)
	if err != nil {
		return nil, err
	}
	file := &store.File{BookID: bookID, LibraryID: lib.ID, Path: dest, Format: formats.Detect(dest), Size: size, MTime: info.ModTime().Unix(), SHA256: sha, NameMD5: formats.NameMD5(dest)}
	file.PartialMD5, _ = formats.PartialMD5(dest)
	if err := s.store.UpsertFile(ctx, file); err != nil {
		return nil, err
	}
	if err := s.store.RefreshBookFormatState(ctx, bookID); err != nil {
		return nil, err
	}
	if shelf == nil {
		shelf = &store.Shelf{Name: balkanCollection, Kind: "manual", Description: "Ebooks imported from Pocket Books. Each book retains its original BalkanDownload post.", OPDS: true}
		if err := s.store.CreateShelf(ctx, uid, shelf); err != nil {
			return nil, err
		}
	}
	if err := s.store.AddToShelf(ctx, uid, shelf.ID, []int64{bookID}); err != nil {
		return nil, err
	}
	if prior != nil {
		receipt = &store.LinkedBookImport{UserID: uid, SHA256: sha, SourceApp: "books", SourceURL: source, OriginalName: name, IdempotencyKey: key, CreatedAt: store.Now()}
	}
	receipt.BookID, receipt.FileID, receipt.ShelfID = bookID, file.ID, shelf.ID
	if err := s.store.SaveLinkedImport(ctx, receipt); err != nil {
		return nil, err
	}
	return &balkanImportResult{BookID: bookID, FileID: file.ID, BookURL: fmt.Sprintf("/book/%d", bookID), Collection: balkanCollection, CollectionID: shelf.ID, SHA256: sha, Duplicate: prior == nil, SourceURL: receipt.SourceURL}, nil
}

func persistImportedFile(staged, dest string) error {
	in, err := os.Open(staged)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(dest), ".incoming-")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	closed := out.Close()
	if err == nil {
		err = closed
	}
	if err != nil {
		return err
	}
	// Publish complete, synced bytes without ever replacing an existing file.
	if err = os.Link(out.Name(), dest); err != nil {
		return err
	}
	if err = os.Remove(out.Name()); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(dest))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
