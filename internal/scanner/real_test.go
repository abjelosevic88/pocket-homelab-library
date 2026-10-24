package scanner

import (
	"context"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

// TestRealLibrary scans a real folder read-only when PLB_REAL_LIBRARY names
// one (kind in PLB_REAL_KIND, database and covers in PLB_REAL_DATA), and
// prints numbers and a sample. It never writes into the folder.
func TestRealLibrary(t *testing.T) {
	root := os.Getenv("PLB_REAL_LIBRARY")
	if root == "" {
		t.Skip("set PLB_REAL_LIBRARY")
	}
	data := os.Getenv("PLB_REAL_DATA")
	if data == "" {
		data = t.TempDir()
	}
	kind := os.Getenv("PLB_REAL_KIND")
	if kind == "" {
		kind = "books"
	}
	ctx := context.Background()
	st, err := store.Open(filepath.Join(data, "real.db"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sc := &Scanner{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), CoversDir: filepath.Join(data, "covers")}
	lib := &store.Library{Name: "Real", Kind: kind, Paths: []string{root}}
	if err := st.CreateLibrary(ctx, lib); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res, err := sc.Scan(ctx, lib, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("first scan: %d files, %d books, %d added, %d errors in %s", res.Files, res.Books, len(res.Added), len(res.Errors), time.Since(start).Round(time.Second))
	for i, e := range res.Errors {
		if i < 10 {
			t.Logf("  error: %s", e)
		}
	}
	start = time.Now()
	res2, err := sc.Scan(ctx, lib, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("second scan: %d added, %d updated, %d removed in %s", len(res2.Added), res2.Updated, res2.Removed, time.Since(start).Round(time.Millisecond))
	books, total, _ := st.ListBooks(ctx, store.BookQuery{Libraries: []int64{lib.ID}, Limit: 500, Sort: "random", Seed: rand.Int63()})
	covered, authored, multi := 0, 0, 0
	all, _, _ := st.ListBooks(ctx, store.BookQuery{Libraries: []int64{lib.ID}, Limit: 500})
	_ = all
	for _, b := range books {
		if b.Cover != "" {
			covered++
		}
		if len(b.Authors) > 0 {
			authored++
		}
		if len(b.Formats) > 1 {
			multi++
		}
	}
	t.Logf("%d books; of a sample of %d: %d with cover, %d with author, %d with several formats", total, len(books), covered, authored, multi)
	for i, b := range books {
		if i >= 100 {
			break
		}
		a := ""
		if len(b.Authors) > 0 {
			a = b.Authors[0].Name
		}
		t.Logf("  %-50.50s | %-25.25s | %-20.20s #%v | %v | lang %s | cover %v", b.Title, a, b.Series, b.SeriesIndex, b.Formats, b.Language, b.Cover != "")
	}
}
