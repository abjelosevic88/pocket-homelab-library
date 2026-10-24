// Package xpointer converts between KOReader's (CREngine's) XPointers, such
// as /body/DocFragment[12]/body/div/p[7]/text().123, and a position the web
// reader understands: the spine item and how far into its text the point is.
//
// The fraction counts the Unicode code points of the text nodes of the spine
// item's <body> in document order, with every run of whitespace counted as
// one space, before the point, divided by the total. The web reader computes
// it the same way.
//
// (contract stub — the sync work implements it; signatures are fixed)
package xpointer

import "errors"

// ErrNotFound is returned when a pointer does not resolve in the book.
var ErrNotFound = errors.New("xpointer: not found")

// Resolve finds where an XPointer points in an EPUB: the spine index
// (0-based) and the fraction (0..1) of that item's text before it.
func Resolve(epubPath, xp string) (spine int, fraction float64, err error) {
	return 0, 0, ErrNotFound
}

// Build makes the XPointer of the point a fraction into a spine item's text.
func Build(epubPath string, spine int, fraction float64) (string, error) {
	return "", ErrNotFound
}

// Overall returns the whole-book percentage of a spine position, weighting
// spine items by their text length (as KOReader roughly does).
func Overall(epubPath string, spine int, fraction float64) (float64, error) {
	return 0, ErrNotFound
}
