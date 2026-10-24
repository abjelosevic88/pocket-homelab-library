package server

// The web reader: file contents, comic pages, progress, annotations and
// reading sessions. (placeholder — owned by the reader work)

import (
	"context"

	"github.com/go-chi/chi/v5"
)

type readerState struct{}

func newReaderState() *readerState { return &readerState{} }

func (r *readerState) forget(uid int64) {}

func (s *Server) startReader(ctx context.Context) {}

func (s *Server) routesReader(api chi.Router) {}

func (s *Server) rawReader(raw chi.Router) {}
