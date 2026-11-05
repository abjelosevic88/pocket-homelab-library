package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

func TestPDFOnlyBooksStayOffEReaders(t *testing.T) {
	f := newScanFixture(t)
	ctx := context.Background()
	mk := func(name, format string) int64 {
		p := filepath.Join(f.lib.Paths[0], name)
		os.WriteFile(p, []byte("x"), 0o644)
		id, _ := f.st.CreateBook(ctx, f.lib.ID, p, store.BookMeta{Title: name})
		f.st.UpsertFile(ctx, &store.File{BookID: id, LibraryID: f.lib.ID, Path: p, Format: format, Size: 1})
		return id
	}
	off := func(id int64) bool { b, _ := f.st.GetBookAny(ctx, id); return b.EReaderOff }
	pdf := mk("a.pdf", "pdf")
	epub := mk("b.epub", "epub")
	mine := mk("c.pdf", "pdf")
	if err := f.st.SetEReaderOff(ctx, []int64{mine}, false); err != nil { // a person wants it on
		t.Fatal(err)
	}
	f.srv.reconcileEReader(ctx)
	if !off(pdf) || off(epub) || off(mine) {
		t.Fatalf("after the rule: pdf %v epub %v mine %v", off(pdf), off(epub), off(mine))
	}
	// an EPUB of the PDF-only book arrives: back on
	p := filepath.Join(f.lib.Paths[0], "a.epub")
	os.WriteFile(p, []byte("x"), 0o644)
	f.st.UpsertFile(ctx, &store.File{BookID: pdf, LibraryID: f.lib.ID, Path: p, Format: "epub", Size: 1})
	f.srv.reconcileEReader(ctx)
	if off(pdf) {
		t.Fatal("still off with an EPUB")
	}
	// a person keeps the EPUB book off: the rule leaves it
	f.st.SetEReaderOff(ctx, []int64{epub}, true)
	f.srv.reconcileEReader(ctx)
	if !off(epub) {
		t.Fatal("the rule overrode a person")
	}
}
