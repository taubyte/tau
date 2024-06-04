package stream

import (
	"context"

	iface "github.com/taubyte/tau/core/services/substrate/components/p2p"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
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
