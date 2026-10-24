// Package scanner walks a library's folders and keeps the catalogue in step
// with them: new files become books (format twins in one folder with one
// name become one book), changed files are read again, vanished files are
// marked missing. Fields edited by hand (a book's Locked list) are never
// overwritten.
package scanner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/abjelosevic88/pocket-homelab-library/internal/covers"
	"github.com/abjelosevic88/pocket-homelab-library/internal/formats"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

// Scanner reads files into the catalogue.
type Scanner struct {
	Store     *store.Store
	Log       *slog.Logger
	CoversDir string
	Workers   int // metadata readers in parallel (default up to 6)
}

// Progress is reported while a scan runs.
type Progress struct {
	Phase   string // listing | reading | cleaning
	Done    int
	Total   int
	Added   int
	Updated int
	Removed int
}

// Result is what a scan did.
type Result struct {
	Files   int      // book files seen
	Books   int      // groups seen
	Added   []int64  // new books
	Updated int      // books read again
	Removed int      // files gone
	Errors  []string // files that could not be read (first 50)
}

// ErrRootMissing is returned when a library folder cannot be listed: then
// nothing is marked missing (a NAS that is down must not empty the library).
var ErrRootMissing = errors.New("library folder not reachable")

type diskFile struct {
	path   string
	dir    string
	format string
	size   int64
	mtime  int64
}

// group is the files of one book on disk.
type group struct {
	key     string
	files   []diskFile
	sidecar string // OPF with the book's metadata
	cover   string // a cover.jpg beside a Calibre metadata.opf
	parent  string // the folder name, for file-name fallbacks
}

func skipName(name string) bool {
	low := strings.ToLower(name)
	return strings.HasPrefix(name, ".") || name == "@eaDir" || low == ".caltrash" || low == "#recycle" || low == "$recycle.bin" ||
		strings.HasSuffix(low, ".part") || strings.HasSuffix(low, ".crdownload") || strings.HasSuffix(low, ".tmp") || low == "lost+found"
}

// listing is everything found under some folders.
type listing struct {
	files      []diskFile
	sidecars   map[string]string // dir + "\x00" + lower base → .opf
	calibreOPF map[string]string // dir → metadata.opf
	coverJPG   map[string]string // dir → cover.jpg
	dirs       map[string]bool   // folders listed
}

func newListing() *listing {
	return &listing{sidecars: map[string]string{}, calibreOPF: map[string]string{}, coverJPG: map[string]string{}, dirs: map[string]bool{}}
}

// listDir adds one folder's entries (and, when deep, its subfolders').
func (l *listing) listDir(ctx context.Context, dir string, deep bool) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	l.dirs[dir] = true
	for _, e := range entries {
		name := e.Name()
		if skipName(name) {
			continue
		}
		full := filepath.Join(dir, name)
		if e.IsDir() {
			if deep {
				if err := l.listDir(ctx, full, true); err != nil && ctx.Err() != nil {
					return err
				}
			}
			continue
		}
		low := strings.ToLower(name)
		switch {
		case low == "metadata.opf":
			l.calibreOPF[dir] = full
			continue
		case strings.HasSuffix(low, ".opf"):
			l.sidecars[dir+"\x00"+strings.TrimSuffix(low, ".opf")] = full
			continue
		case low == "cover.jpg" || low == "cover.jpeg" || low == "cover.png":
			l.coverJPG[dir] = full
			continue
		}
		f := formats.Detect(name)
		if f == "" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			continue
		}
		l.files = append(l.files, diskFile{path: full, dir: dir, format: f, size: info.Size(), mtime: info.ModTime().Unix()})
	}
	return nil
}

// groups binds the files into books for a library of this kind.
func (l *listing) groups(kind string) []*group {
	byKey := map[string]*group{}
	var order []string
	for _, f := range l.files {
		var key string
		switch {
		case kind != "books" || formats.IsComic(f.format):
			key = strings.ToLower(f.path)
		case l.calibreOPF[f.dir] != "":
			key = strings.ToLower(f.dir) + "/"
		default:
			key = strings.ToLower(filepath.Join(f.dir, formats.BaseName(f.path)))
		}
		g := byKey[key]
		if g == nil {
			g = &group{key: key, parent: filepath.Base(f.dir)}
			byKey[key] = g
			order = append(order, key)
			if opf := l.calibreOPF[f.dir]; opf != "" && kind == "books" && !formats.IsComic(f.format) {
				g.sidecar, g.cover = opf, l.coverJPG[f.dir]
			} else if opf := l.sidecars[f.dir+"\x00"+strings.ToLower(formats.BaseName(f.path))]; opf != "" {
				g.sidecar = opf
			}
		}
		g.files = append(g.files, f)
	}
	out := make([]*group, 0, len(order))
	for _, k := range order {
		g := byKey[k]
		sort.SliceStable(g.files, func(i, j int) bool { return store.FormatRank(g.files[i].format) < store.FormatRank(g.files[j].format) })
		out = append(out, g)
	}
	return out
}

