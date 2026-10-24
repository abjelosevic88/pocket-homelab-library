package server

// A person's reading devices — Kindles and other e-mail devices, Xteink
// readers with CrossPoint, KOReader — and how books reach them (send.go),
// downloads and conversions (download.go), and imports from the apps this
// one replaces (imports.go).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-library/internal/convert"
	"github.com/abjelosevic88/pocket-homelab-library/internal/crosspoint"
	"github.com/abjelosevic88/pocket-homelab-library/internal/epub"
	pmail "github.com/abjelosevic88/pocket-homelab-library/internal/mail"
	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

type deviceState struct {
	convOnce sync.Once
	conv     *convert.Converter
	prepMu   sync.Mutex    // one prepared EPUB written at a time
	kick     chan struct{} // wakes the send worker
	// how CrossPoint readers are reached (replaced in tests)
	crosspoint func(address string) (*crosspoint.Client, error)
}

func newDeviceState() *deviceState {
	return &deviceState{kick: make(chan struct{}, 1), crosspoint: crosspoint.New}
}

func (s *Server) startDevices(ctx context.Context) {
	if err := s.store.RequeueStuckSends(ctx); err != nil {
		s.log.Warn("sends: requeue", "err", err)
	}
	go s.sendWorker(ctx)
}

func (s *Server) routesDevices(api chi.Router) {
	api.Get("/devices", s.handleListDevices)
	api.Post("/devices", s.handleCreateDevice)
	api.Put("/devices/{id}", s.handleUpdateDevice)
	api.Delete("/devices/{id}", s.handleDeleteDevice)
	api.Get("/devices/{id}/setup", s.handleDeviceSetup)
	api.Post("/devices/{id}/password", s.handleDevicePassword)
	api.Post("/devices/{id}/default", s.handleDeviceDefault)
	api.Post("/devices/{id}/test", s.handleDeviceTest)
	api.Post("/devices/{id}/provision", s.handleDeviceProvision)

	api.Post("/books/{id}/send", s.handleSendBook)
	api.Post("/send", s.handleSendMany)
	api.Get("/sends", s.handleListSends)
	api.Delete("/sends/{id}", s.handleCancelSend)
	api.Post("/sends/{id}/retry", s.handleRetrySend)

	api.Get("/convert/formats", s.handleConvertFormats)

	s.routesImports(api)
}

func (s *Server) rawDevices(raw chi.Router) {
	s.routesDownloads(raw)
	raw.Post("/import/kindle-clippings", s.handleImportClippings)
}

// ---- devices ----

func validKind(k string) bool {
	switch k {
	case "kindle", "email", "crosspoint", "koreader":
		return true
	}
	return false
}

// signsIn reports whether a kind of device signs in itself (OPDS, KOReader sync).
func signsIn(kind string) bool { return kind == "crosspoint" || kind == "koreader" }

func cleanEmail(kind, v string) (string, error) {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		return "", errors.New("an e-mail address is required")
	}
	a, err := mail.ParseAddress(v)
	if err != nil || !strings.Contains(a.Address, "@") {
		return "", fmt.Errorf("%q is not an e-mail address", v)
	}
	if kind == "kindle" {
		_, dom, _ := strings.Cut(a.Address, "@")
		if !strings.HasPrefix(dom, "kindle.") && !strings.HasPrefix(dom, "free.kindle.") {
			return "", errors.New("a Kindle's address ends in @kindle.com (Amazon → Manage Your Content and Devices → Preferences → Personal Document Settings)")
		}
	}
	return a.Address, nil
}

type deviceIn struct {
	Kind     string         `json:"kind"`
	Name     string         `json:"name"`
	Email    string         `json:"email"`
	Address  string         `json:"address"`
	Format   string         `json:"format"`
	Settings map[string]any `json:"settings"`
}

