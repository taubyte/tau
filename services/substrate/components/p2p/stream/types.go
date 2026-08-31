package stream

import (
	"context"

	iface "github.com/taubyte/tau/core/services/substrate/components/p2p"
	structureSpec "github.com/taubyte/tau/legacy/specs/structure"
	"github.com/taubyte/tau/p2p/streams/client"
)

var _ iface.Stream = &Stream{}

type Stream struct {
	srv     iface.Service
	config  *structureSpec.Service
	matcher *iface.MatchDefinition
	client  *client.Client

	instanceCtx  context.Context
	instanceCtxC context.CancelFunc
}

func (s *Stream) Close() {
	s.instanceCtxC()
}
