package server

// Translate: an English book (uploaded, or one already in the library) into
// Serbian with Pocket AI, filed into the library as a book of its own. One
// job runs at a time; each sends a few requests at once. The model is the
// person's Pocket AI through the Core link "Generate text" (inference:write),
// so the app never holds an AI key. What the model answered is kept per job,
// so Retry after a failure or a restart continues instead of starting over.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-library/internal/formats"
	"github.com/abjelosevic88/pocket-homelab-library/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
	"github.com/abjelosevic88/pocket-homelab-library/internal/translate"
)

type translateState struct {
	wake    chan struct{}
	mu      sync.Mutex
	running int64
	active  int // parts of the running job at Pocket AI right now
	cancel  context.CancelCauseFunc
}

func newTranslateState() *translateState { return &translateState{wake: make(chan struct{}, 1)} }

var errCancelled = errors.New("cancelled")

// translateFrom are the formats a translation starts from (all but EPUB go
// through calibre first).
var translateFrom = map[string]bool{"epub": true, "kepub": true, "mobi": true, "azw": true, "azw3": true, "prc": true, "fb2": true, "fbz": true, "docx": true, "rtf": true, "txt": true, "pdf": true}

func (s *Server) routesTranslate(api chi.Router) {
	api.Get("/translate/status", s.handleTranslateStatus)
	api.Get("/translate/models", s.handleTranslateModels)
	api.Get("/translate/active", s.handleTranslateActive)
	api.Get("/translations", s.handleListTranslations)
	api.Post("/translations", s.handleCreateTranslation)
	api.Get("/translations/{id}", s.handleGetTranslation)
	api.Get("/translations/{id}/log", s.handleTranslationLog)
	api.Get("/translations/{id}/download", s.handleTranslationDownload)
	api.Post("/translations/{id}/cancel", s.handleCancelTranslation)
	api.Post("/translations/{id}/retry", s.handleRetryTranslation)
	api.Delete("/translations/{id}", s.handleDeleteTranslation)
}

func (s *Server) startTranslate(ctx context.Context) {
	go s.translateWorker(ctx)
}

func (s *Server) translationDir(id int64) string {
	return filepath.Join(s.cfg.DataDir, "translations", strconv.FormatInt(id, 10))
}

func (s *Server) wakeTranslate() {
	select {
	case s.translate.wake <- struct{}{}:
	default:
	}
}

// ---- the worker ----

