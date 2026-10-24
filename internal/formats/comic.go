package formats

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/bodgit/sevenzip"
	"github.com/nwaples/rardecode/v2"
)

// Page is one image of a comic.
type Page struct {
	Name string
	Size int64
}

// Comic is an open comic archive.
type Comic struct {
	path   string
	format string
	pages  []Page
	zr     *zip.ReadCloser
	zfiles map[string]*zip.File
	dir    string // extracted pages (RAR, 7z, tar)
}

// archiveEntry is one file of an archive, opened on demand.
type archiveEntry struct {
	name string
	size int64
	open func() (io.ReadCloser, error)
}

// listArchive lists a comic archive's files without extracting them; close
// releases the archive.
func listArchive(p, format string) (entries []archiveEntry, close func() error, err error) {
	switch format {
	case "cbz":
		zr, err := zip.OpenReader(p)
		if err != nil {
			return nil, nil, err
		}
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			f := f
			entries = append(entries, archiveEntry{name: f.Name, size: int64(f.UncompressedSize64), open: f.Open})
		}
		return entries, zr.Close, nil
	case "cbr":
		files, err := rardecode.List(p)
		if err != nil {
			return nil, nil, err
		}
		for _, f := range files {
			if f.IsDir {
				continue
			}
			f := f
			entries = append(entries, archiveEntry{name: f.Name, size: f.UnPackedSize, open: f.Open})
		}
		return entries, func() error { return nil }, nil
	case "cb7":
		zr, err := sevenzip.OpenReader(p)
		if err != nil {
			return nil, nil, err
		}
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			f := f
			entries = append(entries, archiveEntry{name: f.Name, size: int64(f.UncompressedSize), open: f.Open})
		}
		return entries, zr.Close, nil
	case "cbt":
		f, err := os.Open(p)
		if err != nil {
			return nil, nil, err
		}
		tr := tar.NewReader(f)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				f.Close()
				return nil, nil, err
			}
			if h.Typeflag != tar.TypeReg {
				continue
			}
			name, size, off := h.Name, h.Size, int64(0)
			if pos, err := f.Seek(0, io.SeekCurrent); err == nil {
				off = pos
			}
			entries = append(entries, archiveEntry{name: name, size: size, open: func() (io.ReadCloser, error) {
				return io.NopCloser(io.NewSectionReader(f, off, size)), nil
			}})
		}
		return entries, f.Close, nil
	}
	return nil, nil, ErrUnsupported
}

func skipEntry(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") || part == "__MACOSX" || strings.EqualFold(part, "thumbs.db") {
			return true
		}
	}
	return false
}