func defaultName(kind string) string {
	switch kind {
	case "kindle":
		return "Kindle"
	case "crosspoint":
		return "Xteink X4"
	case "koreader":
		return "KOReader"
	}
	return "E-reader"
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListDevices(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "devices")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	var in deviceIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	in.Kind = strings.ToLower(strings.TrimSpace(in.Kind))
	if !validKind(in.Kind) {
		writeErr(w, http.StatusBadRequest, "kind must be kindle, email, crosspoint or koreader")
		return
	}
	d := &store.Device{Kind: in.Kind, Name: strings.TrimSpace(in.Name), Address: strings.TrimSpace(in.Address), Format: strings.ToLower(in.Format), Settings: in.Settings}
	if d.Name == "" {
		d.Name = defaultName(d.Kind)
	}
	if d.Kind == "kindle" || d.Kind == "email" {
		e, err := cleanEmail(d.Kind, in.Email)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		d.Email = e
	}
	if err := cleanDeviceSettings(d); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	out := map[string]any{}
	if signsIn(d.Kind) {
		plain, tok, err := s.store.CreateAPIToken(ctx, u.ID, fmt.Sprintf("%s (%s)", d.Name, d.Kind), "device")
		if err != nil {
			s.fail(w, err, "app password")
			return
		}
		d.TokenID = tok.ID
		out["password"], out["username"] = plain, u.Email
	}
	if err := s.store.CreateDevice(ctx, u.ID, d); err != nil {
		s.fail(w, err, "create device")
		return
	}
	d, _ = s.store.GetDevice(ctx, u.ID, d.ID)
	out["device"] = d
	out["setup"] = s.deviceSetup(ctx, r, u, d)
	writeJSON(w, http.StatusCreated, out)
}

// cleanDeviceSettings keeps the settings a kind understands.
func cleanDeviceSettings(d *store.Device) error {
	in := d.Settings
	out := map[string]any{}
	switch d.Kind {
	case "crosspoint":
		folder, _ := in["folder"].(string)
		folder = "/" + strings.Trim(strings.TrimSpace(folder), "/")
		if folder == "/" {
			folder = "/Books"
		}
		if strings.ContainsAny(folder, `\:`) {
			return errors.New("the folder may not contain : or \\")
		}
		out["folder"] = folder
		push, ok := in["push"].(bool)
		if !ok {
			push = d.Address != ""
		}
		out["push"] = push && d.Address != ""
		if d.Address != "" {
			if _, err := crosspoint.New(d.Address); err != nil {
				return err
			}
		}
	case "koreader":
		if folder, _ := in["folder"].(string); strings.TrimSpace(folder) != "" {
			out["folder"] = strings.TrimSpace(folder)
		}
	}
	switch d.Format {
	case "", "epub", "kepub", "pdf", "azw3", "mobi", "original":
	default:
		return fmt.Errorf("unknown format %q", d.Format)
	}
	if d.Kind == "crosspoint" {
		d.Format = "epub" // the only reflowable format it reads
	}
	d.Settings = out
	return nil
}

func (s *Server) deviceOf(w http.ResponseWriter, r *http.Request) *store.Device {
	d, err := s.store.GetDevice(r.Context(), actorID(r.Context()), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "device")
		return nil
	}
	return d
}

