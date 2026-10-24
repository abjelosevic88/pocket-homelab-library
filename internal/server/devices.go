package server

// Devices, Send to Kindle / to a device, downloads, conversions and imports.
// (placeholder — owned by the devices work)

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-library/internal/store"
)

type deviceState struct{}

func newDeviceState() *deviceState { return &deviceState{} }

func (s *Server) startDevices(ctx context.Context) {}

func (s *Server) routesDevices(api chi.Router) {}

func (s *Server) rawDevices(raw chi.Router) {}

// seedOwnerKindle adds the owner's Kindle (KINDLE_EMAIL) as their first device.
func (s *Server) seedOwnerKindle(ctx context.Context, owner int64, email string) error { return nil }

// testMail sends a test message with the box's mail account.
func (s *Server) testMail(ctx context.Context, to string) error { return errors.New("not built yet") }

// serveBookFile sends a book's file to a browser, e-reader or the KOReader
// plugin: the original with HTTP ranges, or converted to format ("" = as
// is), with the metadata edited here written into EPUBs when the box says so.
// The download is named "Author - Title.ext".
func (s *Server) serveBookFile(w http.ResponseWriter, r *http.Request, u *store.User, b *store.Book, f *store.File, format string) {
	http.ServeFile(w, r, f.Path)
}
