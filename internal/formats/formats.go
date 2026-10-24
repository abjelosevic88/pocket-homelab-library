// Package formats reads e-book and comic files: what they are, their
// metadata and cover, KOReader's document hashes, and comic pages.
package formats

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ErrUnsupported is returned for a file this package cannot read.
var ErrUnsupported = errors.New("unsupported format")

// Meta is what a file (or an OPF sidecar) says about its book.
type Meta struct {
	Title       string
	SortTitle   string
	Subtitle    string
	Authors     []string
	Series      string
	SeriesIndex float64
	Description string
	Language    string // ISO 639-1 where it can be mapped
	Publisher   string
	Published   string // YYYY or YYYY-MM-DD
	ISBN        string // ISBN-13 where possible
	Identifiers map[string]string
	Tags        []string
	Pages       int // comic images, PDF pages, or an estimate for reflowable books
	Words       int
	Manga       bool
	RTL         bool
	AgeRating   string
	Cover       []byte // raw image bytes ('' = none)
}

// Detect returns the format of a path by its extension ("" = not a book).
func Detect(path string) string {
	name := strings.ToLower(filepath.Base(path))
	switch {
	case strings.HasSuffix(name, ".kepub.epub"), strings.HasSuffix(name, ".kepub"):
		return "kepub"
	case strings.HasSuffix(name, ".fb2.zip"):
		return "fbz"
	}
	switch ext := strings.TrimPrefix(filepath.Ext(name), "."); ext {
	case "epub", "pdf", "mobi", "azw", "azw3", "prc", "fb2", "fbz", "txt", "rtf", "doc", "docx", "djvu", "cbz", "cbr", "cb7", "cbt":
		return ext
	}
	return ""
}

