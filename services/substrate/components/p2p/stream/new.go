package stream

import (
	"context"

	iface "github.com/taubyte/tau/core/services/substrate/components/p2p"
	structureSpec "github.com/taubyte/tau/legacy/specs/structure"
	"github.com/taubyte/tau/p2p/streams/client"
)

func New(srv iface.Service, ctx context.Context, config *structureSpec.Service, matcher *iface.MatchDefinition, p2pClient *client.Client) (iface.Stream, error) {
	s := &Stream{
		srv:     srv,
		config:  config,
		matcher: matcher,
		client:  p2pClient,
	}
	s.instanceCtx, s.instanceCtxC = context.WithCancel(ctx)

	return s, nil
}