func (s *Server) translateWorker(ctx context.Context) {
	if err := s.store.RequeueRunningTranslations(ctx); err != nil {
		s.log.Warn("translations: requeue", "err", err)
	}
	for {
		if t, err := s.store.NextTranslation(ctx); err == nil {
			s.runTranslation(ctx, t)
			continue
		} else if !errors.Is(err, store.ErrNotFound) {
			s.log.Warn("translations: queue", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-s.translate.wake:
		case <-time.After(time.Minute):
		}
	}
}

// pocketModel adapts Pocket AI to the translator.
type pocketModel struct{ c *core.InferenceClient }

func (m pocketModel) Complete(ctx context.Context, system, prompt string, jsonMode bool) (string, error) {
	r, err := m.c.Complete(ctx, system, prompt, jsonMode)
	return r.Text, err
}

func (s *Server) runTranslation(parent context.Context, t *store.Translation) {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	s.translate.mu.Lock()
	s.translate.running, s.translate.cancel = t.ID, cancel
	s.translate.mu.Unlock()
	defer func() {
		s.translate.mu.Lock()
		s.translate.running, s.translate.cancel, s.translate.active = 0, nil, 0
		s.translate.mu.Unlock()
	}()

	dir := s.translationDir(t.ID)
	logf, _ := os.OpenFile(filepath.Join(dir, "log.txt"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	var logMu sync.Mutex
	logLine := func(format string, a ...any) {
		if logf == nil {
			return
		}
		logMu.Lock()
		fmt.Fprintf(logf, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, a...))
		logMu.Unlock()
	}
	defer func() {
		if logf != nil {
			logf.Close()
		}
	}()
	if err := s.store.StartTranslation(ctx, t.ID); err != nil {
		s.log.Warn("translations: start", "id", t.ID, "err", err)
		return
	}
	logLine("start: %s (%s script, model %s)", t.SourceName, t.Script, firstNonEmptyStr(t.Model, "Pocket AI default"))
	started := time.Now()
	res, err := s.translateOnce(ctx, t, dir, logLine)
	t.Step = ""
	switch {
	case errors.Is(context.Cause(ctx), errCancelled):
		t.Status, t.Error = "cancelled", ""
		logLine("cancelled")
	case parent.Err() != nil:
		return // shutting down: stays "running" and is queued again on start
	case err != nil:
		t.Status, t.Error = "failed", err.Error()
		logLine("FAILED: %v", err)
	default:
		t.Status = "done"
		logLine("done in %s", time.Since(started).Round(time.Second))
	}
	if res != nil {
		t.Title, t.Author, t.TitleTranslated, t.Warnings = firstNonEmptyStr(res.Title, t.Title), firstNonEmptyStr(res.Author, t.Author), res.TitleTranslated, res.Warnings
	}
	if err := s.store.FinishTranslation(context.WithoutCancel(ctx), t); err != nil {
		s.log.Warn("translations: finish", "id", t.ID, "err", err)
	}
	s.log.Info("translation finished", "id", t.ID, "status", t.Status, "book", t.ResultBookID, "took", time.Since(started).Round(time.Second).String())
	if t.Status == "done" || t.Status == "failed" {
		s.notifyTranslation(context.WithoutCancel(ctx), t)
	}
}

func (s *Server) translateOnce(ctx context.Context, t *store.Translation, dir string, logf func(string, ...any)) (*translate.Result, error) {
	u, err := s.store.GetUser(ctx, t.UserID)
	if err != nil {
		return nil, err
	}
	pocket, status := s.core.SelectAI(ctx, u.Email, "pocket", false)
	if pocket == nil || !status.Ready {
		return nil, errors.New("Pocket AI: " + status.Detail)
	}

	src := filepath.Join(dir, "source"+bookExt(t.SourceName))
	from := formats.Detect(src)
	epubPath := src
	if from != "epub" && from != "kepub" {
		_ = s.store.TranslationStep(ctx, t.ID, "convert")
		epubPath = filepath.Join(dir, "source.epub")
		if _, err := os.Stat(epubPath); err != nil {
			logf("converting %s to EPUB with calibre", strings.ToUpper(from))
			if err := s.ebookToEPUB(ctx, src, from, epubPath); err != nil {
				return nil, err
			}
		}
	}

	job := &translate.Job{
		Model:    pocketModel{pocket.WithModel(t.Model)},
		Options:  translate.Options{Script: t.Script, Notes: t.Notes, Model: t.Model},
		CacheDir: filepath.Join(dir, "cache"),
		MaxChunk: s.cfg.TranslateChunk,
		Parallel: s.cfg.TranslateParallel,
		Log:      logf,
		Meta: func(title, author string) {
			_ = s.store.TranslationMeta(context.WithoutCancel(ctx), t.ID, title, author)
		},
		Progress: func(p translate.Progress) {
			s.translate.mu.Lock()
			s.translate.active = p.Active
			s.translate.mu.Unlock()
			_ = s.store.TranslationProgress(context.WithoutCancel(ctx), t.ID, p.Step, p.Units, p.Chars, p.Batches, p.Done, p.Cached)
		},
	}
	out := filepath.Join(dir, "translated.epub")
	res, err := job.Run(ctx, epubPath, out)
	if err != nil {
		return res, err
	}

	// file it into the library: next to the book it came from, else into uploads
	_ = s.store.TranslationStep(ctx, t.ID, "deliver")
	var lib *store.Library
	if t.SourceBookID > 0 {
		if b, err := s.store.GetBookAny(ctx, t.SourceBookID); err == nil {
			lib, _ = s.store.GetLibrary(ctx, b.LibraryID)
		}
	}
	if lib == nil || len(lib.Paths) == 0 {
		if lib, err = s.store.UploadLibrary(ctx); err != nil {
			return res, errors.New("there is no library to file the translation into")
		}
	}
	name := safeComponent(res.TitleTranslated)
	if res.Author != "" {
		name = safeComponent(res.TitleTranslated + " - " + res.Author)
	}
	s.scan.writeMu.Lock()
	dest, err := s.fileInto(lib, out, firstNonEmptyStr(name, "book")+".epub", false)
	var ids []int64
	if err == nil {
		ids = s.registerFiles(ctx, lib, []string{dest})
	}
	s.scan.writeMu.Unlock()
	if err != nil {
		return res, fmt.Errorf("file into %s: %w", lib.Name, err)
	}
	logf("filed into %s: %s", lib.Name, dest)
	if len(ids) > 0 {
		t.ResultBookID = ids[0]
	}
	_ = os.RemoveAll(filepath.Join(dir, "cache"))
	return res, nil
}

// ebookToEPUB converts with calibre. It is slow for big PDFs, so it gets
// more time than a download's conversion.
func (s *Server) ebookToEPUB(ctx context.Context, src, from, dst string) error {
	bin := s.cfg.ConvertPath()
	if bin == "" {
		return errors.New("translating " + strings.ToUpper(from) + " needs calibre's ebook-convert, which is not installed; upload an EPUB")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()
	tmp := strings.TrimSuffix(dst, ".epub") + ".tmp.epub"
	args := []string{src, tmp}
	if from == "pdf" {
		args = append(args, "--enable-heuristics")
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(), "QT_QPA_PLATFORM=offscreen", "HOME="+os.TempDir())
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		os.Remove(tmp)
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		msg := strings.TrimSpace(out.String())
		if len(msg) > 600 {
			msg = msg[len(msg)-600:]
		}
		return fmt.Errorf("ebook-convert: %v: %s", err, msg)
	}
	return os.Rename(tmp, dst)
}

func (s *Server) notifyTranslation(ctx context.Context, t *store.Translation) {
	u, err := s.store.GetUser(ctx, t.UserID)
	if err != nil {
		return
	}
	st, err := s.store.GetSettings(ctx, u.ID)
	if err != nil || st.NotifyURL == "" {
		return
	}
	msg := notify.Message{Title: "Translated: " + firstNonEmptyStr(t.TitleTranslated, t.Title, t.SourceName), Priority: "info", Tags: []string{"books"}}
	msg.Body = firstNonEmptyStr(t.Title, t.SourceName) + " is in Serbian now"
	if len(t.Warnings) > 0 {
		msg.Body += fmt.Sprintf(" (%d warning(s): some passages stayed in English)", len(t.Warnings))
	}
	if t.Status == "failed" {
		msg = notify.Message{Title: "Translation failed: " + firstNonEmptyStr(t.Title, t.SourceName), Body: t.Error + "\nRetry continues where it stopped.", Priority: "failure", Tags: []string{"books", "warning"}}
	}
	if s.cfg.BaseURL != "" {
		msg.Link = s.cfg.BaseURL + "/translate"
		if t.ResultBookID > 0 {
			msg.Link = fmt.Sprintf("%s/book/%d", s.cfg.BaseURL, t.ResultBookID)
		}
	}
	if err := notify.New(st.NotifyURL, st.NotifyFormat).Send(ctx, msg); err != nil {
		s.log.Debug("notify translation", "err", err)
	}
}

func firstNonEmptyStr(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// ---- the API ----

func (s *Server) handleTranslateStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, status := s.core.SelectAI(ctx, userFrom(ctx).Email, "pocket", false)
	writeJSON(w, http.StatusOK, map[string]any{
		"ready": status.Ready, "detail": status.Detail, "core": s.coreConnected(),
		"convert": s.cfg.ConvertPath() != "", "parallel": s.cfg.TranslateParallel,
	})
}

func (s *Server) handleTranslateModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, status := s.core.SelectAI(ctx, userFrom(ctx).Email, "pocket", false)
	if c == nil || !status.Ready {
		writeErr(w, http.StatusServiceUnavailable, status.Detail)
		return
	}
	models, err := c.Models(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, models)
}

func (s *Server) handleListTranslations(w http.ResponseWriter, r *http.Request) {
	book, _ := strconv.ParseInt(r.URL.Query().Get("book"), 10, 64)
	list, err := s.store.ListTranslations(r.Context(), actorID(r.Context()), book)
	if err != nil {
		s.fail(w, err, "translations")
		return
	}
	for i := range list {
		s.liveActive(&list[i])
	}
	writeJSON(w, http.StatusOK, list)
}

// liveActive fills in how many parts of a running job are at Pocket AI.
func (s *Server) liveActive(t *store.Translation) {
	s.translate.mu.Lock()
	defer s.translate.mu.Unlock()
	if t.Status == "running" && s.translate.running == t.ID {
		t.Active = s.translate.active
	}
}

// handleTranslateActive is the sidebar's light poll: the person's running
// job (if any) and how many wait.
func (s *Server) handleTranslateActive(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListTranslations(r.Context(), actorID(r.Context()), 0)
	if err != nil {
		s.fail(w, err, "translations")
		return
	}
	out := map[string]any{"running": nil, "queued": 0}
	queued := 0
	for i := range list {
		switch list[i].Status {
		case "running":
			s.liveActive(&list[i])
			out["running"] = list[i]
		case "queued":
			queued++
		}
	}
	out["queued"] = queued
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) translationOf(w http.ResponseWriter, r *http.Request) *store.Translation {
	t, err := s.store.GetTranslation(r.Context(), actorID(r.Context()), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "translation")
		return nil
	}
	return t
}