func (s *Server) handleUpdateDevice(w http.ResponseWriter, r *http.Request) {
	d := s.deviceOf(w, r)
	if d == nil {
		return
	}
	var in deviceIn
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if keys["name"] && strings.TrimSpace(in.Name) != "" {
		d.Name = strings.TrimSpace(in.Name)
	}
	if keys["email"] && (d.Kind == "kindle" || d.Kind == "email") {
		e, err := cleanEmail(d.Kind, in.Email)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		d.Email = e
	}
	if keys["address"] {
		d.Address = strings.TrimSpace(in.Address)
	}
	if keys["format"] {
		d.Format = strings.ToLower(in.Format)
	}
	if keys["settings"] {
		for k, v := range in.Settings {
			d.Settings[k] = v
		}
	}
	if err := cleanDeviceSettings(d); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	uid := actorID(r.Context())
	if err := s.store.UpdateDevice(r.Context(), uid, d); err != nil {
		s.fail(w, err, "update device")
		return
	}
	d, _ = s.store.GetDevice(r.Context(), uid, d.ID)
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteDevice(r.Context(), actorID(r.Context()), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete device")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeviceDefault(w http.ResponseWriter, r *http.Request) {
	if err := s.store.SetDefaultDevice(r.Context(), actorID(r.Context()), idParam(r, "id")); err != nil {
		s.fail(w, err, "default device")
		return
	}
	s.handleListDevices(w, r)
}

// newDevicePassword replaces a device's app password and returns the new one.
func (s *Server) newDevicePassword(ctx context.Context, u *store.User, d *store.Device) (string, error) {
	plain, tok, err := s.store.CreateAPIToken(ctx, u.ID, fmt.Sprintf("%s (%s)", d.Name, d.Kind), "device")
	if err != nil {
		return "", err
	}
	old := d.TokenID
	d.TokenID = tok.ID
	if err := s.store.UpdateDevice(ctx, u.ID, d); err != nil {
		_ = s.store.DeleteAPIToken(ctx, u.ID, tok.ID)
		return "", err
	}
	if old > 0 {
		_ = s.store.DeleteAPIToken(ctx, u.ID, old)
	}
	return plain, nil
}

func (s *Server) handleDevicePassword(w http.ResponseWriter, r *http.Request) {
	d := s.deviceOf(w, r)
	if d == nil {
		return
	}
	if !signsIn(d.Kind) {
		writeErr(w, http.StatusBadRequest, "this device does not sign in: books are e-mailed to it")
		return
	}
	u := userFrom(r.Context())
	plain, err := s.newDevicePassword(r.Context(), u, d)
	if err != nil {
		s.fail(w, err, "app password")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"password": plain, "username": u.Email, "device": d, "setup": s.deviceSetup(r.Context(), r, u, d)})
}

// ---- setup ----

// DeviceSetup is what to type into a device.
type DeviceSetup struct {
	OPDSURL     string   `json:"opds_url"`
	KosyncURL   string   `json:"kosync_url"`
	Username    string   `json:"username"`
	HasPassword bool     `json:"has_password"`
	PluginURL   string   `json:"plugin_url,omitempty"`
	SenderEmail string   `json:"sender_email,omitempty"`
	Steps       []string `json:"steps"`
}

