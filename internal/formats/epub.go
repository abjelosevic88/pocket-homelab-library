package formats

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxCover = 20 << 20

// zipIndex finds entries by name, also case-insensitively and URL-decoded
// (manifests often get either wrong).
type zipIndex struct {
	exact map[string]*zip.File
	fold  map[string]*zip.File
}

func indexZip(zr *zip.Reader) *zipIndex {
	ix := &zipIndex{exact: map[string]*zip.File{}, fold: map[string]*zip.File{}}
	for _, f := range zr.File {
		ix.exact[f.Name] = f
		ix.fold[strings.ToLower(f.Name)] = f
	}
	return ix
}

func (ix *zipIndex) get(name string) *zip.File {
	name = strings.TrimPrefix(name, "/")
	if f, ok := ix.exact[name]; ok {
		return f
	}
	if u, err := url.PathUnescape(name); err == nil {
		if f, ok := ix.exact[u]; ok {
			return f
		}
		name = u
	}
	return ix.fold[strings.ToLower(name)]
}

func readZipFile(f *zip.File, limit int64) ([]byte, error) {
	if f == nil {
		return nil, errors.New("missing entry")
	}
	rc, err := f.Open()
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

// joinHref resolves a manifest href against the directory of the OPF.
func joinHref(base, href string) string {
	if i := strings.IndexByte(href, '#'); i >= 0 {
		href = href[:i]
	}
	if strings.HasPrefix(href, "/") {
		return strings.TrimPrefix(href, "/")
	}
	return path.Clean(path.Join(path.Dir(base), href))
}

// EPUBPackage returns the path of an EPUB's OPF inside the archive.
func epubPackage(ix *zipIndex) string {
	if b, err := readZipFile(ix.get("META-INF/container.xml"), 1<<20); err == nil {
		if root, err := parseXML(bytes.NewReader(b)); err == nil {
			for _, rf := range root.all("rootfile") {
				if p := rf.attr("full-path"); p != "" && ix.get(p) != nil {
					return p
				}
			}
		}
	}
	for name := range ix.exact {
		if strings.HasSuffix(strings.ToLower(name), ".opf") {
			return name
		}
	}
	return ""
}

func readEPUB(p string, wantCover bool) (*Meta, error) {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	ix := indexZip(&zr.Reader)
	opfPath := epubPackage(ix)
	if opfPath == "" {
		return nil, errors.New("epub: no package document")
	}
	b, err := readZipFile(ix.get(opfPath), 8<<20)
	if err != nil {
		return nil, err
	}
	info, err := parseOPF(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	m := info.meta
	// words in the spine
	for _, href := range info.spine {
		f := ix.get(joinHref(opfPath, href))
		if f == nil {
			continue
		}
		data, err := readZipFile(f, 32<<20)
		if err != nil {
			continue
		}
		m.Words += countWords(data)
	}
	if wantCover {
		m.Cover = epubCover(ix, opfPath, info)
	}
	return m, nil
}

var imgSrc = regexp.MustCompile(`(?i)<(?:img|image)\b[^>]*?(?:\bsrc|xlink:href|\bhref)\s*=\s*["']([^"']+)["']`)

func epubCover(ix *zipIndex, opfPath string, info *opfInfo) []byte {
	try := func(href string) []byte {
		if href == "" {
			return nil
		}
		b, err := readZipFile(ix.get(joinHref(opfPath, href)), maxCover)
		if err != nil || imageMIME(b) == "" {
			return nil
		}
		return b
	}
	if b := try(info.coverHref); b != nil {
		return b
	}
	// a cover page: the first image it shows
	pages := []string{info.coverPage}
	if len(info.spine) > 0 {
		pages = append(pages, info.spine[0])
	}
	for _, pg := range pages {
		if pg == "" {
			continue
		}
		full := joinHref(opfPath, pg)
		data, err := readZipFile(ix.get(full), 4<<20)
		if err != nil {
			continue
		}
		if m := imgSrc.FindSubmatch(data); m != nil {
			src := string(m[1])
			if b, err := readZipFile(ix.get(joinHref(full, src)), maxCover); err == nil && imageMIME(b) != "" {
				return b
			}
		}
	}
	// a file called cover.*
	for name, f := range ix.fold {
		if base := path.Base(name); strings.HasPrefix(base, "cover.") && isImageName(base) {
			if b, err := readZipFile(f, maxCover); err == nil && imageMIME(b) != "" {
				return b
			}
		}
	}
	return try(info.firstImage)
}

// countWords counts the words in the text of an (X)HTML document, skipping
// markup, entities and the contents of <head>, <script> and <style>.
func countWords(b []byte) int {
	n := 0
	inWord := false
	skip := ""
	for i := 0; i < len(b); {
		c := b[i]
		if c == '<' {
			j := bytes.IndexByte(b[i:], '>')
			if j < 0 {
				break
			}
			tag := strings.ToLower(string(b[i+1 : i+j]))
			name := strings.Fields(strings.TrimSuffix(tag, "/") + " ")[0]
			switch {
			case skip != "" && name == "/"+skip:
				skip = ""
			case skip == "" && (name == "head" || name == "script" || name == "style"):
				skip = name
			}
			inWord = false
			i += j + 1
			continue
		}
		if skip != "" {
			i++
			continue
		}
		if c == '&' {
			if j := bytes.IndexByte(b[i:min(len(b), i+12)], ';'); j > 0 {
				i += j + 1
				inWord = false
				continue
			}
		}
		r, size := utf8.DecodeRune(b[i:])
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if !inWord {
				n++
				inWord = true
			}
		} else if r != '\'' && r != '’' && r != '-' {
			inWord = false
		}
		i += size
	}
	return n
}
