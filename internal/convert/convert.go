// Package convert turns a book file into another format: KEPUB from EPUB
// in-process (kepubify), everything else with calibre's ebook-convert when it
// is installed. Results are cached by file and modification time, and only
// one conversion runs at a time.
package convert

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pgaskin/kepubify/v4/kepub"
)

// ErrUnavailable means the conversion needs calibre, which is not installed.
var ErrUnavailable = errors.New("converting this format needs calibre's ebook-convert, which is not installed")

// Timeout bounds one conversion.
const Timeout = 5 * time.Minute

// Converter converts and caches.
type Converter struct {
	EbookConvert string // path to ebook-convert ('' = not installed)
	CacheDir     string // where results are kept
	mu           sync.Mutex
}

// Targets are the formats a book can be converted to.
var Targets = []string{"epub", "kepub", "azw3", "mobi", "pdf", "fb2", "txt", "docx"}

// Can reports whether from → to is possible on this box.
func (c *Converter) Can(from, to string) bool {
	if from == to {
		return true
	}
	if to == "kepub" && from == "epub" {
		return true
	}
	if c.EbookConvert == "" {
		return false
	}
	switch from {
	case "epub", "kepub", "mobi", "azw", "azw3", "prc", "fb2", "fbz", "docx", "rtf", "txt", "pdf", "cbz", "cbr":
	default:
		return false
	}
	for _, t := range Targets {
		if t == to {
			return true
		}
	}
	return false
}

// Convert returns the path of src converted to format `to`, from the cache
// when it is there. key identifies the source (file id and mtime).
func (c *Converter) Convert(ctx context.Context, src, from, to, key string) (string, error) {
	from, to = strings.ToLower(from), strings.ToLower(to)
	if !c.Can(from, to) {
		if to == "kepub" || c.EbookConvert != "" {
			return "", fmt.Errorf("cannot convert %s to %s", strings.ToUpper(from), strings.ToUpper(to))
		}
		return "", ErrUnavailable
	}
	if from == to {
		return src, nil
	}
	dir := filepath.Join(c.CacheDir, "convert")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	ext := to
	if to == "kepub" {
		ext = "kepub.epub"
	}
	dst := filepath.Join(dir, key+"."+ext)
	if _, err := os.Stat(dst); err == nil {
		now := time.Now()
		_ = os.Chtimes(dst, now, now) // the cache sweep keeps recently used results
		return dst, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := os.Stat(dst); err == nil {
		return dst, nil
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	var err error
	if to == "kepub" && from == "epub" {
		err = kepubify(ctx, src, dst)
	} else {
		err = c.ebookConvert(ctx, src, from, dst)
	}
	if err != nil {
		os.Remove(dst)
		return "", err
	}
	return dst, nil
}

func kepubify(ctx context.Context, src, dst string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("read EPUB: %w", err)
	}
	defer zr.Close()
	tmp := dst + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := kepub.NewConverter().Convert(ctx, f, zr); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("kepubify: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func (c *Converter) ebookConvert(ctx context.Context, src, from, dst string) error {
	// ebook-convert decides by extension, so give the input a proper one
	in := src
	if !strings.EqualFold(filepath.Ext(src), "."+from) {
		in = dst + ".in." + from
		if err := os.Symlink(src, in); err != nil {
			return err
		}
		defer os.Remove(in)
	}
	tmp := strings.TrimSuffix(dst, filepath.Ext(dst)) + ".tmp" + filepath.Ext(dst)
	cmd := exec.CommandContext(ctx, c.EbookConvert, in, tmp)
	cmd.Env = append(os.Environ(), "QT_QPA_PLATFORM=offscreen", "HOME="+os.TempDir())
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		os.Remove(tmp)
		if ctx.Err() != nil {
			return fmt.Errorf("ebook-convert took longer than %s", Timeout)
		}
		msg := strings.TrimSpace(out.String())
		if len(msg) > 400 {
			msg = msg[len(msg)-400:]
		}
		return fmt.Errorf("ebook-convert: %v: %s", err, msg)
	}
	// calibre exits 0 even when it is not really there (a broken PATH): check the result
	if fi, err := os.Stat(tmp); err != nil || fi.Size() == 0 {
		os.Remove(tmp)
		return errors.New("ebook-convert produced no file")
	}
	return os.Rename(tmp, dst)
}

// Sweep removes cached results not used for maxAge.
func (c *Converter) Sweep(maxAge time.Duration) {
	dir := filepath.Join(c.CacheDir, "convert")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > maxAge {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