// Scan walks every folder of a library. full reads every file's metadata
// again (otherwise only new and changed files are read).
func (s *Scanner) Scan(ctx context.Context, lib *store.Library, full bool, progress func(Progress)) (*Result, error) {
	l := newListing()
	if progress == nil {
		progress = func(Progress) {}
	}
	progress(Progress{Phase: "listing"})
	for _, root := range lib.Paths {
		fi, err := os.Stat(root)
		if err != nil || !fi.IsDir() {
			return nil, fmt.Errorf("%w: %s", ErrRootMissing, root)
		}
		if err := l.listDir(ctx, root, true); err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrRootMissing, root, err)
		}
	}
	return s.apply(ctx, lib, l, full, nil, progress)
}

// ScanDirs reads only some folders of a library (not their subfolders):
// uploads, the ingest folder, a book's rescan. Files under other folders
// are left alone.
func (s *Scanner) ScanDirs(ctx context.Context, lib *store.Library, dirs []string, full bool) (*Result, error) {
	l := newListing()
	seen := map[string]bool{}
	for _, d := range dirs {
		d = filepath.Clean(d)
		if seen[d] {
			continue
		}
		seen[d] = true
		if err := l.listDir(ctx, d, false); err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrRootMissing, d, err)
		}
	}
	return s.apply(ctx, lib, l, full, seen, func(Progress) {})
}

// readResult is one group's files read in a worker.
type readResult struct {
	g      *group
	meta   *store.BookMeta
	cover  []byte // raw, then the prepared JPEG
	color  string
	hashes map[string][2]string // path → partial md5, name md5
	pages  map[string]int
	errs   []string
}

