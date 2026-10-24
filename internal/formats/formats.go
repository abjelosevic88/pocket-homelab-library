// Package formats reads e-book and comic files: what they are, their
// metadata and cover, KOReader's document hashes, and comic pages.
//
// (contract stub — the scanner work implements it; signatures are fixed)
package formats

import (
	"errors"
	"io"
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
func Detect(path string) string { return "" }

// Read reads a file's metadata (and its cover when wantCover).
func Read(path string, wantCover bool) (*Meta, error) { return nil, ErrUnsupported }

// ReadOPF reads an OPF document (an EPUB's package file or a Calibre sidecar).
func ReadOPF(r io.Reader) (*Meta, error) { return nil, ErrUnsupported }

// PartialMD5 is KOReader's document hash of a file.
func PartialMD5(path string) (string, error) { return "", ErrUnsupported }

// NameMD5 is KOReader's (and CrossPoint's default) "file name" hash: the MD5 of the base name.
func NameMD5(path string) string { return "" }

// IsComic reports whether a format is a comic archive.
func IsComic(format string) bool {
	switch format {
	case "cbz", "cbr", "cb7", "cbt":
		return true
	}
	return false
}

// MIME returns the media type of a format.
func MIME(format string) string { return "application/octet-stream" }

// Page is one image of a comic.
type Page struct {
	Name string
	Size int64
}

// Comic is an open comic archive.
type Comic struct{}

// OpenComic opens a CBZ/CBR/CB7/CBT; cacheDir may hold extracted pages of
// archives that cannot be read at random (RAR).
func OpenComic(path, cacheDir string) (*Comic, error) { return nil, ErrUnsupported }

// Pages lists the images in reading order.
func (c *Comic) Pages() []Page { return nil }

// Page opens page i (0-based) and returns its media type.
func (c *Comic) Page(i int) (io.ReadCloser, string, error) { return nil, "", ErrUnsupported }

// Close releases the archive.
func (c *Comic) Close() error { return nil }
