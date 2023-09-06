package substrate

import (
	"context"

	"github.com/taubyte/p2p/streams"
	"github.com/taubyte/p2p/streams/command"
	cr "github.com/taubyte/p2p/streams/command/response"
)

func (s *Service) setupStreamRoutes() {
	s.stream.Router().AddStatic("has", s.hasHandler)
	s.stream.Router().AddStatic("handle_meta", s.handleMetaHandler)
}

func (s *Service) hasHandler(context.Context, streams.Connection, command.Body) (cr.Response, error)

func (s *Service) handleMetaHandler(context.Context, streams.Connection, command.Body) (cr.Response, error)
