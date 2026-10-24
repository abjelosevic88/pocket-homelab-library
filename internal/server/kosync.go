package server

// E-readers at the root: OPDS, KOReader sync, the KOReader plugin API and
// CrossPoint. (placeholder — owned by the sync work)

import (
	"context"

	"github.com/go-chi/chi/v5"
)

type kosyncState struct{}

func newKosyncState() *kosyncState { return &kosyncState{} }

func (s *Server) startKosync(ctx context.Context) {}

func (s *Server) routesSync(r chi.Router) {}