func (s *Server) handleGetTranslation(w http.ResponseWriter, r *http.Request) {
	if t := s.translationOf(w, r); t != nil {
		s.liveActive(t)
		writeJSON(w, http.StatusOK, t)
	}
}

func validScript(v string) bool { return v == "latin" || v == "cyrillic" }

// handleCreateTranslation starts a job from an uploaded file (multipart:
// file, script, model, notes) or from a book in the library (JSON:
// book_id, file_id optional, script, model, notes).
func (s *Server) handleCreateTranslation(w http.ResponseWriter, r *http.Request) {
	if !s.requireEditor(w, r) {
		return
	}
	ctx := r.Context()
	u := userFrom(ctx)
	t := &store.Translation{UserID: u.ID, Script: "latin"}
	var srcPath string
	var cleanup func()
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ct == "multipart/form-data" {
		r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBookUploadBytes)
		mr, err := r.MultipartReader()
		if err != nil {
			writeErr(w, http.StatusBadRequest, "expected a multipart upload")
			return
		}
		tmpDir := filepath.Join(s.cfg.CacheDir, "uploads")
		if err := os.MkdirAll(tmpDir, 0o755); err != nil {
			s.fail(w, err, "upload")
			return
		}
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				var mbe *http.MaxBytesError
				if errors.As(err, &mbe) {
					writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the upload is larger than %d MB", s.cfg.MaxBookUploadBytes>>20))
					return
				}
				writeErr(w, http.StatusBadRequest, "upload: "+err.Error())
				return
			}
			switch part.FormName() {
			case "script", "model", "notes":
				b, _ := io.ReadAll(io.LimitReader(part, 8<<10))
				v := strings.TrimSpace(string(b))
				switch part.FormName() {
				case "script":
					t.Script = v
				case "model":
					t.Model = v
				case "notes":
					t.Notes = v
				}
			case "file", "files":
				if srcPath != "" {
					_, _ = io.Copy(io.Discard, part)
					continue
				}
				name := filepath.Base(part.FileName())
				if !translateFrom[formats.Detect(name)] {
					_, _ = io.Copy(io.Discard, part)
					writeErr(w, http.StatusBadRequest, "translate EPUB, PDF, MOBI, AZW3, FB2, DOCX, RTF or TXT files")
					return
				}
				p, err := stagePart(tmpDir, part, bookExt(name))
				if err != nil {
					s.fail(w, err, "upload")
					return
				}
				srcPath, t.SourceName = p, name
				cleanup = func() { _ = os.Remove(p) }
			default:
				_, _ = io.Copy(io.Discard, part)
			}
			part.Close()
		}
		if srcPath == "" {
			writeErr(w, http.StatusBadRequest, "choose a book file")
			return
		}
		t.Title = formats.BaseName(t.SourceName)
	} else {
		var in struct {
			BookID int64  `json:"book_id"`
			FileID int64  `json:"file_id"`
			Script string `json:"script"`
			Model  string `json:"model"`
			Notes  string `json:"notes"`
		}
		if err := decode(r, &in); err != nil || in.BookID <= 0 {
			writeErr(w, http.StatusBadRequest, "book_id is required")
			return
		}
		b, err := s.store.GetBook(ctx, u.ID, in.BookID, s.allowedLibraries(ctx, u))
		if err != nil {
			s.fail(w, err, "book")
			return
		}
		f := pickTranslateFile(b.Files, in.FileID)
		if f == nil {
			writeErr(w, http.StatusBadRequest, "this book has no file that can be translated (EPUB, PDF, MOBI, AZW3, FB2, DOCX, RTF, TXT)")
			return
		}
		srcPath, t.SourceName, t.SourceBookID = f.Path, f.Name, b.ID
		t.Title = b.Title
		if len(b.Authors) > 0 {
			t.Author = b.Authors[0].Name
		}
		t.Script, t.Model, t.Notes = firstNonEmptyStr(in.Script, "latin"), in.Model, in.Notes
	}
	if cleanup != nil {
		defer cleanup()
	}
	if !validScript(t.Script) {
		writeErr(w, http.StatusBadRequest, "script is latin or cyrillic")
		return
	}
	if t.Model != "" && !core.ValidInferenceModel(t.Model) {
		writeErr(w, http.StatusBadRequest, "unknown model")
		return
	}
	if len(t.Notes) > 4000 {
		t.Notes = t.Notes[:4000]
	}
	if from := formats.Detect(t.SourceName); from != "epub" && from != "kepub" && s.cfg.ConvertPath() == "" {
		writeErr(w, http.StatusBadRequest, "translating "+strings.ToUpper(from)+" needs calibre's ebook-convert, which is not installed here; use an EPUB")
		return
	}
	if fi, err := os.Stat(srcPath); err == nil {
		t.SourceSize = fi.Size()
	}
	if err := s.store.CreateTranslation(ctx, t); err != nil {
		s.fail(w, err, "translation")
		return
	}
	dir := s.translationDir(t.ID)
	err := os.MkdirAll(dir, 0o755)
	if err == nil {
		err = copyFile(srcPath, filepath.Join(dir, "source"+bookExt(t.SourceName)))
	}
	if err != nil {
		_ = s.store.DeleteTranslation(ctx, t.ID)
		_ = os.RemoveAll(dir)
		s.fail(w, err, "translation")
		return
	}
	s.log.Info("translation queued", "id", t.ID, "by", u.Email, "source", t.SourceName, "book", t.SourceBookID)
	s.wakeTranslate()
	writeJSON(w, http.StatusOK, t)
}

