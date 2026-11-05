package server

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

func TestMoveBooksKeepsTheBook(t *testing.T) {
	f := newScanFixture(t)
	ctx := context.Background()
	// a book with a sidecar in Books
	dir := filepath.Join(f.lib.Paths[0], "Ivo Andric")
	os.MkdirAll(dir, 0o755)
	src := filepath.Join(dir, "Ivo Andric - Prokleta avlija.epub")
	os.WriteFile(src, testEPUB("Prokleta avlija", "Ivo Andrić"), 0o644)
	os.WriteFile(filepath.Join(dir, "Ivo Andric - Prokleta avlija.opf"), []byte("<package/>"), 0o644)
	f.srv.runScan(ctx, f.lib, false)
	list, _, err := f.st.ListBooks(ctx, store.BookQuery{Libraries: []int64{f.lib.ID}, Status: "any"})
	if err != nil || len(list) != 1 {
		t.Fatalf("scan: %v %d", err, len(list))
	}
	id := list[0].ID
	f.priv.UploadTemplate = "{author}/{author} - {title}"

	move := func(tok string, body map[string]any) (int, map[string]any) {
		b, _ := json.Marshal(body)
		res := f.do(t, "POST", "/api/v1/books/move", tok, bytes.NewReader(b), "application/json")
		out := map[string]any{}
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	if code, _ := move(f.editor, map[string]any{"ids": []int64{id}, "library_id": f.priv.ID}); code != 403 {
		t.Fatalf("an editor moved books: %d", code)
	}
	code, out := move(f.admin, map[string]any{"ids": []int64{id}, "library_id": f.priv.ID})
	if code != 200 || out["moved"] != float64(1) {
		t.Fatalf("move: %d %v", code, out)
	}
	moved := filepath.Join(f.priv.Paths[0], "Ivo Andric", "Ivo Andric - Prokleta avlija.epub")
	if _, err := os.Stat(moved); err != nil {
		t.Fatalf("file not moved: %v", err)
	}
	if _, err := os.Stat(strings.TrimSuffix(moved, ".epub") + ".opf"); err != nil {
		t.Fatalf("sidecar not moved: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("the emptied folder stayed")
	}
	// both libraries scanned again: still one book, the same one, in Private
	f.srv.runScan(ctx, f.lib, false)
	f.srv.runScan(ctx, f.priv, false)
	all, _, _ := f.st.ListBooks(ctx, store.BookQuery{Libraries: []int64{f.lib.ID, f.priv.ID}, Status: "any"})
	if len(all) != 1 || all[0].ID != id || all[0].LibraryID != f.priv.ID {
		t.Fatalf("after rescans: %+v", all)
	}
	b, _ := f.st.GetBookAny(ctx, id)
	if len(b.Files) != 1 || b.Files[0].Path != moved || b.Files[0].Missing {
		t.Fatalf("file row: %+v", b.Files)
	}
	// moving it back by the template
	code, out = move(f.admin, map[string]any{"ids": []int64{id}, "library_id": f.lib.ID, "rename": true})
	if code != 200 || out["moved"] != float64(1) {
		t.Fatalf("move back: %d %v", code, out)
	}
	if b, _ = f.st.GetBookAny(ctx, id); b.LibraryID != f.lib.ID || !strings.HasPrefix(b.Files[0].Path, f.lib.Paths[0]) {
		t.Fatalf("moved back: %+v", b)
	}
	if data, _ := os.ReadFile(filepath.Join(f.srv.cfg.DataDir, "moves.jsonl")); bytes.Count(data, []byte("\n")) != 2 {
		t.Fatalf("moves log: %s", data)
	}
}
