package server

// Sending books to devices: Kindles and other e-mail devices get a mail,
// a CrossPoint reader on a reachable network gets the book pushed onto its
// SD card, and everything else (KOReader, a CrossPoint that is away) waits
// in a queue the device fetches from (the KOReader plugin, the OPDS
// catalogue's "To send to this device").

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-library/internal/convert"
	"github.com/abjelosevic88/pocket-homelab-library/internal/crosspoint"
	"github.com/abjelosevic88/pocket-homelab-library/internal/formats"
	pmail "github.com/abjelosevic88/pocket-homelab-library/internal/mail"
	"github.com/abjelosevic88/pocket-homelab-library/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

// MaxMailBytes is Amazon's limit for one e-mail (all attachments).
const MaxMailBytes = 50 << 20

// emailFormats are what Amazon (and most e-mail-to-device services) take, best first.
var emailFormats = []string{"epub", "kepub", "pdf", "docx", "doc", "rtf", "txt"}

// toEPUB are formats that become an EPUB with calibre.
var toEPUB = map[string]bool{"mobi": true, "azw3": true, "azw": true, "prc": true, "fb2": true, "fbz": true, "docx": true, "rtf": true, "txt": true}

// plan is what a send will deliver.
type plan struct {
	file   *store.File
	format string // what the device gets
	method string // email | push | pull
}

// planSend decides which file goes to a device, in which format and how.
// wantFile/wantFormat are the person's explicit choices (0/"" = decide).
func (s *Server) planSend(b *store.Book, d *store.Device, wantFile int64, wantFormat string) (*plan, error) {
	conv := s.converter()
	var files []*store.File
	for i := range b.Files {
		f := &b.Files[i]
		if f.Missing || (wantFile > 0 && f.ID != wantFile) {
			continue
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		if wantFile > 0 {
			return nil, store.ErrNotFound
		}
		return nil, errors.New("this book has no file in the library folder")
	}
	find := func(format string) *store.File {
		for _, f := range files {
			if f.Format == format {
				return f
			}
		}
		return nil
	}
	convertible := func(to string) (*store.File, error) {
		for _, f := range files {
			if (to == "epub" && toEPUB[f.Format] || to != "epub") && f.Format != to && conv.Can(f.Format, to) {
				return f, nil
			}
		}
		if conv.EbookConvert == "" {
			return nil, convert.ErrUnavailable
		}
		return nil, fmt.Errorf("no file of this book converts to %s", strings.ToUpper(to))
	}
	if wantFormat == "original" {
		wantFormat = ""
	}
	if wantFormat == "" && d.Format != "" && d.Format != "original" {
		wantFormat = d.Format
	}
	switch d.Kind {
	case "kindle", "email":
		p := &plan{method: "email"}
		if wantFormat != "" {
			ok := false
			for _, f := range emailFormats {
				if f == wantFormat {
					ok = true
				}
			}
			if !ok && d.Kind == "kindle" {
				return nil, fmt.Errorf("Amazon does not take %s by e-mail; send EPUB or PDF", strings.ToUpper(wantFormat))
			}
			if f := find(wantFormat); f != nil {
				p.file, p.format = f, wantFormat
				return p, nil
			}
			f, err := convertible(wantFormat)
			if err != nil {
				return nil, err
			}
			p.file, p.format = f, wantFormat
			return p, nil
		}
		for _, fm := range emailFormats {
			if f := find(fm); f != nil {
				p.file, p.format = f, fm
				if fm == "kepub" {
					p.format = "epub"
				}
				return p, nil
			}
		}
		f, err := convertible("epub")
		if err != nil {
			if errors.Is(err, convert.ErrUnavailable) {
				return nil, errors.New("Amazon no longer takes MOBI or AZW3: this book has no EPUB, and calibre is not installed for converting")
			}
			return nil, err
		}
		p.file, p.format = f, "epub"
		return p, nil
	case "crosspoint":
		p := &plan{method: "pull", format: "epub"}
		if push, _ := d.Settings["push"].(bool); push && d.Address != "" {
			p.method = "push"
		}
		if f := find("epub"); f != nil {
			p.file = f
		} else if f := find("kepub"); f != nil {
			p.file = f
		} else {
			f, err := convertible("epub")
			if err != nil {
				if errors.Is(err, convert.ErrUnavailable) {
					return nil, errors.New("CrossPoint reads EPUB only: this book has no EPUB, and calibre is not installed for converting")
				}
				return nil, err
			}
			p.file = f
		}
		return p, nil
	case "koreader":
		p := &plan{method: "pull"}
		if wantFormat != "" {
			if f := find(wantFormat); f != nil {
				p.file, p.format = f, wantFormat
				return p, nil
			}
			f, err := convertible(wantFormat)
			if err != nil {
				return nil, err
			}
			p.file, p.format = f, wantFormat
			return p, nil
		}
		p.file = files[0] // best format first; KOReader reads them all
		p.format = p.file.Format
		return p, nil
	}
	return nil, fmt.Errorf("cannot send to a %s", d.Kind)
}

type sendIn struct {
	DeviceID int64   `json:"device_id"`
	FileID   int64   `json:"file_id"`
	Format   string  `json:"format"`
	BookIDs  []int64 `json:"book_ids"`
}

// sendDevice is the device asked for, else the person's default.
func (s *Server) sendDevice(ctx context.Context, uid, id int64) (*store.Device, error) {
	if id > 0 {
		return s.store.GetDevice(ctx, uid, id)
	}
	d, err := s.store.DefaultDevice(ctx, uid)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errors.New("add a device first (Devices → Add a device)")
	}
	return d, err
}

