package server

// Moving books between libraries: the files move on disk (into the target's
// folder, by its upload template or keeping their place under the library
// folder) with their sidecars (an .opf or cover of the same name), and the
// book keeps its id, so reading state, highlights, sync and links stay.
// Every move is appended to DATA_DIR/moves.jsonl, enough to put it back.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/abjelosevic88/pocket-homelab-library/internal/formats"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

var moveMu sync.Mutex

type moveRecord struct {
	At       string            `json:"at"`
	BookID   int64             `json:"book_id"`
	From     int64             `json:"from_library"`
	To       int64             `json:"to_library"`
	Paths    map[string]string `json:"paths"` // old → new, sidecars included
	GroupKey string            `json:"old_group_key"`
}

// POST /books/move {"ids": [...], "library_id": n, "rename": bool} — admins.
// rename files each book by the target's upload template (from its details);
// otherwise it keeps its place under the library folder.
func (s *Server) handleMoveBooks(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in struct {
		IDs       []int64 `json:"ids"`
		LibraryID int64   `json:"library_id"`
		Rename    bool    `json:"rename"`
	}
	if err := decode(r, &in); err != nil || len(in.IDs) == 0 || len(in.IDs) > 5000 || in.LibraryID == 0 {
		writeErr(w, http.StatusBadRequest, "ids (1–5000 books) and library_id are required")
		return
	}
	ctx := r.Context()
	to, err := s.store.GetLibrary(ctx, in.LibraryID)
	if err != nil || len(to.Paths) == 0 {
		writeErr(w, http.StatusBadRequest, "no such library, or it has no folder")
		return
	}
	if s.scanning(0) {
		writeErr(w, http.StatusConflict, "a scan is running; move books when it is done")
		return
	}
	moveMu.Lock()
	defer moveMu.Unlock()
	libs := map[int64]*store.Library{}
	moved, skipped := 0, 0
	var errs []string
	for _, id := range in.IDs {
		if err := s.moveBook(r, id, to, in.Rename, libs); err != nil {
			if errors.Is(err, errSameLibrary) {
				skipped++
				continue
			}
			errs = append(errs, fmt.Sprintf("#%d: %v", id, err))
			continue
		}
		moved++
	}
	if len(errs) > 20 {
		errs = append(errs[:20], fmt.Sprintf("… and %d more", len(errs)-20))
	}
	writeJSON(w, http.StatusOK, map[string]any{"moved": moved, "skipped": skipped, "errors": errs})
}

var errSameLibrary = errors.New("already there")

func (s *Server) moveBook(r *http.Request, id int64, to *store.Library, rename bool, libs map[int64]*store.Library) error {
	ctx := r.Context()
	b, err := s.store.GetBookAny(ctx, id)
	if err != nil {
		return err
	}
	if b.LibraryID == to.ID {
		return errSameLibrary
	}
	from := libs[b.LibraryID]
	if from == nil {
		if from, err = s.store.GetLibrary(ctx, b.LibraryID); err != nil {
			return err
		}
		libs[b.LibraryID] = from
	}
	files := []store.File{}
	for _, f := range b.Files {
		if !f.Missing {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return errors.New("no files on disk")
	}
	// where each file goes: the template's name, or the same place under the target folder
	dest := func(f store.File) string {
		if rename {
			m := &formats.Meta{Title: b.Title, Series: b.Series, SeriesIndex: b.SeriesIndex, Published: b.Published, Language: b.Language}
			for _, a := range b.Authors {
				m.Authors = append(m.Authors, a.Name)
			}
			return filepath.Join(to.Paths[0], templatePath(to.UploadTemplate, m, f.Path)+bookExt(f.Path))
		}
		for _, root := range from.Paths {
			if rel, err := filepath.Rel(root, f.Path); err == nil && !strings.HasPrefix(rel, "..") {
				return filepath.Join(to.Paths[0], rel)
			}
		}
		return filepath.Join(to.Paths[0], filepath.Base(f.Path))
	}
	rec := moveRecord{At: store.Now(), BookID: b.ID, From: b.LibraryID, To: to.ID, Paths: map[string]string{}, GroupKey: b.GroupKey}
	newPaths := map[int64]string{}
	undo := func() {
		for old, nw := range rec.Paths {
			_ = os.Rename(nw, old)
		}
	}
	for _, f := range files {
		d, err := freeName(dest(f))
		if err != nil {
			undo()
			return err
		}
		if err := moveFile(f.Path, d); err != nil {
			undo()
			return err
		}
		rec.Paths[f.Path], newPaths[f.ID] = d, d
		// sidecars: "<name>.opf", "<name>.jpg" … next to it
		base := strings.TrimSuffix(f.Path, bookExt(f.Path))
		for _, ext := range []string{".opf", ".jpg", ".jpeg", ".png"} {
			if _, err := os.Stat(base + ext); err == nil {
				sd := strings.TrimSuffix(d, bookExt(d)) + ext
				if _, err := os.Stat(sd); errors.Is(err, os.ErrNotExist) && moveFile(base+ext, sd) == nil {
					rec.Paths[base+ext] = sd
				}
			}
		}
	}
	first := newPaths[files[0].ID]
	key := strings.ToLower(filepath.Join(filepath.Dir(first), formats.BaseName(first)))
	if err := s.store.RelocateBook(ctx, b.ID, to.ID, key, newPaths); err != nil {
		undo()
		return err
	}
	s.logMove(rec)
	for _, f := range files {
		_ = removeEmptyDirs(filepath.Dir(f.Path), libs[b.LibraryID].Paths)
	}
	return nil
}

// freeName is p, or "name (2).ext" … when p is taken.
func freeName(p string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	ext := bookExt(p)
	stem := strings.TrimSuffix(p, ext)
	for i := 1; i < 100; i++ {
		cand := p
		if i > 1 {
			cand = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		if _, err := os.Lstat(cand); errors.Is(err, os.ErrNotExist) {
			return cand, nil
		}
	}
	return "", errors.New("too many files with that name")
}

func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyFile(src, dst); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return os.Remove(src)
}

// removeEmptyDirs removes dir and its empty parents, up to (not including) a library folder.
func removeEmptyDirs(dir string, roots []string) error {
	for {
		for _, r := range roots {
			if filepath.Clean(dir) == filepath.Clean(r) || !strings.HasPrefix(dir, filepath.Clean(r)+string(filepath.Separator)) {
				return nil
			}
		}
		if err := os.Remove(dir); err != nil {
			return err // not empty
		}
		dir = filepath.Dir(dir)
	}
}

func (s *Server) logMove(rec moveRecord) {
	f, err := os.OpenFile(filepath.Join(s.cfg.DataDir, "moves.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		s.log.Warn("moves log", "err", err)
		return
	}
	defer f.Close()
	b, _ := json.Marshal(rec)
	_, _ = f.Write(append(b, '\n'))
}