// naturalLess orders names as people number pages: "2" before "10".
func naturalLess(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	for a != "" && b != "" {
		ra, rb := rune(a[0]), rune(b[0])
		if unicode.IsDigit(ra) && unicode.IsDigit(rb) {
			na, ia := leadingNumber(a)
			nb, ib := leadingNumber(b)
			if na != nb {
				return na < nb
			}
			if ia != ib {
				return ia < ib
			}
			a, b = a[ia:], b[ib:]
			continue
		}
		if ra != rb {
			return ra < rb
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func leadingNumber(s string) (uint64, int) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	n, _ := strconv.ParseUint(strings.TrimLeft(s[:i], "0")+"0", 10, 64)
	return n / 10, i
}

// pageEntries keeps the images in reading order.
func pageEntries(entries []archiveEntry) []archiveEntry {
	var out []archiveEntry
	for _, e := range entries {
		if !skipEntry(e.name) && isImageName(e.name) {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return naturalLess(out[i].name, out[j].name) })
	return out
}

func readEntry(e archiveEntry, limit int64) ([]byte, error) {
	rc, err := e.open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("entry too large")
	}
	return b, nil
}

func readComic(p string, wantCover bool) (*Meta, error) {
	entries, closeFn, err := listArchive(p, Detect(p))
	if err != nil {
		return nil, err
	}
	defer closeFn()
	pages := pageEntries(entries)
	m := &Meta{Identifiers: map[string]string{}, Pages: len(pages)}
	coverIdx := 0
	for _, e := range entries {
		if strings.EqualFold(path.Base(e.name), "ComicInfo.xml") {
			if b, err := readEntry(e, 4<<20); err == nil {
				if fc := applyComicInfo(m, b); fc >= 0 && fc < len(pages) {
					coverIdx = fc
				}
			}
			break
		}
	}
	if wantCover && len(pages) > 0 {
		if b, err := readEntry(pages[coverIdx], maxCover); err == nil && imageMIME(b) != "" {
			m.Cover = b
		}
	}
	return m, nil
}

// applyComicInfo reads ComicInfo.xml (the Anansi project's schema) and
// returns the index of the front cover page (-1 = not given).
func applyComicInfo(m *Meta, b []byte) int {
	root, err := parseXML(bytes.NewReader(b))
	if err != nil {
		return -1
	}
	ci := root.first("comicinfo")
	if ci == nil {
		return -1
	}
	get := func(name string) string {
		for _, c := range ci.Children {
			if c.Name == name {
				return strings.TrimSpace(c.text())
			}
		}
		return ""
	}
	m.Title = get("title")
	m.Series = get("series")
	if n, err := strconv.ParseFloat(strings.TrimSpace(get("number")), 64); err == nil {
		m.SeriesIndex = n
	}
	if v := get("volume"); v != "" && m.Series != "" && m.SeriesIndex == 0 {
		m.SeriesIndex, _ = strconv.ParseFloat(v, 64)
	}
	for _, role := range []string{"writer", "penciller", "artist"} {
		for _, a := range strings.Split(get(role), ",") {
			m.Authors = append(m.Authors, a)
		}
		if len(m.Authors) > 0 && strings.TrimSpace(m.Authors[0]) != "" {
			break
		}
	}
	m.Description = get("summary")
	m.Publisher = get("publisher")
	if y := get("year"); y != "" {
		m.Published = y
		if mo, err := strconv.Atoi(get("month")); err == nil && mo >= 1 && mo <= 12 {
			d, _ := strconv.Atoi(get("day"))
			if d < 1 || d > 31 {
				d = 1
			}
			m.Published = fmt.Sprintf("%s-%02d-%02d", y, mo, d)
		}
	}
	for _, k := range []string{"genre", "tags"} {
		for _, t := range strings.Split(get(k), ",") {
			m.Tags = append(m.Tags, t)
		}
	}
	m.Language = get("languageiso")
	switch strings.ToLower(get("manga")) {
	case "yesandrighttoleft":
		m.Manga, m.RTL = true, true
	case "yes":
		m.Manga = true
	}
	if a := get("agerating"); a != "" && !strings.EqualFold(a, "unknown") {
		m.AgeRating = a
	}
	if n, _ := strconv.Atoi(get("pagecount")); n > 0 && m.Pages == 0 {
		m.Pages = n
	}
	if gtin := get("gtin"); gtin != "" {
		m.ISBN = gtin
	}
	for _, pg := range ci.all("page") {
		if strings.EqualFold(pg.attr("type"), "frontcover") {
			if i, err := strconv.Atoi(pg.attr("image")); err == nil {
				return i
			}
		}
	}
	return -1
}

// extract locks make concurrent requests for an unextracted archive wait for one extraction.
var extractLocks sync.Map // dir → *sync.Mutex

// OpenComic opens a CBZ/CBR/CB7/CBT; cacheDir may hold extracted pages of
// archives that cannot be read at random (RAR).
func OpenComic(p, cacheDir string) (*Comic, error) {
	format := Detect(p)
	if !IsComic(format) {
		return nil, ErrUnsupported
	}
	c := &Comic{path: p, format: format}
	if format == "cbz" {
		zr, err := zip.OpenReader(p)
		if err != nil {
			return nil, err
		}
		c.zr, c.zfiles = zr, map[string]*zip.File{}
		var entries []archiveEntry
		for _, f := range zr.File {
			if f.FileInfo().IsDir() {
				continue
			}
			c.zfiles[f.Name] = f
			entries = append(entries, archiveEntry{name: f.Name, size: int64(f.UncompressedSize64)})
		}
		for _, e := range pageEntries(entries) {
			c.pages = append(c.pages, Page{Name: e.name, Size: e.size})
		}
		return c, nil
	}
	// the others: extracted once into the cache, then read from there
	fi, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if cacheDir == "" {
		cacheDir = os.TempDir()
	}
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d", p, fi.ModTime().Unix(), fi.Size())))
	c.dir = filepath.Join(cacheDir, "pages", hex.EncodeToString(sum[:10]))
	mu, _ := extractLocks.LoadOrStore(c.dir, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	index := filepath.Join(c.dir, "index.txt")
	if b, err := os.ReadFile(index); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			name, size, _ := strings.Cut(line, "\t")
			n, _ := strconv.ParseInt(size, 10, 64)
			if name != "" {
				c.pages = append(c.pages, Page{Name: name, Size: n})
			}
		}
		return c, nil
	}
	entries, closeFn, err := listArchive(p, format)
	if err != nil {
		return nil, err
	}
	defer closeFn()
	tmp := c.dir + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return nil, err
	}
	var idx strings.Builder
	for i, e := range pageEntries(entries) {
		b, err := readEntry(e, 100<<20)
		if err != nil {
			_ = os.RemoveAll(tmp)
			return nil, fmt.Errorf("extract %s: %w", e.name, err)
		}
		name := fmt.Sprintf("%05d%s", i, strings.ToLower(path.Ext(e.name)))
		if err := os.WriteFile(filepath.Join(tmp, name), b, 0o644); err != nil {
			_ = os.RemoveAll(tmp)
			return nil, err
		}
		c.pages = append(c.pages, Page{Name: e.name, Size: int64(len(b))})
		fmt.Fprintf(&idx, "%s\t%d\n", e.name, len(b))
	}
	if err := os.WriteFile(filepath.Join(tmp, "index.txt"), []byte(idx.String()), 0o644); err != nil {
		return nil, err
	}
	_ = os.RemoveAll(c.dir)
	if err := os.Rename(tmp, c.dir); err != nil {
		return nil, err
	}
	return c, nil
}

