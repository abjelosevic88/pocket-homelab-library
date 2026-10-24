package server

// Downloads: a book's file as it is, converted to another format, or with
// the catalogue's metadata written in — for browsers, e-readers (OPDS, the
// KOReader plugin) and every send to a device.

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-library/internal/convert"
	"github.com/abjelosevic88/pocket-homelab-library/internal/epub"
	"github.com/abjelosevic88/pocket-homelab-library/internal/formats"
	"github.com/abjelosevic88/pocket-homelab-library/internal/mail"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

func (s *Server) routesDownloads(raw chi.Router) {
	raw.Get("/books/{id}/download", s.handleBookDownload)
	raw.Get("/files/{id}/download", s.handleFileDownload)
}

// handleBookDownload: ?file= picks one of its files (default: the best),
// ?format= converts (epub, kepub, azw3, mobi, pdf …).
func (s *Server) handleBookDownload(w http.ResponseWriter, r *http.Request) {
	b := s.bookFor(w, r, idParam(r, "id"))
	if b == nil {
		return
	}
	f := pickFile(b, queryInt64(r, "file"), strings.ToLower(r.URL.Query().Get("format")))
	if f == nil {
		writeErr(w, http.StatusNotFound, "this book has no file to download")
		return
	}
	s.serveBookFile(w, r, userFrom(r.Context()), b, f, strings.ToLower(r.URL.Query().Get("format")))
}

func (s *Server) handleFileDownload(w http.ResponseWriter, r *http.Request) {
	b, f := s.fileFor(w, r, idParam(r, "id"))
	if f == nil {
		return
	}
	s.serveBookFile(w, r, userFrom(r.Context()), b, f, strings.ToLower(r.URL.Query().Get("format")))
}

// pickFile chooses a file of a book: the one asked for, else one already in
// the wanted format, else the best (EPUB first). Missing files are skipped.
func pickFile(b *store.Book, fileID int64, format string) *store.File {
	var best *store.File
	for i := range b.Files {
		f := &b.Files[i]
		if f.Missing {
			continue
		}
		if fileID > 0 {
			if f.ID == fileID {
				return f
			}
			continue
		}
		if format != "" && f.Format == format {
			return f
		}
		if best == nil {
			best = f // Files come best format first
		}
	}
	return best
}

// serveBookFile sends a book's file to a browser, e-reader or the KOReader
// plugin: the original with HTTP ranges, or converted to format ("" = as
// is), with the metadata edited here written into EPUBs when the box says so.
// The download is named "Author - Title.ext".
func (s *Server) serveBookFile(w http.ResponseWriter, r *http.Request, u *store.User, b *store.Book, f *store.File, format string) {
	if format == "" {
		format = f.Format
	}
	p, err := s.prepareFile(r.Context(), b, f, format, false)
	if err != nil {
		switch {
		case errors.Is(err, convert.ErrUnavailable):
			writeErr(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, os.ErrNotExist):
			writeErr(w, http.StatusNotFound, "the file is no longer in the library folder")
		default:
			s.log.Warn("download", "book", b.ID, "file", f.ID, "format", format, "err", err)
			writeErr(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	fh, err := os.Open(p)
	if err != nil {
		writeErr(w, http.StatusNotFound, "the file is no longer in the library folder")
		return
	}
	defer fh.Close()
	fi, err := fh.Stat()
	if err != nil {
		s.fail(w, err, "download")
		return
	}
	w.Header().Set("Content-Type", formats.MIME(format))
	w.Header().Set("Content-Disposition", "attachment; "+mail.FilenameParams(downloadName(b, format)))
	w.Header().Set("Cache-Control", "private, max-age=0")
	http.ServeContent(w, r, "", fi.ModTime(), fh)
}

// extOf is the file extension of a format.
func extOf(format string) string {
	switch format {
	case "kepub":
		return ".kepub.epub"
	case "fbz":
		return ".fb2.zip"
	}
	return "." + format
}

// downloadName is "Author - Title.ext", safe for every file system and
// e-reader (no colons: CrossPoint splits on them).
func downloadName(b *store.Book, format string) string {
	title := strings.TrimSpace(b.Title)
	if title == "" {
		title = "Book"
	}
	name := title
	if len(b.Authors) > 0 && b.Authors[0].Name != "" {
		name = b.Authors[0].Name + " - " + title
	}
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return ' '
		}
		if r < 0x20 {
			return -1
		}
		return r
	}, name)
	name = strings.Join(strings.Fields(name), " ")
	if rs := []rune(name); len(rs) > 110 {
		name = strings.TrimSpace(string(rs[:110]))
	}
	return name + extOf(format)
}

