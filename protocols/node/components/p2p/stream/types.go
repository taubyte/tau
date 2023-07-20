package stream

import (
	"context"

	iface "github.com/taubyte/go-interfaces/services/substrate/components/p2p"
	structureSpec "github.com/taubyte/go-specs/structure"
)

var _ iface.Stream = &Stream{}

type Stream struct {
	srv     iface.Service
	config  *structureSpec.Service
	matcher *iface.MatchDefinition

	instanceCtx  context.Context
	instanceCtxC context.CancelFunc
}

func (s *Stream) Close() {
	s.instanceCtxC()
}