// pickTranslateFile prefers the asked file, then EPUB, then formats calibre
// converts well, PDF last.
func pickTranslateFile(files []store.File, want int64) *store.File {
	order := []string{"epub", "kepub", "azw3", "mobi", "azw", "prc", "fb2", "fbz", "docx", "rtf", "txt", "pdf"}
	var best *store.File
	rank := len(order)
	for i := range files {
		f := &files[i]
		if f.Missing || !translateFrom[f.Format] {
			continue
		}
		if want > 0 && f.ID == want {
			return f
		}
		for k, fm := range order {
			if fm == f.Format && k < rank {
				best, rank = f, k
			}
		}
	}
	return best
}

func (s *Server) handleTranslationLog(w http.ResponseWriter, r *http.Request) {
	t := s.translationOf(w, r)
	if t == nil {
		return
	}
	b, _ := os.ReadFile(filepath.Join(s.translationDir(t.ID), "log.txt"))
	if len(b) > 256<<10 {
		b = b[len(b)-256<<10:]
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(b)
}

func (s *Server) handleTranslationDownload(w http.ResponseWriter, r *http.Request) {
	t := s.translationOf(w, r)
	if t == nil {
		return
	}
	p := filepath.Join(s.translationDir(t.ID), "translated.epub")
	if t.Status != "done" {
		writeErr(w, http.StatusNotFound, "not translated yet")
		return
	}
	if _, err := os.Stat(p); err != nil {
		writeErr(w, http.StatusNotFound, "the file is gone; open the book in the library")
		return
	}
	name := firstNonEmptyStr(t.TitleTranslated, t.Title, "book")
	if t.Author != "" {
		name += " - " + t.Author
	}
	w.Header().Set("Content-Type", "application/epub+zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": safeComponent(name) + ".epub"}))
	http.ServeFile(w, r, p)
}

