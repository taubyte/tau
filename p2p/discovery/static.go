package discovery

import (
	"context"
	"fmt"
	"slices"

	"github.com/libp2p/go-libp2p/core/peer"
)

// static is a Resolver over a list that never changes.
type static []peer.ID

// Static resolves to the peers it is given, in order: for a service test whose
// subject is not discovery, or peers named in configuration. It never waits; asking
// for more than it holds returns what it holds.
func Static(peers ...peer.ID) Resolver { return static(slices.Clone(peers)) }

func (s static) Known() []peer.ID { return slices.Clone(s) }

// Failed does nothing: dropping a configured peer would quietly resolve to less than
// configured.
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
