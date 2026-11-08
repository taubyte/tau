package clients

import (
	"context"
	"fmt"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/discovery"
)

// Discovered is the nodes serving id as h's node finds them, waited for when asked.
func Discovered(h service.Host, id protocol.ID) discovery.Resolver { return h.Find(id) }

// Others is the other nodes serving id, for a node that answers locally first. It
// does not wait for one: the node asking serves id itself, and a cloud with one such
// node has no other to wait for.
func Others(h service.Host, id protocol.ID) discovery.Resolver { return others{h: h, id: id} }

type others struct {
	h  service.Host
	id protocol.ID
}

var _ discovery.Resolver = others{}

func (o others) Known() []peer.ID { return service.Serving(o.h, o.id) }

func (o others) Any(ctx context.Context) (peer.ID, error) {
	peers, err := o.Peers(ctx, 1)
	if err != nil {
		return "", err
	}
	return peers[0], nil
}

func (o others) Peers(_ context.Context, want int) ([]peer.ID, error) {
	known := o.Known()
	if len(known) == 0 {
		return nil, fmt.Errorf("%w: no other node serves %s", discovery.ErrNoPeers, o.id)
	}
	if want > 0 && len(known) > want {
		known = known[:want]
	}
	return known, nil
}

// Failed does nothing: the finder's own resolver is what quarantines a peer that
// failed, and this one asks it afresh every time.
func (others) Failed(peer.ID) {}