// queueSend checks and records one send (the worker delivers mail and pushes).
func (s *Server) queueSend(ctx context.Context, u *store.User, b *store.Book, d *store.Device, fileID int64, format string) (*store.Send, error) {
	p, err := s.planSend(b, d, fileID, strings.ToLower(strings.TrimSpace(format)))
	if err != nil {
		return nil, err
	}
	if p.method == "email" {
		if _, err := s.mailAccount(ctx); err != nil {
			return nil, err
		}
	}
	if p.method == "pull" {
		// one waiting copy per book and device is enough
		if list, err := s.store.ListSends(ctx, u.ID, b.ID, 50); err == nil {
			for _, x := range list {
				if x.DeviceID == d.ID && x.Status == "queued" && x.Method == "pull" {
					return &x, nil
				}
			}
		}
	}
	x := &store.Send{DeviceID: d.ID, DeviceName: d.Name, BookID: b.ID, FileID: p.file.ID, Title: b.Title, Format: p.format, Method: p.method, Status: "queued", Bytes: p.file.Size}
	if err := s.store.CreateSend(ctx, u.ID, x); err != nil {
		return nil, err
	}
	if p.method != "pull" {
		s.kickSends()
	}
	if b.Cover != "" {
		x.Cover = b.Cover
	}
	return x, nil
}

func (s *Server) kickSends() {
	select {
	case s.devices.kick <- struct{}{}:
	default:
	}
}

func (s *Server) handleSendBook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	b := s.bookFor(w, r, idParam(r, "id"))
	if b == nil {
		return
	}
	var in sendIn
	_ = decode(r, &in)
	d, err := s.sendDevice(ctx, u.ID, in.DeviceID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	x, err := s.queueSend(ctx, u, b, d, in.FileID, in.Format)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, x)
}