// publicBase is BASE_URL, or what the request came in on.
func (s *Server) publicBase(r *http.Request) string {
	if s.cfg.BaseURL != "" {
		return s.cfg.BaseURL
	}
	if r == nil {
		return "https://your-library"
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) deviceSetup(ctx context.Context, r *http.Request, u *store.User, d *store.Device) DeviceSetup {
	base := s.publicBase(r)
	st, _ := s.store.GetSettings(ctx, 0)
	set := DeviceSetup{OPDSURL: base + "/opds", KosyncURL: base + "/kosync", Username: u.Email, HasPassword: d.TokenID > 0, SenderEmail: st.SMTPFrom}
	sender := st.SMTPFrom
	if sender == "" {
		sender = "the library's sender address (an admin sets it under Settings → Mail)"
	}
	switch d.Kind {
	case "kindle":
		set.Steps = []string{
			"On amazon.com open Manage Your Content and Devices → Preferences → Personal Document Settings.",
			"Under Approved Personal Document E-mail List add " + sender + ". Amazon throws away mail from addresses not on that list.",
			"Your Kindle's address (" + d.Email + ") is in the same place, under Send-to-Kindle E-Mail Settings.",
			"Send a book: it shows up in your Kindle's library within a few minutes (Wi-Fi on). Test sends a one-page document first.",
		}
	case "email":
		set.Steps = []string{
			"Books are mailed to " + d.Email + " from " + sender + ".",
			"If the service only takes mail from known senders, add that address to its allowed list.",
		}
	case "crosspoint":
		set.Steps = []string{
			"On the X4 add an OPDS server (Settings → Connectivity → OPDS servers, or the reader's web page → OPDS): URL " + set.OPDSURL + ", username " + u.Email + " and this device's app password.",
			"Browse it from the reader: \"To send to this device\" holds what you send here, \"Recently added\" the newest books. CrossPoint downloads EPUBs.",
			"For progress sync: Settings → Connectivity → CrossPoint Sync → server URL " + set.KosyncURL + ", the same username and app password, server type KOSync. Document matching by file name (the default) or binary both work.",
			"Optional: give the reader's address here and, while it is on its File Transfer screen on a network this server can reach, books are pushed straight onto the SD card; otherwise they wait in the catalogue.",
		}
	case "koreader":
		set.PluginURL = base + "/koreader/plugin.zip"
		set.Steps = []string{
			"Easiest: download the Pocket Library plugin (" + set.PluginURL + "), unzip it into koreader/plugins/ on the device and restart KOReader. In Tools → Pocket Library enter " + base + ", " + u.Email + " and this device's app password; it sets up the catalogue and progress sync for you.",
			"Without the plugin: Search → OPDS catalog → add " + set.OPDSURL + " with the username and app password.",
			"And Tools → Progress sync → Custom sync server " + set.KosyncURL + ", then log in with the same username and app password.",
			"With the plugin, books you send here download with Tools → Pocket Library → Download sent books (or on their own when Wi-Fi connects), and highlights and reading time come back to the library.",
		}
	}
	return set
}

func (s *Server) handleDeviceSetup(w http.ResponseWriter, r *http.Request) {
	d := s.deviceOf(w, r)
	if d == nil {
		return
	}
	writeJSON(w, http.StatusOK, s.deviceSetup(r.Context(), r, userFrom(r.Context()), d))
}

// ---- test & provisioning ----

func (s *Server) handleDeviceTest(w http.ResponseWriter, r *http.Request) {
	d := s.deviceOf(w, r)
	if d == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	switch d.Kind {
	case "kindle", "email":
		data, err := epub.Minimal("Pocket Library test", "Pocket Library", "en",
			"If you can read this on your "+d.Name+", sending from Pocket Library works.\n\nBooks you send arrive the same way. You can delete this document.")
		if err == nil {
			err = s.mailBook(ctx, d.Email, "Pocket Library test", []pmail.Attachment{{Name: "Pocket Library test.epub", Type: "application/epub+zip", Data: data}})
		}
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "A one-page test document is on its way to " + d.Email + ". It shows up in the library as a document within a few minutes, if the sender address is approved at Amazon."})
	case "crosspoint":
		if d.Address == "" {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "No address: books wait in the reader's catalogue (OPDS → To send to this device)." + seenText(d)})
			return
		}
		c, err := s.devices.crosspoint(d.Address)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		st, err := c.Status(ctx)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "The reader at " + d.Address + " did not answer. It answers only while it is on its File Transfer screen, joined to a network this server can reach."})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": fmt.Sprintf("Reached %s with CrossPoint %s (%s, %s).", nz(st.Device, "the reader"), st.Version, st.Mode, st.IP), "status": st})
	default:
		if d.LastSeenAt == "" {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "KOReader has not signed in yet. Set it up with the steps above."})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "KOReader signed in." + seenText(d)})
	}
}

func seenText(d *store.Device) string {
	if d.LastSeenAt == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339Nano, d.LastSeenAt); err == nil {
		return " Last seen " + t.Local().Format("2 Jan 2006 15:04") + "."
	}
	return ""
}

func nz(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// handleDeviceProvision saves the library as an OPDS catalogue on a
// CrossPoint reader. The device needs the plain app password, which is never
// stored, so a new one is made and handed to the device.
func (s *Server) handleDeviceProvision(w http.ResponseWriter, r *http.Request) {
	d := s.deviceOf(w, r)
	if d == nil {
		return
	}
	if d.Kind != "crosspoint" || d.Address == "" {
		writeErr(w, http.StatusBadRequest, "only a CrossPoint reader with an address can be set up from here")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	c, err := s.devices.crosspoint(d.Address)
	if err == nil {
		_, err = c.Status(ctx)
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, "The reader at "+d.Address+" did not answer: open its File Transfer screen on a network this server can reach and try again.")
		return
	}
	u := userFrom(ctx)
	plain, err := s.newDevicePassword(ctx, u, d)
	if err != nil {
		s.fail(w, err, "app password")
		return
	}
	st, _ := s.store.GetSettings(ctx, 0)
	if err := c.AddOPDS(ctx, nz(st.Title, "Pocket Library"), s.publicBase(r)+"/opds", u.Email, plain); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "password": plain, "username": u.Email,
		"message": "The catalogue is saved on the reader with a new app password (the old one no longer works). For progress sync type the same password into CrossPoint Sync."})
}

