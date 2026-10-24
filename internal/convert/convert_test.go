package convert

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-library/internal/epub"
)

func TestKepub(t *testing.T) {
	dir := t.TempDir()
	data, err := epub.Minimal("T", "A", "en", "One.\n\nTwo.")
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "b.epub")
	os.WriteFile(src, data, 0o644)
	c := &Converter{CacheDir: dir}
	out, err := c.Convert(context.Background(), src, "epub", "kepub", "1-1")
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	found := false
	for _, f := range zr.File {
		if f.Name == "OEBPS/page.xhtml" {
			rc, _ := f.Open()
			b := make([]byte, 4096)
			n, _ := rc.Read(b)
			rc.Close()
			found = n > 0 && strings.Contains(string(b[:n]), "koboSpan")
		}
	}
	if !found {
		t.Fatal("no kobo spans")
	}
	if _, err := c.Convert(context.Background(), src, "mobi", "epub", "1-1"); err != ErrUnavailable {
		t.Fatalf("err = %v", err)
	}
}
