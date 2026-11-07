package instance

import (
	"context"
	"strings"
	"sync"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/discovery"
)

// How one service on this node finds another: p2p discovery by the protocol it
// serves — identify for the peers this node is connected to, routing for the ones it
// has not met. The stats service's usage table is never asked; dns and placement
// read it, nothing that dials does.

// tauProtocols is the prefix every protocol a tau service serves starts with
// (p2p/rpc.Protocol).
const tauProtocols = "/tau/"

// finder is one p2p discovery per protocol, opened on first ask and closed with the
// node.
type finder struct {
	n  *Instance
	mu sync.Mutex
	by map[protocol.ID]*discovery.Discovery
}

func (f *finder) Find(id protocol.ID) discovery.Resolver {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.by[id]; ok {
		return d
	}
	d, err := discovery.New(f.n.Context(), f.n.Host(), id, discovery.WithRouting(f.n.Node.Discovery()))
	if err != nil {
		return refused{err}
	}
	if f.by == nil {
		f.by = map[protocol.ID]*discovery.Discovery{}
	}
	f.by[id] = d
	return d
}

func (f *finder) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.by {
		d.Close()
	}
	f.by = nil
	return nil
}

// refused is a protocol nothing can be found for, which says why whenever asked.
type refused struct{ err error }

func (r refused) Known() []peer.ID                              { return nil }
func (r refused) Any(context.Context) (peer.ID, error)          { return "", r.err }
func (r refused) Peers(context.Context, int) ([]peer.ID, error) { return nil, r.err }
func (refused) Failed(peer.ID)                                  {}

// advertise publishes every tau protocol this node serves, so a node that has never
// connected to it finds it through routing. Connected peers learn the same from
// identify without it.
func (n *Instance) advertise() {
	for _, id := range n.Host().Mux().Protocols() {
		if strings.HasPrefix(string(id), tauProtocols) {
			discovery.Advertise(n.Context(), n.Node.Discovery(), id)
		}
	}
}
