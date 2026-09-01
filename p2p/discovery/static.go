package discovery

import (
	"context"
	"fmt"
	"slices"

	"github.com/libp2p/go-libp2p/core/peer"
)

// static is a Resolver over a list that never changes.
type static []peer.ID

// Static resolves to the peers it is given, in the order it is given them.
//
// It is what a service test injects when the question under test is not
// discovery — a handler's authorization branch does not need a swarm — and
// what a deployment uses when the peers are named in configuration rather than
// found. Because nothing about the list can change, it never waits: asking for
// more than it holds returns what it holds, immediately, rather than blocking
// until the caller's deadline the way a live Discovery must.
func Static(peers ...peer.ID) Resolver { return static(slices.Clone(peers)) }

func (s static) Known() []peer.ID { return slices.Clone(s) }

// Failed does nothing here. A list that was handed in cannot be re-derived, so
// dropping a peer from it would leave a Resolver that quietly resolves to less
// than it was configured with — which is worse than handing back a peer whose
// last call failed.
func (s static) Failed(peer.ID) {}

func (s static) Any(ctx context.Context) (peer.ID, error) {
	ids, err := s.Peers(ctx, 1)
	if err != nil {
		return "", err
	}
	return ids[0], nil
}

func (s static) Peers(ctx context.Context, want int) ([]peer.ID, error) {
	if want < 1 {
		return nil, fmt.Errorf("asked for %d peers", want)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoPeers, err)
	}
	if len(s) == 0 {
		return nil, fmt.Errorf("%w: static resolver holds none", ErrNoPeers)
	}
	return slices.Clone(s[:min(want, len(s))]), nil
}