// Pages lists the images in reading order.
func (c *Comic) Pages() []Page { return c.pages }

// Page opens page i (0-based) and returns its media type.
func (c *Comic) Page(i int) (io.ReadCloser, string, error) {
	if i < 0 || i >= len(c.pages) {
		return nil, "", errors.New("no such page")
	}
	var rc io.ReadCloser
	if c.zr != nil {
		f := c.zfiles[c.pages[i].Name]
		if f == nil {
			return nil, "", errors.New("page missing")
		}
		r, err := f.Open()
		if err != nil {
			return nil, "", err
		}
		rc = r
	} else {
		f, err := os.Open(filepath.Join(c.dir, fmt.Sprintf("%05d%s", i, strings.ToLower(path.Ext(c.pages[i].Name)))))
		if err != nil {
			return nil, "", err
		}
		rc = f
	}
	// sniff the type from the first bytes, then hand back a reader over all of it
	head := make([]byte, 16)
	n, _ := io.ReadFull(rc, head)
	head = head[:n]
	mime := imageMIME(head)
	if mime == "" {
		mime = "application/octet-stream"
		switch strings.ToLower(path.Ext(c.pages[i].Name)) {
		case ".avif":
			mime = "image/avif"
		case ".jxl":
			mime = "image/jxl"
		}
	}
	return struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(head), rc), rc}, mime, nil
}

// Close releases the archive.
func (c *Comic) Close() error {
	if c.zr != nil {
		return c.zr.Close()
	}
	return nil
}
