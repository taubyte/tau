package clients

import (
	"context"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/discovery"
)

// lookAgain is how often a lookup waiting for a node looks again. The directory is
// gossiped and changes on its own schedule; there is nothing to subscribe to.
const lookAgain = 250 * time.Millisecond

// directory is a discovery.Resolver over the directory beside a service: the nodes it
// says run one service, asked at call time.
type directory struct {
	h    service.Host
	name string
	// others leaves this node out: a host does not dial itself.
	others bool
}

var _ discovery.Resolver = directory{}

// Discovered is the nodes running name as the directory beside h names them.
func Discovered(h service.Host, name string) discovery.Resolver { return directory{h: h, name: name} }

// Others is Discovered without this node: the others running name, for a node that
// answers locally first. It does not wait for one: the node asking runs name itself,
// and a cloud with one such node has no other to wait for.
func Others(h service.Host, name string) discovery.Resolver {
	return directory{h: h, name: name, others: true}
}

// Known is nil on an instance running no directory: it looked nowhere, which is not
// the same as having found nobody.
func (d directory) Known() []peer.ID {
	dir, ok := service.Get[service.Directory](d.h)
	if !ok {
		return nil
	}
	peers := dir.Peers(d.name)
	if !d.others {
		return peers
	}
	self := d.h.Peer().ID()
	out := make([]peer.ID, 0, len(peers))
	for _, id := range peers {
		if id != self {
			out = append(out, id)
		}
	}
	return out
}

func (d directory) Any(ctx context.Context) (peer.ID, error) {
	peers, err := d.Peers(ctx, 1)
	if err != nil {
		return "", err
	}
	return peers[0], nil
}

func (d directory) Peers(ctx context.Context, want int) ([]peer.ID, error) {
	for {
		if known := d.Known(); len(known) > 0 {
			if want > 0 && len(known) > want {
				known = known[:want]
			}
			return known, nil
		}
		if d.others {
			return nil, fmt.Errorf("%w: no other %s is known", discovery.ErrNoPeers, d.name)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("build: no %s is reachable: %w", d.name, ctx.Err())
		case <-time.After(lookAgain):
		}
	}
}

// Failed does nothing: a failed dial is not evidence the node stopped running the
// service, and the directory already ages out nodes that stopped reporting.
func (directory) Failed(peer.ID) {}