// ---- mail ----

// mailAccount is the box's mail account with its password unsealed.
func (s *Server) mailAccount(ctx context.Context) (pmail.Account, error) {
	st, err := s.store.GetSettings(ctx, 0)
	if err != nil {
		return pmail.Account{}, err
	}
	a := pmail.Account{Host: st.SMTPHost, Port: st.SMTPPort, TLS: st.SMTPTLS, User: st.SMTPUser, From: st.SMTPFrom, FromName: st.SMTPFromName}
	if st.SMTPPassword != "" {
		if a.Password, err = s.open(st.SMTPPassword); err != nil {
			return a, errors.New("the mail password cannot be read (was DATA_DIR/secret.key replaced?): enter it again under Settings → Mail")
		}
	}
	if !a.Configured() {
		return a, errors.New("mail is not set up: an admin enters the mail account under Settings → Mail")
	}
	if a.FromName == "" {
		a.FromName = nz(st.Title, "Pocket Library")
	}
	return a, nil
}

// mailBook mails attachments with the box account.
func (s *Server) mailBook(ctx context.Context, to, title string, att []pmail.Attachment) error {
	a, err := s.mailAccount(ctx)
	if err != nil {
		return err
	}
	return s.sendMail(ctx, a, pmail.Message{To: []string{to}, Subject: title,
		Text: "Sent from " + a.FromName + ": " + title + ".\n", Attachments: att})
}

// mailHook replaces delivery in tests (nil = real SMTP).
var mailHook func(ctx context.Context, a pmail.Account, m pmail.Message) error

func (s *Server) sendMail(ctx context.Context, a pmail.Account, m pmail.Message) error {
	if mailHook != nil {
		return mailHook(ctx, a, m)
	}
	return pmail.Send(ctx, a, m)
}

// testMail sends a test message with the box's mail account.
func (s *Server) testMail(ctx context.Context, to string) error {
	a, err := s.mailAccount(ctx)
	if err != nil {
		return err
	}
	return s.sendMail(ctx, a, pmail.Message{To: []string{to}, Subject: "Pocket Library: mail works",
		Text: "This is a test from Pocket Library. Books sent to Kindles and other e-mail devices go out from this account (" + a.From + ").\n\nFor a Kindle, add " + a.From + " to Amazon's Approved Personal Document E-mail List.\n"})
}

// seedOwnerKindle adds the owner's Kindle (KINDLE_EMAIL) as their first device.
func (s *Server) seedOwnerKindle(ctx context.Context, owner int64, email string) error {
	return s.addKindle(ctx, owner, email, true)
}

// addKindle adds a Kindle (or e-mail device) with that address for a person
// unless they have one already.
func (s *Server) addKindle(ctx context.Context, uid int64, email string, makeDefault bool) error {
	kind := "kindle"
	e, err := cleanEmail("kindle", email)
	if err != nil {
		kind = "email"
		if e, err = cleanEmail("email", email); err != nil {
			return err
		}
	}
	if _, err := s.store.DeviceByEmail(ctx, uid, e); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	d := &store.Device{Kind: kind, Name: defaultName(kind), Email: e, IsDefault: makeDefault, Settings: map[string]any{}}
	if err := s.store.CreateDevice(ctx, uid, d); err != nil {
		return err
	}
	s.log.Info("device added", "user", uid, "kind", kind)
	return nil
}

// ---- conversions ----

func (s *Server) handleConvertFormats(w http.ResponseWriter, r *http.Request) {
	c := s.converter()
	out := map[string][]string{}
	for _, from := range []string{"epub", "kepub", "mobi", "azw3", "azw", "prc", "fb2", "fbz", "pdf", "docx", "rtf", "txt", "cbz", "cbr"} {
		var to []string
		for _, t := range convert.Targets {
			if t != from && c.Can(from, t) {
				to = append(to, t)
			}
		}
		if len(to) > 0 {
			out[from] = to
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"calibre": c.EbookConvert != "", "targets": out})
}
