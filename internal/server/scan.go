package server

// Libraries: scanning folders, uploads, the ingest folder, metadata lookups,
// covers and duplicates. (placeholder — owned by the scanner work)

import (
	"context"

	"github.com/go-chi/chi/v5"
)

type scanState struct{}

func newScanState() *scanState { return &scanState{} }

func (s *Server) startScan(ctx context.Context) {}

func (s *Server) routesScan(api chi.Router) {}

func (s *Server) rawScan(raw chi.Router) {}

// scanning reports whether a library is being scanned right now.
func (s *Server) scanning(libraryID int64) bool { return false }

// kickScan asks for a scan of one library (0 = all) soon.
func (s *Server) kickScan(libraryID int64) {}
