package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

func TestCleanupPlanAndRun(t *testing.T) {
	f := newScanFixture(t)
	ctx := context.Background()
	dir := filepath.Dir(f.lib.Paths[0])
	unsorted := &store.Library{Name: "Unsorted", Kind: "books", Paths: []string{filepath.Join(dir, "unsorted")}, UploadTemplate: "{author}/{author} - {title}"}
	tech := &store.Library{Name: "Tech books", Kind: "books", Paths: []string{filepath.Join(dir, "tech")}, UploadTemplate: "{author}/{author} - {title}"}
	for _, l := range []*store.Library{unsorted, tech} {
		os.MkdirAll(l.Paths[0], 0o755)
		if err := f.st.CreateLibrary(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	mk := func(lib *store.Library, rel, title, author, format string) int64 {
		t.Helper()
		p := filepath.Join(lib.Paths[0], rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, testEPUB(title, author), 0o644)
		m := store.BookMeta{Title: title}
		if author != "" {
			m.Authors = []string{author}
		}
		id, err := f.st.CreateBook(ctx, lib.ID, p, m)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.st.UpsertFile(ctx, &store.File{BookID: id, LibraryID: lib.ID, Path: p, Format: format, Size: 10}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	good := mk(f.lib, "Ivo Andric/Ivo Andric - Na Drini cuprija.epub", "Na Drini ćuprija", "Ivo Andrić", "epub")
	copyPDF := mk(f.lib, "Ivo Andric/Na Drini cuprija.pdf", "Na Drini cuprija", "Ivo Andric", "pdf")
	junk := mk(f.lib, "Bojana/x.epub", "(Microsoft Word - ANA.doc)", "Bojana", "epub")
	uploader := mk(f.lib, "t/Poezija.epub", "Poezija", "t", "epub")
	read := mk(f.lib, "x/Read.epub", "(\\212tampanje)", "Someone", "epub")
	admin, _ := f.st.GetUserByEmail(ctx, "admin@example.com")
	if _, _, err := f.st.SetStatus(ctx, admin.ID, read, "reading", ""); err != nil {
		t.Fatal(err)
	}
	fine := mk(unsorted, "misc/witches.epub", "A Discovery of Witches", "Deborah Harkness", "epub")
	learn := mk(unsorted, "misc/py.epub", "Learning Python Programming", "Mark Lutz", "epub")
	noAuthor := mk(unsorted, "misc/1514064693206618.epub", "LEGENDY POLSKIE", "", "epub")

	plan, err := f.srv.planCleanup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	moves := map[int64][]int64{}
	for _, m := range plan.Moves {
		moves[m.To] = m.IDs
	}
	want := map[int64]map[int64]bool{
		unsorted.ID: {copyPDF: true, junk: true, uploader: true},
		f.lib.ID:    {fine: true},
		tech.ID:     {learn: true},
	}
	for lib, ids := range want {
		if len(moves[lib]) != len(ids) {
			t.Fatalf("to library %d: %v, want %v", lib, moves[lib], ids)
		}
		for _, id := range moves[lib] {
			if !ids[id] {
				t.Fatalf("to library %d: %v, want %v", lib, moves[lib], ids)
			}
		}
	}
	if plan.Protected != 1 || plan.RenameTo != "Review" {
		t.Fatalf("protected %d, rename %q", plan.Protected, plan.RenameTo)
	}

	f.srv.runCleanup(plan)
	at := func(id int64) int64 { b, _ := f.st.GetBookAny(ctx, id); return b.LibraryID }
	for id, lib := range map[int64]int64{good: f.lib.ID, copyPDF: unsorted.ID, junk: unsorted.ID, read: f.lib.ID, fine: f.lib.ID, learn: tech.ID, noAuthor: unsorted.ID} {
		if at(id) != lib {
			t.Fatalf("book %d in %d, want %d", id, at(id), lib)
		}
	}
	if b, _ := f.st.GetBookAny(ctx, fine); b.Files[0].Path != filepath.Join(f.lib.Paths[0], "Deborah Harkness", "Deborah Harkness - A Discovery of Witches.epub") {
		t.Fatalf("filed as %s", b.Files[0].Path)
	}
	if l, _ := f.st.GetLibrary(ctx, unsorted.ID); l.Name != "Review" {
		t.Fatalf("not renamed: %s", l.Name)
	}
	if cleanupNow.Running || cleanupNow.Moved != 5 || len(cleanupNow.Errors) != 0 {
		t.Fatalf("run: %+v", cleanupNow)
	}
	// nothing left to do: an empty list, never null (the page reads it)
	again, err := f.srv.planCleanup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if j, _ := json.Marshal(again); !strings.Contains(string(j), `"moves":[]`) {
		t.Fatalf("empty plan: %s", j)
	}
}