// handleSendMany sends several books to one device; books that cannot go
// are listed with the reason.
func (s *Server) handleSendMany(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	var in sendIn
	if err := decode(r, &in); err != nil || len(in.BookIDs) == 0 {
		writeErr(w, http.StatusBadRequest, "book_ids are required")
		return
	}
	if len(in.BookIDs) > 200 {
		writeErr(w, http.StatusBadRequest, "at most 200 books at once")
		return
	}
	d, err := s.sendDevice(ctx, u.ID, in.DeviceID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	libs := s.allowedLibraries(ctx, u)
	sent := []store.Send{}
	type skip struct {
		BookID int64  `json:"book_id"`
		Title  string `json:"title"`
		Reason string `json:"reason"`
	}
	skipped := []skip{}
	for _, id := range in.BookIDs {
		b, err := s.store.GetBook(ctx, u.ID, id, libs)
		if err != nil {
			skipped = append(skipped, skip{BookID: id, Reason: "not found"})
			continue
		}
		x, err := s.queueSend(ctx, u, b, d, 0, in.Format)
		if err != nil {
			skipped = append(skipped, skip{BookID: id, Title: b.Title, Reason: err.Error()})
			continue
		}
		sent = append(sent, *x)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sends": sent, "skipped": skipped})
}

func (s *Server) handleListSends(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListSends(r.Context(), actorID(r.Context()), queryInt64(r, "book"), queryInt(r, "limit"))
	if err != nil {
		s.fail(w, err, "sends")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCancelSend(w http.ResponseWriter, r *http.Request) {
	err := s.store.CancelSend(r.Context(), actorID(r.Context()), idParam(r, "id"))
	if errors.Is(err, store.ErrConflict) {
		writeErr(w, http.StatusConflict, "only a send that is still waiting can be cancelled")
		return
	}
	if err != nil {
		s.fail(w, err, "cancel send")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRetrySend sends the same book to the same device again.
func (s *Server) handleRetrySend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	old, err := s.store.GetSend(ctx, u.ID, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "send")
		return
	}
	if old.BookID == 0 || old.DeviceID == 0 {
		writeErr(w, http.StatusBadRequest, "the book or the device is gone")
		return
	}
	b := s.bookFor(w, r, old.BookID)
	if b == nil {
		return
	}
	d, err := s.store.GetDevice(ctx, u.ID, old.DeviceID)
	if err != nil {
		s.fail(w, err, "device")
		return
	}
	x, err := s.queueSend(ctx, u, b, d, old.FileID, old.Format)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, x)
}

// ---- the worker ----

// sendWorker delivers waiting mail and pushes one at a time.
func (s *Server) sendWorker(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	sweep := time.NewTicker(24 * time.Hour)
	defer sweep.Stop()
	for {
		for {
			x, uid, err := s.store.NextSend(ctx)
			if err != nil {
				if !errors.Is(err, store.ErrNotFound) && ctx.Err() == nil {
					s.log.Warn("sends: next", "err", err)
				}
				break
			}
			s.deliver(ctx, uid, x)
		}
		select {
		case <-ctx.Done():
			return
		case <-s.devices.kick:
		case <-t.C:
		case <-sweep.C:
			s.sweepOutputs()
		}
	}
}

// deliver mails or pushes one send and records how it went.
func (s *Server) deliver(ctx context.Context, uid int64, x *store.Send) {
	_ = s.store.SetSendState(ctx, x.ID, x.Method, "sending", "", 0)
	method, status, msg, size := x.Method, "sent", "", int64(0)
	err := func() error {
		u, err := s.store.GetUser(ctx, uid)
		if err != nil {
			return err
		}
		d, err := s.store.GetDevice(ctx, uid, x.DeviceID)
		if err != nil {
			return errors.New("the device was removed")
		}
		b, err := s.store.GetBook(ctx, uid, x.BookID, s.allowedLibraries(ctx, u))
		if err != nil {
			return errors.New("the book is no longer in your libraries")
		}
		var f *store.File
		for i := range b.Files {
			if b.Files[i].ID == x.FileID && !b.Files[i].Missing {
				f = &b.Files[i]
			}
		}
		if f == nil {
			return errors.New("the file is no longer in the library folder")
		}
		cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		path, err := s.prepareFile(cctx, b, f, x.Format, x.Method == "email" && d.Kind == "kindle")
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		size = int64(len(data))
		name := downloadName(b, x.Format)
		if x.Method == "email" {
			if size > MaxMailBytes {
				return fmt.Errorf("the file is %.0f MB, over Amazon's 50 MB e-mail limit (use amazon.com/sendtokindle in a browser for files up to 200 MB)", float64(size)/(1<<20))
			}
			if x.Format == "epub" && strings.HasSuffix(name, ".kepub.epub") {
				name = strings.TrimSuffix(name, ".kepub.epub") + ".epub"
			}
			return s.mailBook(cctx, d.Email, b.Title, []pmail.Attachment{{Name: name, Type: formats.MIME(x.Format), Data: data}})
		}
		// push to a CrossPoint reader
		c, err := s.devices.crosspoint(d.Address)
		if err != nil {
			return err
		}
		folder, _ := d.Settings["folder"].(string)
		if err := c.Upload(cctx, folder, name, data); err != nil {
			if errors.Is(err, crosspoint.ErrUnreachable) {
				method, status = "pull", "queued"
				msg = "The reader wasn't reachable; the book waits in its catalogue under To send to this device."
				return nil
			}
			return err
		}
		return nil
	}()
	if err != nil {
		status, msg = "failed", err.Error()
	}
	if err := s.store.SetSendState(ctx, x.ID, method, status, msg, size); err != nil {
		s.log.Warn("sends: state", "id", x.ID, "err", err)
	}
	s.log.Info("send", "id", x.ID, "user", uid, "device", x.DeviceName, "method", method, "status", status, "error", msg)
	u, uerr := s.store.GetUser(ctx, uid)
	if uerr != nil {
		return
	}
	switch status {
	case "sent":
		s.publish(ctx, u, "library.sent", fmt.Sprintf("%d", x.BookID), map[string]any{"book_id": x.BookID, "title": x.Title, "device": x.DeviceName, "method": method, "status": status, "format": x.Format})
	case "failed":
		s.pushSendFailure(ctx, u, x, msg)
	}
}

// pushSendFailure tells the person a send failed (Settings → Notifications).
func (s *Server) pushSendFailure(ctx context.Context, u *store.User, x *store.Send, msg string) {
	st, err := s.store.GetSettings(ctx, u.ID)
	if err != nil || !st.NotifySent || st.NotifyURL == "" {
		return
	}
	n := notify.New(st.NotifyURL, st.NotifyFormat)
	link := ""
	if s.cfg.BaseURL != "" {
		link = fmt.Sprintf("%s/book/%d", s.cfg.BaseURL, x.BookID)
	}
	if err := n.Send(ctx, notify.Message{Title: "Couldn't send " + x.Title, Body: x.DeviceName + ": " + msg, Priority: "failure", Tags: []string{"books", "warning"}, Link: link}); err != nil {
		s.log.Debug("notify send failure", "err", err)
	}
}