// converter is the box's converter (calibre when installed, kepubify always).
func (s *Server) converter() *convert.Converter {
	s.devices.convOnce.Do(func() {
		s.devices.conv = &convert.Converter{EbookConvert: s.cfg.ConvertPath(), CacheDir: s.cfg.CacheDir}
	})
	return s.devices.conv
}

// prepareFile returns the path of what to hand out for a file in a format:
// the original, a conversion, or an EPUB with the catalogue's metadata
// (box setting) and, for Kindles, Amazon's fixes. Results are cached.
func (s *Server) prepareFile(ctx context.Context, b *store.Book, f *store.File, format string, kindleFix bool) (string, error) {
	if _, err := os.Stat(f.Path); err != nil {
		return "", err
	}
	src, srcFormat := f.Path, f.Format
	key := fmt.Sprintf("%d-%d", f.ID, f.MTime)
	if format != srcFormat && !(format == "epub" && srcFormat == "kepub") {
		out, err := s.converter().Convert(ctx, src, srcFormat, format, key)
		if err != nil {
			return "", err
		}
		src, srcFormat = out, format
	}
	if srcFormat != "epub" && srcFormat != "kepub" {
		return src, nil
	}
	st, _ := s.store.GetSettings(ctx, 0)
	embed := st.EmbedMetadata
	fix := kindleFix && st.KindleFixEPUB
	if !embed && !fix {
		return src, nil
	}
	h := sha1.Sum([]byte(fmt.Sprintf("%s|%s|%s|%v|%v|%s", key, format, b.UpdatedAt, embed, fix, s.storedCoverName(ctx, b.ID))))
	dir := filepath.Join(s.cfg.CacheDir, "out")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, hex.EncodeToString(h[:10])+extOf(srcFormat))
	if _, err := os.Stat(dst); err == nil {
		now := time.Now()
		_ = os.Chtimes(dst, now, now)
		return dst, nil
	}
	s.devices.prepMu.Lock()
	defer s.devices.prepMu.Unlock()
	if _, err := os.Stat(dst); err == nil {
		return dst, nil
	}
	book, err := epub.Open(src)
	if err != nil {
		// not a well-formed EPUB: hand out what there is
		s.log.Warn("epub: cannot rewrite, sending as is", "book", b.ID, "file", f.ID, "err", err)
		return src, nil
	}
	defer book.Close()
	if embed {
		if err := book.Embed(s.epubMeta(ctx, b)); err != nil {
			s.log.Warn("epub: metadata not written", "book", b.ID, "err", err)
		}
	}
	if fix {
		lang := epub.NormalizeLanguage(b.Language)
		if lang == "" {
			lang = "en"
		}
		if err := book.KindleFix(lang); err != nil {
			s.log.Warn("epub: kindle fixes", "book", b.ID, "err", err)
		}
		if len(book.Fixed) > 0 {
			s.log.Info("epub: fixed for Kindle", "book", b.ID, "fixes", len(book.Fixed), "first", book.Fixed[0])
		}
	}
	if !book.Changed() {
		return src, nil
	}
	if err := book.Write(dst); err != nil {
		return "", err
	}
	return dst, nil
}

// epubMeta is the catalogue's metadata of a book for writing into an EPUB.
func (s *Server) epubMeta(ctx context.Context, b *store.Book) epub.Meta {
	m := epub.Meta{Title: b.Title, Series: b.Series, SeriesIndex: b.SeriesIndex, Description: b.Description,
		Language: b.Language, Publisher: b.Publisher, Published: b.Published, ISBN: b.ISBN, Tags: b.Tags}
	if b.Subtitle != "" && !strings.Contains(b.Title, b.Subtitle) {
		m.Title = b.Title + ": " + b.Subtitle
	}
	for _, a := range b.Authors {
		m.Authors = append(m.Authors, a.Name)
		m.AuthorSorts = append(m.AuthorSorts, store.AuthorSortKey(a.Name))
	}
	if c := s.storedCoverName(ctx, b.ID); c != "" {
		if data, err := os.ReadFile(filepath.Join(s.cfg.CoversDir, c)); err == nil && len(data) > 3 && data[0] == 0xff && data[1] == 0xd8 {
			m.Cover = data
		}
	}
	return m
}

// sweepOutputs removes prepared files and conversions not used for a week.
func (s *Server) sweepOutputs() {
	s.converter().Sweep(7 * 24 * time.Hour)
	dir := filepath.Join(s.cfg.CacheDir, "out")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > 7*24*time.Hour {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// storedCoverName is the stored cover file name of a book (” = none).
func (s *Server) storedCoverName(ctx context.Context, bookID int64) string {
	name, _, err := s.store.BookCoverFile(ctx, bookID)
	if err != nil {
		return ""
	}
	return name
}
