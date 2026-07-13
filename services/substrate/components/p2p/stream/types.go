package stream

import (
	"context"

	iface "github.com/taubyte/tau/core/services/substrate/components/p2p"
	"github.com/taubyte/tau/p2p/streams/client"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
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