func (s *Server) stopTranslation(id int64) bool {
	s.translate.mu.Lock()
	defer s.translate.mu.Unlock()
	if s.translate.running == id && s.translate.cancel != nil {
		s.translate.cancel(errCancelled)
		return true
	}
	return false
}

func (s *Server) handleCancelTranslation(w http.ResponseWriter, r *http.Request) {
	t := s.translationOf(w, r)
	if t == nil {
		return
	}
	switch t.Status {
	case "running":
		s.stopTranslation(t.ID) // the worker marks it cancelled
	case "queued":
		_ = s.store.SetTranslationStatus(r.Context(), t.ID, "cancelled")
	default:
		writeErr(w, http.StatusConflict, "it is not running")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRetryTranslation(w http.ResponseWriter, r *http.Request) {
	t := s.translationOf(w, r)
	if t == nil {
		return
	}
	if t.Status != "failed" && t.Status != "cancelled" {
		writeErr(w, http.StatusConflict, "only a failed or cancelled translation can be tried again")
		return
	}
	if err := s.store.SetTranslationStatus(r.Context(), t.ID, "queued"); err != nil {
		s.fail(w, err, "translation")
		return
	}
	s.wakeTranslate()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleDeleteTranslation removes the job and its files; a book it filed
// into the library stays there.
func (s *Server) handleDeleteTranslation(w http.ResponseWriter, r *http.Request) {
	t := s.translationOf(w, r)
	if t == nil {
		return
	}
	if s.stopTranslation(t.ID) {
		for i := 0; i < 50; i++ { // let the worker let go of the files
			s.translate.mu.Lock()
			busy := s.translate.running == t.ID
			s.translate.mu.Unlock()
			if !busy {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if err := s.store.DeleteTranslation(r.Context(), t.ID); err != nil {
		s.fail(w, err, "translation")
		return
	}
	_ = os.RemoveAll(s.translationDir(t.ID))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
