package server

// Copies of one book, for Review and the clean-up. Two books are copies when
// they share a file (the same SHA-256), or have the same title and
//   - authors that are the same person written differently: the same
//     surname, a transliteration (Safon/Zafón, Hemingvej/Hemingway), a
//     reversed name (Adams Daglas/Douglas Adams) or initials (J.R/Joseph
//     Roth), or
//   - the same cover (fingerprints a few bits apart).
// Cover fingerprints (a 64-bit difference hash of the cover, books.cover_hash)
// are taken in the background.

import (
	"context"
	"fmt"
	"image"
	_ "image/jpeg" // covers are JPEGs
	_ "image/png"
	"math/bits"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

// coverHashWorker fingerprints covers that have none (new books, changed covers).
func (s *Server) coverHashWorker(ctx context.Context) {
	for {
		todo, err := s.store.CoversToHash(ctx, 200)
		if err == nil && len(todo) > 0 {
			for id, name := range todo {
				h := ""
				if v, err := dHashFile(filepath.Join(s.cfg.CoversDir, name)); err == nil {
					h = fmt.Sprintf("%016x", v)
				}
				_ = s.store.SetCoverHash(ctx, id, name, h)
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Minute):
		}
	}
}

// dHashFile is the difference hash of an image: 9×8 grey cells, one bit per
// neighbouring pair (left brighter than right).
func dHashFile(p string) (uint64, error) {
	f, err := os.Open(p)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return 0, err
	}
	b := img.Bounds()
	if b.Dx() < 9 || b.Dy() < 8 {
		return 0, fmt.Errorf("image too small")
	}
	var cell [8][9]float64
	for y := 0; y < 8; y++ {
		for x := 0; x < 9; x++ {
			// the average of a 4×4 sample grid inside the cell
			x0, x1 := b.Min.X+x*b.Dx()/9, b.Min.X+(x+1)*b.Dx()/9
			y0, y1 := b.Min.Y+y*b.Dy()/8, b.Min.Y+(y+1)*b.Dy()/8
			sum, n := 0.0, 0
			for sy := 0; sy < 4; sy++ {
				for sx := 0; sx < 4; sx++ {
					px := x0 + (x1-x0)*(2*sx+1)/8
					py := y0 + (y1-y0)*(2*sy+1)/8
					r, g, bl, _ := img.At(px, py).RGBA()
					sum += 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bl)
					n++
				}
			}
			cell[y][x] = sum / float64(n)
		}
	}
	var h uint64
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			h <<= 1
			if cell[y][x] > cell[y][x+1] {
				h |= 1
			}
		}
	}
	return h, nil
}

// sameCover reports whether two fingerprints are a few bits apart.
func sameCover(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	x, err1 := strconv.ParseUint(a, 16, 64)
	y, err2 := strconv.ParseUint(b, 16, 64)
	return err1 == nil && err2 == nil && bits.OnesCount64(x^y) <= 6
}

// initialsOf is "jr" for "Joseph Roth".
func initialsOf(name string) string {
	var sb strings.Builder
	for _, w := range strings.Fields(fold(name)) {
		sb.WriteByte(w[0])
	}
	return sb.String()
}

// similar is a quick string likeness (1 = equal): the share of the longer
// string covered by the longest common subsequence.
func similar(a, b string) float64 {
	if a == b {
		return 1
	}
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 || len(rb) == 0 {
		return 0
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			if ra[i-1] == rb[j-1] {
				cur[j] = prev[j-1] + 1
			} else {
				cur[j] = max(prev[j], cur[j-1])
			}
		}
		prev, cur = cur, prev
	}
	return float64(prev[len(rb)]) / float64(max(len(ra), len(rb)))
}

// samePerson reports whether two author names look like one person.
func samePerson(x, y string) bool {
	fx, fy := strings.Fields(fold(x)), strings.Fields(fold(y))
	if len(fx) == 0 || len(fy) == 0 {
		return false
	}
	sx, sy := fx[len(fx)-1], fy[len(fy)-1]
	switch {
	case sx == sy:
		return true
	case len(sx) >= 4 && len(sy) >= 4 && similar(sx, sy) >= 0.65:
		return true // Safon / Zafon, Hemingvej / Hemingway (only ever asked for books with the same title)
	}
	// a reversed name: one's surname is the other's first word (Adams Daglas / Douglas Adams)
	if len(fx) >= 2 && len(fy) >= 2 && (fx[0] == sy || fy[0] == sx) && len(sx) >= 3 {
		return true
	}
	// initials: "J.R", "JR" for Joseph Roth
	jx, jy := strings.Join(fx, ""), strings.Join(fy, "")
	if len(jx) <= 3 && len(fy) >= 2 && jx == initialsOf(y) {
		return true
	}
	if len(jy) <= 3 && len(fx) >= 2 && jy == initialsOf(x) {
		return true
	}
	return false
}

// sameAuthors reports whether any author of one is an author of the other.
func sameAuthors(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if samePerson(x, y) {
				return true
			}
		}
	}
	return false
}

// duplicateGroups binds copies of one book (books with bad details left out
// by the caller as it likes). It returns groups of two or more.
func duplicateGroups(books []store.ReviewBook) [][]store.ReviewBook {
	parent := make([]int, len(books))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		if parent[i] != i {
			parent[i] = find(parent[i])
		}
		return parent[i]
	}
	union := func(i, j int) { parent[find(i)] = find(j) }
	byTitle := map[string][]int{}
	byFile := map[string][]int{}
	for i, b := range books {
		if t := fold(b.Title); t != "" {
			byTitle[t] = append(byTitle[t], i)
		}
		for _, h := range strings.Split(b.Hashes, ",") {
			if h != "" {
				byFile[h] = append(byFile[h], i)
			}
		}
	}
	for _, idx := range byFile {
		for _, j := range idx[1:] {
			union(idx[0], j)
		}
	}
	for _, idx := range byTitle {
		if len(idx) < 2 || len(idx) > 50 {
			continue // a title shared by dozens ("Pesme") is not one book
		}
		for a := 0; a < len(idx); a++ {
			for c := a + 1; c < len(idx); c++ {
				x, y := books[idx[a]], books[idx[c]]
				if sameAuthors(x.Authors, y.Authors) || sameCover(x.CoverHash, y.CoverHash) {
					union(idx[a], idx[c])
				}
			}
		}
	}
	groups := map[int][]store.ReviewBook{}
	for i, b := range books {
		r := find(i)
		groups[r] = append(groups[r], b)
	}
	out := [][]store.ReviewBook{}
	for _, g := range groups {
		if len(g) > 1 {
			out = append(out, g)
		}
	}
	return out
}
