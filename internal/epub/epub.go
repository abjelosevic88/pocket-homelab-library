// Package epub rewrites EPUB files on their way out of the library: the
// metadata as edited in the catalogue written into the package document,
// and the fixes Amazon's Send to Kindle needs. Originals are never touched;
// a book is opened, changed in memory and written to a new file, copying
// every entry that did not change as it is.
package epub

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Book is an EPUB opened for changes.
type Book struct {
	zr      *zip.ReadCloser
	files   map[string]*zip.File
	order   []string
	changed map[string][]byte
	added   []string // names of new entries, in order
	OPFPath string
	// Fixed lists what KindleFix changed (for logs).
	Fixed []string
}

// Open reads an EPUB's directory and finds its package document.
func Open(p string) (*Book, error) {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return nil, fmt.Errorf("not an EPUB (zip): %w", err)
	}
	b := &Book{zr: zr, files: map[string]*zip.File{}, changed: map[string][]byte{}}
	for _, f := range zr.File {
		if _, dup := b.files[f.Name]; dup {
			continue
		}
		b.files[f.Name] = f
		b.order = append(b.order, f.Name)
	}
	container, err := b.Read("META-INF/container.xml")
	if err != nil {
		zr.Close()
		return nil, errors.New("not an EPUB: META-INF/container.xml is missing")
	}
	m := regexp.MustCompile(`full-path\s*=\s*["']([^"']+)["']`).FindSubmatch(container)
	if m == nil {
		zr.Close()
		return nil, errors.New("not an EPUB: container.xml names no package document")
	}
	b.OPFPath = string(m[1])
	if _, ok := b.files[b.OPFPath]; !ok {
		zr.Close()
		return nil, fmt.Errorf("not an EPUB: %s is missing", b.OPFPath)
	}
	return b, nil
}

// Close releases the source file.
func (b *Book) Close() error { return b.zr.Close() }

// Names lists the entries (original order, then added ones).
func (b *Book) Names() []string {
	out := append([]string(nil), b.order...)
	for _, a := range b.added {
		if _, ok := b.files[a]; !ok {
			out = append(out, a)
		}
	}
	return out
}

// Read returns an entry's current contents.
func (b *Book) Read(name string) ([]byte, error) {
	if d, ok := b.changed[name]; ok {
		return d, nil
	}
	f, ok := b.files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, 512<<20))
}

// Put replaces (or adds) an entry.
func (b *Book) Put(name string, data []byte) {
	if _, ok := b.files[name]; !ok {
		if _, seen := b.changed[name]; !seen {
			b.added = append(b.added, name)
		}
	}
	b.changed[name] = data
}

// Changed reports whether anything was changed.
func (b *Book) Changed() bool { return len(b.changed) > 0 }

// Write saves the book to dst: "mimetype" first and stored, changed entries
// recompressed, the rest copied as they are.
func (b *Book) Write(dst string) error {
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(out)
	fail := func(err error) error {
		zw.Close()
		out.Close()
		os.Remove(tmp)
		return err
	}
	mt := []byte("application/epub+zip")
	if d, err := b.Read("mimetype"); err == nil && len(bytes.TrimSpace(d)) > 0 {
		mt = bytes.TrimSpace(d)
	}
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		return fail(err)
	}
	if _, err := w.Write(mt); err != nil {
		return fail(err)
	}
	for _, name := range b.Names() {
		if name == "mimetype" {
			continue
		}
		if d, ok := b.changed[name]; ok {
			hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
			if f, ok := b.files[name]; ok {
				hdr.Modified = f.Modified
			}
			w, err := zw.CreateHeader(hdr)
			if err != nil {
				return fail(err)
			}
			if _, err := w.Write(d); err != nil {
				return fail(err)
			}
			continue
		}
		if strings.HasSuffix(name, "/") {
			// a folder entry: some tools give these a body, which zip.Writer refuses
			if _, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store}); err != nil {
				return fail(err)
			}
			continue
		}
		if err := zw.Copy(b.files[name]); err != nil {
			return fail(err)
		}
	}
	if err := zw.Close(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// opfDir is the folder hrefs in the package document are relative to.
func (b *Book) opfDir() string {
	d := path.Dir(b.OPFPath)
	if d == "." {
		return ""
	}
	return d + "/"
}

// ---------- helpers on the package document (text level, keeps formatting) ----------

var (
	metadataRE = regexp.MustCompile(`(?s)<((?:[A-Za-z0-9_-]+:)?metadata)\b([^>]*)>(.*?)</(?:[A-Za-z0-9_-]+:)?metadata\s*>`)
	manifestRE = regexp.MustCompile(`(?s)<((?:[A-Za-z0-9_-]+:)?manifest)\b[^>]*>(.*?)</(?:[A-Za-z0-9_-]+:)?manifest\s*>`)
	itemRE     = regexp.MustCompile(`(?s)<(?:[A-Za-z0-9_-]+:)?item\b[^>]*?/?>`)
	attrRE     = regexp.MustCompile(`([A-Za-z_:][A-Za-z0-9_.:-]*)\s*=\s*("([^"]*)"|'([^']*)')`)
	packageRE  = regexp.MustCompile(`(?s)<(?:[A-Za-z0-9_-]+:)?package\b[^>]*>`)
)

// attrs parses the attributes of one tag.
func attrs(tag string) map[string]string {
	out := map[string]string{}
	for _, m := range attrRE.FindAllStringSubmatch(tag, -1) {
		v := m[3]
		if strings.HasPrefix(m[2], "'") {
			v = m[4]
		}
		out[m[1]] = xmlUnescape(v)
	}
	return out
}

func xmlUnescape(s string) string {
	var v string
	if err := xml.Unmarshal([]byte("<x>"+s+"</x>"), &v); err != nil {
		return s
	}
	return v
}

func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// nsPrefix returns the prefix bound to a namespace URI in the document ("" = none).
func nsPrefix(doc, uri string) (string, bool) {
	re := regexp.MustCompile(`xmlns:([A-Za-z0-9_-]+)\s*=\s*["']` + regexp.QuoteMeta(uri) + `["']`)
	if m := re.FindStringSubmatch(doc); m != nil {
		return m[1], true
	}
	return "", false
}

// manifestItem is an <item> of the manifest.
type manifestItem struct {
	tag   string // the whole tag as written
	attrs map[string]string
}

func manifestItems(opf string) []manifestItem {
	m := manifestRE.FindStringSubmatch(opf)
	if m == nil {
		return nil
	}
	var out []manifestItem
	for _, tag := range itemRE.FindAllString(m[2], -1) {
		out = append(out, manifestItem{tag: tag, attrs: attrs(tag)})
	}
	return out
}

func epubVersion(opf string) string {
	if t := packageRE.FindString(opf); t != "" {
		return attrs(t)["version"]
	}
	return "2.0"
}

// textEntries are the entries KindleFix looks into, sorted.
func (b *Book) entriesWithExt(exts ...string) []string {
	var out []string
	for _, n := range b.Names() {
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(n), "."))
		for _, e := range exts {
			if ext == e {
				out = append(out, n)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}
