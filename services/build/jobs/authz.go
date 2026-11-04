package jobs

import (
	"context"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/p2p/rpc"
)

// caller is the only read of caller identity in this package: rpc.RemotePeer from
// the authenticated stream, never a request field. It fails for a request not
// arriving over p2p/rpc, so these handlers cannot be mounted on an HTTP ingress.
// By default any peer the transport admitted may call (membership is the libp2p
// handshake); AllowPeers narrows ActEnqueue.
func caller(ctx context.Context) (peer.ID, error) {
	id, err := rpc.RemotePeer(ctx)
	if err != nil {
		return "", errUnauthenticated(err)
	}
	return id, nil
}

// allow resolves the caller and asks the injected Authorizer. Every one of the
// five RPCs calls it, first, before anything else reads the request.
func (s *server) allow(ctx context.Context, act Action, subject string) error {
	id, err := caller(ctx)
	if err != nil {
		return err
	}
	if err := s.authz.Allow(ctx, id, act, subject); err != nil {
		return err
	}
	return nil
}