func (s *Scanner) apply(ctx context.Context, lib *store.Library, l *listing, full bool, onlyDirs map[string]bool, progress func(Progress)) (*Result, error) {
	existing, err := s.Store.FilesInLibrary(ctx, lib.ID)
	if err != nil {
		return nil, err
	}
	groups := l.groups(lib.Kind)
	res := &Result{Files: len(l.files), Books: len(groups)}
	p := Progress{Phase: "reading", Total: len(groups)}
	progress(p)

	// which groups need reading
	var todo []*group
	seenPaths := map[string]bool{}
	for _, g := range groups {
		changed := full
		for _, f := range g.files {
			seenPaths[f.path] = true
			old, ok := existing[f.path]
			if !ok || old.Size != f.size || old.MTime != f.mtime || old.Missing {
				changed = true
			}
		}
		if !changed {
			if _, err := s.Store.BookByGroup(ctx, lib.ID, g.key); err != nil {
				changed = true // files known but the book is not (renamed key)
			}
		}
		if changed {
			todo = append(todo, g)
			continue
		}
		for _, f := range g.files {
			_ = s.Store.TouchFile(ctx, existing[f.path].ID)
		}
		p.Done++
	}
	progress(p)

	// missing files, gathered first: a moved book keeps its id (and everyone's progress)
	gone := map[string]store.File{} // partial md5 → file
	var goneList []store.File
	for path, f := range existing {
		if seenPaths[path] || f.Missing {
			continue
		}
		if onlyDirs != nil && !onlyDirs[filepath.Dir(path)] {
			continue
		}
		goneList = append(goneList, f)
		if f.PartialMD5 != "" {
			gone[f.PartialMD5] = f
		}
	}

	// read in parallel, write here
	workers := s.Workers
	if workers <= 0 {
		workers = min(6, max(2, runtime.NumCPU()))
	}
	jobs := make(chan *group)
	results := make(chan readResult)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for g := range jobs {
				results <- s.read(lib, g)
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, g := range todo {
			select {
			case jobs <- g:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()

	moved := map[int64]bool{}
	for r := range results {
		p.Done++
		for _, e := range r.errs {
			if len(res.Errors) < 50 {
				res.Errors = append(res.Errors, e)
			}
		}
		id, created, err := s.write(ctx, lib, r, existing, gone, moved)
		if err != nil {
			if len(res.Errors) < 50 {
				res.Errors = append(res.Errors, r.g.files[0].path+": "+err.Error())
			}
			s.Log.Warn("scan: save", "path", r.g.files[0].path, "err", err)
		} else if created {
			res.Added = append(res.Added, id)
			p.Added++
		} else {
			res.Updated++
			p.Updated++
		}
		if p.Done%25 == 0 {
			progress(p)
		}
	}
	if ctx.Err() != nil {
		return res, ctx.Err()
	}

	// files that are gone
	p.Phase = "cleaning"
	progress(p)
	touched := map[int64]bool{}
	for _, f := range goneList {
		if moved[f.ID] {
			continue
		}
		if err := s.Store.SetFileMissing(ctx, f.ID, true); err == nil {
			res.Removed++
			touched[f.BookID] = true
		}
	}
	for b := range touched {
		_ = s.Store.RefreshBookFormatState(ctx, b)
	}
	p.Removed = res.Removed
	progress(p)
	if len(res.Added) > 0 || res.Removed > 0 || res.Updated > 0 {
		_ = s.Store.PruneOrphans(ctx)
	}
	return res, nil
}

// read reads one group's files (in a worker; no database writes).
func (s *Scanner) read(lib *store.Library, g *group) readResult {
	r := readResult{g: g, hashes: map[string][2]string{}, pages: map[string]int{}}
	var embedded []*formats.Meta
	for _, f := range g.files {
		pm, err := formats.PartialMD5(f.path)
		if err != nil {
			r.errs = append(r.errs, f.path+": "+err.Error())
		}
		r.hashes[f.path] = [2]string{pm, formats.NameMD5(f.path)}
		m, err := formats.Read(f.path, r.cover == nil)
		if err != nil {
			r.errs = append(r.errs, f.path+": "+err.Error())
			continue
		}
		if formats.IsComic(f.format) || f.format == "pdf" {
			r.pages[f.path] = m.Pages
		}
		if r.cover == nil && len(m.Cover) > 0 {
			r.cover = m.Cover
		}
		embedded = append(embedded, m)
	}
	var side *formats.Meta
	if g.sidecar != "" {
		if f, err := os.Open(g.sidecar); err == nil {
			side, err = formats.ReadOPF(f)
			f.Close()
			if err != nil {
				r.errs = append(r.errs, g.sidecar+": "+err.Error())
			}
		}
	}
	if g.cover != "" {
		if b, err := os.ReadFile(g.cover); err == nil {
			r.cover = b
		}
	}
	m, source := merge(side, embedded)
	r.meta = s.finish(lib, g, m, source)
	if len(r.cover) > 0 {
		jpg, color, err := covers.Prepare(r.cover)
		if err != nil {
			r.cover = nil
		} else {
			r.cover, r.color = jpg, color
		}
	}
	return r
}

// merge takes the sidecar first, then fills what it lacks from the files
// (best format first).
func merge(side *formats.Meta, embedded []*formats.Meta) (*formats.Meta, string) {
	out := &formats.Meta{Identifiers: map[string]string{}}
	source := "file"
	all := embedded
	if side != nil {
		all = append([]*formats.Meta{side}, embedded...)
		source = "sidecar"
	}
	for _, m := range all {
		if out.Title == "" {
			out.Title, out.SortTitle, out.Subtitle = m.Title, m.SortTitle, m.Subtitle
		}
		if len(out.Authors) == 0 {
			out.Authors = m.Authors
		}
		if out.Series == "" && m.Series != "" {
			out.Series, out.SeriesIndex = m.Series, m.SeriesIndex
		}
		if out.Description == "" {
			out.Description = m.Description
		}
		if out.Language == "" {
			out.Language = m.Language
		}
		if out.Publisher == "" {
			out.Publisher = m.Publisher
		}
		if out.Published == "" {
			out.Published = m.Published
		}
		if out.ISBN == "" {
			out.ISBN = m.ISBN
		}
		for k, v := range m.Identifiers {
			if _, ok := out.Identifiers[k]; !ok {
				out.Identifiers[k] = v
			}
		}
		if len(out.Tags) == 0 {
			out.Tags = m.Tags
		}
		if out.Pages == 0 {
			out.Pages = m.Pages
		}
		if out.Words == 0 {
			out.Words = m.Words
		}
		out.Manga, out.RTL = out.Manga || m.Manga, out.RTL || m.RTL
		if out.AgeRating == "" {
			out.AgeRating = m.AgeRating
		}
	}
	return out, source
}

var (
	chapterRE  = regexp.MustCompile(`(?i)\b(?:chapter|chap|ch\.?|issue|#)\s*(\d+(?:\.\d+)?)`)
	volumeRE   = regexp.MustCompile(`(?i)\b(?:volume|vol\.?|v)\s*(\d+(?:\.\d+)?)`)
	bracketsRE = regexp.MustCompile(`\s*[\(\[\{][^\)\]\}]*[\)\]\}]`)
	numberRE   = regexp.MustCompile(`(\d+(?:\.\d+)?)`)
)

// finish fills the gaps from the file name and the library's kind.
func (s *Scanner) finish(lib *store.Library, g *group, m *formats.Meta, source string) *store.BookMeta {
	first := g.files[0]
	base := formats.BaseName(first.path)
	comic := lib.Kind != "books" || formats.IsComic(first.format)
	if comic {
		name := strings.ReplaceAll(base, "_", " ")
		idx := 0.0
		if mm := chapterRE.FindStringSubmatch(name); mm != nil {
			idx, _ = strconv.ParseFloat(mm[1], 64)
			if m.Title == "" {
				m.Title = "Chapter " + mm[1]
			}
		} else if mm := volumeRE.FindStringSubmatch(name); mm != nil {
			idx, _ = strconv.ParseFloat(mm[1], 64)
		} else {
			stripped := bracketsRE.ReplaceAllString(name, "")
			if all := numberRE.FindAllString(stripped, -1); len(all) > 0 {
				idx, _ = strconv.ParseFloat(all[len(all)-1], 64)
			}
		}
		if m.Series == "" {
			if root := isRoot(lib, first.dir); !root {
				m.Series = g.parent
			}
			if m.SeriesIndex == 0 {
				m.SeriesIndex = idx
			}
		} else if m.SeriesIndex == 0 {
			m.SeriesIndex = idx
		}
		if m.Title == "" {
			m.Title = strings.TrimSpace(bracketsRE.ReplaceAllString(name, ""))
		}
	}
	if m.Title == "" || len(m.Authors) == 0 {
		title, author := fromFileName(base, g.parent)
		if m.Title == "" {
			m.Title = title
		}
		if len(m.Authors) == 0 && author != "" && !comic {
			m.Authors = []string{author}
		}
	}
	kind := "book"
	switch {
	case lib.Kind == "manga" || (comic && m.Manga):
		kind = "manga"
	case comic:
		kind = "comic"
	}
	dir := "ltr"
	if m.RTL || lib.Kind == "manga" {
		dir = "rtl"
	}
	return &store.BookMeta{Title: m.Title, SortTitle: m.SortTitle, Subtitle: m.Subtitle, Authors: m.Authors, Description: m.Description,
		Language: m.Language, Publisher: m.Publisher, Published: m.Published, ISBN: m.ISBN, Identifiers: m.Identifiers,
		Series: m.Series, SeriesIndex: m.SeriesIndex, Tags: m.Tags, Pages: m.Pages, Words: m.Words, Kind: kind,
		ReadingDirection: dir, AgeRating: m.AgeRating, MetaSource: source}
}

func isRoot(lib *store.Library, dir string) bool {
	for _, p := range lib.Paths {
		if filepath.Clean(p) == filepath.Clean(dir) {
			return true
		}
	}
	return false
}

// fromFileName reads "<Author> - <Title>" only when the file sits in a
// folder named after that author (the archive's biblioteka/<Author>/ layout);
// anything else is just a title. The other way round ("Title - Author",
// "Series - Title") is too common to guess from.
func fromFileName(base, folder string) (title, author string) {
	base = strings.TrimSpace(base)
	if folder != "" && len(base) > len(folder)+3 && strings.EqualFold(base[:len(folder)], folder) && strings.HasPrefix(base[len(folder):], " - ") {
		return strings.TrimSpace(base[len(folder)+3:]), folder
	}
	return base, ""
}

// write saves one group: a new book, or the existing one updated without
// touching its locked fields.
func (s *Scanner) write(ctx context.Context, lib *store.Library, r readResult, existing map[string]store.File, gone map[string]store.File, moved map[int64]bool) (int64, bool, error) {
	g := r.g
	var bookID int64
	for _, f := range g.files {
		if old, ok := existing[f.path]; ok && old.BookID > 0 {
			bookID = old.BookID
			break
		}
	}
	if bookID == 0 {
		if id, err := s.Store.BookByGroup(ctx, lib.ID, g.key); err == nil {
			bookID = id
		}
	}
	if bookID == 0 {
		// a file that moved: same contents as one that vanished
		for _, f := range g.files {
			if h := r.hashes[f.path][0]; h != "" {
				if old, ok := gone[h]; ok {
					bookID = old.BookID
					moved[old.ID] = true
					_ = s.Store.DeleteFile(ctx, old.ID)
					break
				}
			}
		}
		if bookID > 0 {
			if err := s.Store.MoveBook(ctx, bookID, lib.ID, g.key); err != nil {
				bookID = 0
			}
		}
	}
	created := false
	if bookID == 0 {
		id, err := s.Store.CreateBook(ctx, lib.ID, g.key, *r.meta)
		if err != nil {
			return 0, false, err
		}
		bookID, created = id, true
	} else {
		cur, err := s.Store.GetBookAny(ctx, bookID)
		if err != nil {
			return 0, false, err
		}
		m := keepLocked(store.MetaOf(cur), *r.meta, cur.Locked)
		if err := s.Store.SaveBookMeta(ctx, bookID, m); err != nil {
			return 0, false, err
		}
	}
	for _, f := range g.files {
		h := r.hashes[f.path]
		file := &store.File{BookID: bookID, LibraryID: lib.ID, Path: f.path, Format: f.format, Size: f.size, MTime: f.mtime,
			PartialMD5: h[0], NameMD5: h[1], Pages: r.pages[f.path]}
		if err := s.Store.UpsertFile(ctx, file); err != nil {
			return bookID, created, err
		}
	}
	if err := s.Store.RefreshBookFormatState(ctx, bookID); err != nil {
		return bookID, created, err
	}
	if len(r.cover) > 0 {
		cur, err := s.Store.GetBookAny(ctx, bookID)
		if err == nil && !contains(cur.Locked, "cover") {
			name, err := covers.Store(s.CoversDir, fmt.Sprintf("book-%d", bookID), r.cover)
			if err == nil {
				_ = s.Store.SetBookCover(ctx, bookID, name, r.color)
			} else {
				s.Log.Debug("scan: cover", "book", bookID, "err", err)
			}
		}
	}
	return bookID, created, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// keepLocked returns fresh metadata with the locked fields taken from cur.
func keepLocked(cur, fresh store.BookMeta, locked []string) store.BookMeta {
	out := fresh
	for _, f := range locked {
		switch f {
		case "title":
			out.Title = cur.Title
		case "sort_title":
			out.SortTitle = cur.SortTitle
		case "subtitle":
			out.Subtitle = cur.Subtitle
		case "authors":
			out.Authors = cur.Authors
		case "description":
			out.Description = cur.Description
		case "language":
			out.Language = cur.Language
		case "publisher":
			out.Publisher = cur.Publisher
		case "published":
			out.Published = cur.Published
		case "isbn":
			out.ISBN = cur.ISBN
		case "identifiers":
			out.Identifiers = cur.Identifiers
		case "series":
			out.Series, out.SeriesIndex = cur.Series, cur.SeriesIndex
		case "series_index":
			out.SeriesIndex = cur.SeriesIndex
		case "tags":
			out.Tags = cur.Tags
		case "pages":
			out.Pages = cur.Pages
		case "kind":
			out.Kind = cur.Kind
		case "reading_direction":
			out.ReadingDirection = cur.ReadingDirection
		case "age_rating":
			out.AgeRating = cur.AgeRating
		}
	}
	if !contains(locked, "sort_title") && !contains(locked, "title") {
		out.SortTitle = fresh.SortTitle
	}
	if len(locked) > 0 && cur.MetaSource != "" && cur.MetaSource != "file" && cur.MetaSource != "sidecar" {
		out.MetaSource = cur.MetaSource
	}
	return out
}

// Hash computes a file's whole-file SHA-256 (duplicates), slowly in the background.
func Hash(path string) (string, error) { return sha256File(path) }
