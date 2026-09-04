package node

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/ipfs/go-log/v2"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	pkgregister "github.com/taubyte/tau/pkg/register"
	registerService "github.com/taubyte/tau/services/register"
)

// locate.go is how one service on this node finds another.
//
// THERE IS ONE RULE AND IT IS THE SAME EVERYWHERE: if this node runs the
// service, use it directly; otherwise ask stats which node does and dial that
// one. Both halves read the same view, so there is one answer to "who does
// this" and it is a local read rather than a lookup on the request path.

var logger = log.Logger("tau.v2.node")

// rebroadcastSeconds is how often the CRDT under the register re-announces its
// heads. Five is what every other kvdb in this repo uses; differing would be a
// number to explain.
const rebroadcastSeconds = 5

// ErrNoRegister is a node that needs a register and can find none.
var ErrNoRegister = errors.New("node: no register is reachable")

// registerReader is the read surface both halves offer. Publishing is not on
// it: nothing this composition wires is allowed to deploy.
type registerReader interface {
	Resolve(ctx context.Context, project, environment string) (pkgregister.Pointer, error)
	Object(ctx context.Context, id pkgregister.ObjectID) ([]byte, error)
	ResolveHost(ctx context.Context, fqdn string) (project, environment string, err error)
}

// client is the register this node reads from.
//
// A LOCAL REGISTER IS NOT A CACHE, IT IS THE ABSENCE OF A HOP.
func (s *source) client() (registerReader, error) {
	if s.node.register != nil {
		return s.node.register.Local(), nil
	}
	return s.node.remoteRegister()
}

// remoteRegister finds a register in the fleet and keeps the stream.
//
// IT IS LOOKED UP PER CALL AND CACHED BY PEER, not resolved once at
// construction: services start concurrently, a register can go, and which node
// answers is a routing question rather than a wiring one.
func (n *Node) remoteRegister() (registerReader, error) {
	if n.stats == nil {
		return nil, fmt.Errorf("%w: this node runs neither a register nor stats to find one", ErrNoRegister)
	}

	nodes := n.stats.Route(Register, n.cfg.Stats.Fresh)
	if len(nodes) == 0 {
		return nil, ErrNoRegister
	}

	id, err := libp2ppeer.Decode(nodes[0].Peer)
	if err != nil {
		return nil, fmt.Errorf("%w: %q is not a peer", ErrNoRegister, nodes[0].Peer)
	}

	n.registerMu.Lock()
	defer n.registerMu.Unlock()

	if n.registerClient != nil && n.registerAt == id {
		return n.registerClient, nil
	}
	if n.registerClient != nil {
		n.registerClient.Close()
	}

	c, err := registerService.NewClient(n.opts.host, id)
	if err != nil {
		return nil, err
	}
	n.registerClient, n.registerAt = c, id
	return c, nil
}

// closeRegisterClient releases the stream to a remote register, if one was
// opened.
func (n *Node) closeRegisterClient() error {
	n.registerMu.Lock()
	defer n.registerMu.Unlock()

	if n.registerClient != nil {
		n.registerClient.Close()
		n.registerClient = nil
	}
	return nil
}

// closerFunc adapts a plain stop function to io.Closer, for the things this
// package opens that do not have one — a kvdb and a pool both close without
// answering.
type closerFunc func()

func (f closerFunc) Close() error { f(); return nil }

// starter is what Start walks. It is an explicit list rather than a loop over
// an interface slice because a typed nil in an interface is not a nil
// interface, and the loop would call Start on a service this node does not run.
type starter interface {
	Start(context.Context) error
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
