package server

import (
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

func TestSamePerson(t *testing.T) {
	same := [][2]string{{"J.R", "Joseph Roth"}, {"Karlos Ruis Safon", "Carlos Ruiz Zafon"}, {"Adams Daglas", "Douglas Adams"},
		{"Ernest Hemingvej", "Ernest Hemingway"}, {"Fjodor M. Dostojevski", "F. M. Dostojevski"}, {"Boris Vijan", "Boris Vian"}}
	differ := [][2]string{{"Ivan Goran Kovačić", "Ivan Gundulić"}, {"Jhumpa Lahiri", "Eckhart Tolle"}, {"J.K", "Joseph Roth"}, {"Ana", "Ivo Andrić"}}
	for _, p := range same {
		if !samePerson(p[0], p[1]) {
			t.Errorf("%q and %q should be one person", p[0], p[1])
		}
	}
	for _, p := range differ {
		if samePerson(p[0], p[1]) {
			t.Errorf("%q and %q are two people", p[0], p[1])
		}
	}
}

func TestCoverHashAndDuplicates(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, shade func(x, y int) uint8) string {
		img := image.NewRGBA(image.Rect(0, 0, 300, 450))
		for y := 0; y < 450; y++ {
			for x := 0; x < 300; x++ {
				v := shade(x, y)
				img.Set(x, y, color.RGBA{v, v / 2, 255 - v, 255})
			}
		}
		p := filepath.Join(dir, name)
		f, _ := os.Create(p)
		jpeg.Encode(f, img, &jpeg.Options{Quality: 70})
		f.Close()
		return p
	}
	a := write("a.jpg", func(x, y int) uint8 { return uint8((x*3 + y) % 256) })
	b := write("b.jpg", func(x, y int) uint8 { return uint8((x*3+y)%256) ^ 1 }) // the same cover, saved again
	c := write("c.jpg", func(x, y int) uint8 { return uint8((x * y / 50) % 256) })
	ha, _ := dHashFile(a)
	hb, _ := dHashFile(b)
	hc, _ := dHashFile(c)
	hex := func(v uint64) string { return strconv16(v) }
	if !sameCover(hex(ha), hex(hb)) || sameCover(hex(ha), hex(hc)) {
		t.Fatalf("hashes %x %x %x", ha, hb, hc)
	}
	books := []store.ReviewBook{
		{ID: 1, Title: "1002", Authors: []string{"J.R"}},
		{ID: 2, Title: "1002", Authors: []string{"Joseph Roth"}},
		{ID: 3, Title: "Pesme", Authors: []string{"Bojana"}, CoverHash: hex(ha)},
		{ID: 4, Title: "Pesme", Authors: []string{"Desanka Maksimović"}, CoverHash: hex(hc)},
		{ID: 5, Title: "Marina", Authors: []string{"Nobody"}, CoverHash: hex(ha)},
		{ID: 6, Title: "Marina", Authors: []string{"Someone"}, CoverHash: hex(hb)},
		{ID: 7, Title: "Other", Authors: []string{"X Y"}, Hashes: "abc"},
		{ID: 8, Title: "Different", Authors: []string{"Z Q"}, Hashes: "abc,def"},
	}
	got := map[int64]int{}
	for gi, g := range duplicateGroups(books) {
		for _, b := range g {
			got[b.ID] = gi + 1
		}
	}
	if got[1] == 0 || got[1] != got[2] || got[3] != 0 || got[4] != 0 || got[5] == 0 || got[5] != got[6] || got[7] == 0 || got[7] != got[8] {
		t.Fatalf("groups: %v", got)
	}
}

func strconv16(v uint64) string {
	const digits = "0123456789abcdef"
	b := make([]byte, 16)
	for i := 15; i >= 0; i-- {
		b[i] = digits[v&15]
		v >>= 4
	}
	return string(b)
}