// BaseName is the file name without its book extension (both parts of
// ".kepub.epub" and ".fb2.zip").
func BaseName(path string) string {
	name := filepath.Base(path)
	low := strings.ToLower(name)
	for _, ext := range []string{".kepub.epub", ".fb2.zip"} {
		if strings.HasSuffix(low, ext) {
			return name[:len(name)-len(ext)]
		}
	}
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// Read reads a file's metadata (and its cover when wantCover).
func Read(path string, wantCover bool) (*Meta, error) {
	var m *Meta
	var err error
	switch Detect(path) {
	case "epub", "kepub":
		m, err = readEPUB(path, wantCover)
	case "mobi", "azw", "azw3", "prc":
		m, err = readMOBI(path, wantCover)
	case "fb2":
		m, err = readFB2File(path, wantCover)
	case "fbz":
		m, err = readFBZ(path, wantCover)
	case "pdf":
		m, err = readPDF(path, wantCover)
	case "cbz", "cbr", "cb7", "cbt":
		m, err = readComic(path, wantCover)
	case "docx":
		m, err = readDOCX(path)
	case "txt", "rtf", "doc", "djvu":
		m = &Meta{}
	default:
		return nil, ErrUnsupported
	}
	if err != nil {
		return nil, err
	}
	m.tidy()
	return m, nil
}

// tidy normalises what any reader found.
func (m *Meta) tidy() {
	m.Title = cleanSpace(m.Title)
	m.Subtitle = cleanSpace(m.Subtitle)
	var authors []string
	seen := map[string]bool{}
	for _, a := range m.Authors {
		a = cleanSpace(a)
		if a == "" || seen[strings.ToLower(a)] || strings.EqualFold(a, "unknown") || strings.EqualFold(a, "nepoznat") {
			continue
		}
		seen[strings.ToLower(a)] = true
		authors = append(authors, a)
	}
	m.Authors = authors
	m.Series = cleanSpace(m.Series)
	m.Publisher = cleanSpace(m.Publisher)
	m.Description = strings.TrimSpace(m.Description)
	m.Language = Language(m.Language)
	m.Published = NormalizeDate(m.Published)
	if m.Identifiers == nil {
		m.Identifiers = map[string]string{}
	}
	if m.ISBN == "" {
		m.ISBN = m.Identifiers["isbn"]
	}
	m.ISBN = NormalizeISBN(m.ISBN)
	if m.ISBN != "" {
		m.Identifiers["isbn"] = m.ISBN
	} else {
		delete(m.Identifiers, "isbn")
	}
	var tags []string
	tseen := map[string]bool{}
	for _, t := range m.Tags {
		t = cleanSpace(t)
		if t == "" || tseen[strings.ToLower(t)] || len(t) > 60 {
			continue
		}
		tseen[strings.ToLower(t)] = true
		tags = append(tags, t)
	}
	m.Tags = tags
	if m.Pages == 0 && m.Words > 0 {
		m.Pages = max(1, m.Words/250)
	}
}

var spaceRun = regexp.MustCompile(`\s+`)

func cleanSpace(s string) string { return strings.TrimSpace(spaceRun.ReplaceAllString(s, " ")) }

// PartialMD5 is KOReader's document hash of a file (frontend/util.lua
// partialMD5): 1 KiB samples at 0, 1 KiB, 4 KiB, 16 KiB … 1 GiB, stopping at
// the first sample past the end of the file.
func PartialMD5(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	buf := make([]byte, 1024)
	for i := -1; i <= 10; i++ {
		off := int64(0)
		if i >= 0 {
			off = int64(1024) << (2 * uint(i))
		}
		n, err := f.ReadAt(buf, off)
		if n == 0 {
			if err != nil && err != io.EOF {
				return "", err
			}
			break
		}
		h.Write(buf[:n])
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// NameMD5 is KOReader's (and CrossPoint's default) "file name" hash: the MD5 of the base name.
func NameMD5(path string) string {
	h := md5.Sum([]byte(filepath.Base(path)))
	return hex.EncodeToString(h[:])
}

// IsComic reports whether a format is a comic archive.
func IsComic(format string) bool {
	switch format {
	case "cbz", "cbr", "cb7", "cbt":
		return true
	}
	return false
}

var mimes = map[string]string{
	"epub": "application/epub+zip", "kepub": "application/kepub+zip", "pdf": "application/pdf",
	"mobi": "application/x-mobipocket-ebook", "prc": "application/x-mobipocket-ebook",
	"azw3": "application/vnd.amazon.mobi8-ebook", "azw": "application/vnd.amazon.ebook",
	"fb2": "application/x-fictionbook+xml", "fbz": "application/x-zip-compressed-fb2",
	"cbz": "application/vnd.comicbook+zip", "cbr": "application/vnd.comicbook-rar", "cb7": "application/x-cb7", "cbt": "application/x-cbt",
	"txt": "text/plain; charset=utf-8", "rtf": "application/rtf", "doc": "application/msword",
	"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "djvu": "image/vnd.djvu",
}

// MIME returns the media type of a format.
func MIME(format string) string {
	if m, ok := mimes[format]; ok {
		return m
	}
	return "application/octet-stream"
}

// ---- normalisers ----

var lang3 = map[string]string{
	"eng": "en", "srp": "sr", "scc": "sr", "hrv": "hr", "scr": "hr", "bos": "bs", "cnr": "sr", "slv": "sl", "mac": "mk", "mkd": "mk",
	"ger": "de", "deu": "de", "fre": "fr", "fra": "fr", "spa": "es", "ita": "it", "rus": "ru", "por": "pt", "dut": "nl", "nld": "nl",
	"pol": "pl", "cze": "cs", "ces": "cs", "slo": "sk", "slk": "sk", "bul": "bg", "hun": "hu", "rum": "ro", "ron": "ro", "gre": "el", "ell": "el",
	"tur": "tr", "swe": "sv", "nor": "no", "nob": "nb", "nno": "nn", "dan": "da", "fin": "fi", "jpn": "ja", "chi": "zh", "zho": "zh",
	"kor": "ko", "ara": "ar", "heb": "he", "ukr": "uk", "lat": "la", "alb": "sq", "sqi": "sq", "cat": "ca", "est": "et", "lav": "lv",
	"lit": "lt", "ice": "is", "isl": "is", "per": "fa", "fas": "fa", "hin": "hi", "vie": "vi", "tha": "th", "ind": "id", "bel": "be",
}

var langNames = map[string]string{
	"english": "en", "serbian": "sr", "croatian": "hr", "bosnian": "bs", "srpski": "sr", "hrvatski": "hr", "bosanski": "bs",
	"german": "de", "french": "fr", "spanish": "es", "italian": "it", "russian": "ru", "slovenian": "sl", "macedonian": "mk",
}

// Language maps a language tag ("en-US", "srp", "Serbian") to ISO 639-1 ("" when unknown).
func Language(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || s == "und" || s == "unk" || s == "zxx" || s == "mul" {
		return ""
	}
	if v, ok := langNames[s]; ok {
		return v
	}
	if i := strings.IndexAny(s, "-_"); i > 0 {
		s = s[:i]
	}
	if len(s) == 2 {
		return s
	}
	if v, ok := lang3[s]; ok {
		return v
	}
	return ""
}

var (
	dateFull = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})`)
	dateYM   = regexp.MustCompile(`^(\d{4})-(\d{2})`)
	dateYear = regexp.MustCompile(`\b(1[0-9]{3}|20[0-9]{2})\b`)
)

// NormalizeDate returns YYYY-MM-DD or YYYY ("" for unknown dates such as
// Calibre's 0101-01-01).
func NormalizeDate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if m := dateFull.FindStringSubmatch(s); m != nil {
		y, _ := strconv.Atoi(m[1])
		if y < 1000 {
			return ""
		}
		return m[0]
	}
	if m := dateYM.FindStringSubmatch(s); m != nil {
		y, _ := strconv.Atoi(m[1])
		if y < 1000 {
			return ""
		}
		return m[1]
	}
	if m := dateYear.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

var nonISBN = regexp.MustCompile(`[^0-9Xx]`)

// NormalizeISBN returns the ISBN-13 of an ISBN-10 or -13 with a valid check digit, else "".
func NormalizeISBN(s string) string {
	s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "urn:isbn:")
	d := strings.ToUpper(nonISBN.ReplaceAllString(s, ""))
	switch len(d) {
	case 13:
		if strings.Contains(d, "X") || !(strings.HasPrefix(d, "978") || strings.HasPrefix(d, "979")) {
			return ""
		}
		if int(d[12]-'0') != ean13Check(d[:12]) {
			return ""
		}
		return d
	case 10:
		if strings.Contains(d[:9], "X") {
			return ""
		}
		sum := 0
		for i := 0; i < 10; i++ {
			v := 10
			if d[i] != 'X' {
				v = int(d[i] - '0')
			}
			sum += v * (10 - i)
		}
		if sum%11 != 0 {
			return ""
		}
		core := "978" + d[:9]
		return core + string(rune('0'+ean13Check(core)))
	}
	return ""
}

func ean13Check(twelve string) int {
	sum := 0
	for i, r := range twelve {
		n := int(r - '0')
		if i%2 == 1 {
			n *= 3
		}
		sum += n
	}
	return (10 - sum%10) % 10
}

// imageMIME sniffs an image's type ("" = not an image we keep).
func imageMIME(b []byte) string {
	switch {
	case len(b) > 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return "image/jpeg"
	case len(b) > 8 && string(b[:8]) == "\x89PNG\r\n\x1a\n":
		return "image/png"
	case len(b) > 6 && (string(b[:6]) == "GIF87a" || string(b[:6]) == "GIF89a"):
		return "image/gif"
	case len(b) > 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp"
	case len(b) > 2 && b[0] == 'B' && b[1] == 'M':
		return "image/bmp"
	}
	return ""
}

func isImageName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".avif", ".jxl":
		return true
	}
	return false
}
