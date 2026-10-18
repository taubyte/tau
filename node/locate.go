package node

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/ipfs/go-log/v2"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/p2p/rpc"
	pkgregister "github.com/taubyte/tau/pkg/register"
	pkgstats "github.com/taubyte/tau/pkg/stats"
	registerService "github.com/taubyte/tau/services/register"
)

// locate.go is how one service on this node finds another.
//
// THERE IS ONE RULE AND IT IS THE SAME EVERYWHERE: if this node runs the
// service, use it directly; otherwise ask discovery which node does and dial that
// one. Both halves read the same view, so there is one answer to "who does
// this" and it is a local read rather than a lookup on the request path.
//
// THE RULE ITSELF LIVES IN p2p/rpc, as [rpc.Reach]: it is a statement about
// when a stream is established, so it belongs where streams are established,
// and it was written five times here before it lived anywhere. What is left in
// this package is the half that is tau's — WHO to call — because routing by
// load and freshness is the discovery view's business and a transport package that
// imported a service to answer it would have the dependency backwards. See
// [Node.routeTo].

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

// registerSeam is the register as the REACH onto it hands it back: this node's
// own when it runs one, a stream onto the node discovery names when it does not.
//
// IT IS THE UNION OF WHAT ITS THREE CALLERS NEED, AND IT IS NOT WHAT ANY OF
// THEM IS HANDED. registerReader, artifact.Register and deploy.Register are
// each narrower and each is narrowed FROM this — a deploy is the only thing in
// this composition allowed to publish, and it is the only one whose seam
// carries Publish. It is exactly as wide as the concrete client the reach used
// to hand back, so nothing above this line can reach further than it could.
type registerSeam interface {
	registerReader
	Environments(ctx context.Context, project string) ([]string, error)
	Publish(ctx context.Context, p pkgregister.Publication) (pkgregister.ObjectID, error)
}

// routeTo is the `pick` half of every reach on this node: WHICH node in the
// fleet runs `service`, LEAST LOADED FIRST — which is the view's own order and
// not a choice made at a call site.
//
// THE TOLERANCE IS THE STATS CONFIG'S EFFECTIVE ONE, not its raw field.
// Reading Config.Fresh directly gave zero whenever the operator left it unset
// — every row is older than nothing — so every node in the fleet looked like no
// node at all. It was written out once per service before this, which is four
// chances to make that mistake again.
//
// WHAT AN EMPTY ROUTE MEANS IS NOT DECIDED HERE. This says "nobody", as
// `absent`; whether nobody means a refusal or means this node is the reach's,
// and the two answers in this package are [rpc.Reach]'s and [rpc.ReachRouted]'s.
//
// `blind` is what a node with no view of a fleet at all is told, which is a
// different sentence from an empty view: it is a composition that forgot discovery
// rather than a fleet that has nothing.
func (n *Node) routeTo(service string, absent error, blind string) func() (libp2ppeer.ID, error) {
	return func() (libp2ppeer.ID, error) {
		if n.discovery == nil {
			return "", fmt.Errorf("%w: %s", absent, blind)
		}

		nodes := n.rowsFor(service)
		if len(nodes) == 0 {
			return "", absent
		}

		id, err := libp2ppeer.Decode(nodes[0].Peer)
		if err != nil {
			return "", fmt.Errorf("%w: %q is not a peer", absent, nodes[0].Peer)
		}
		return id, nil
	}
}

// rowsFor is every node the view says runs `service`, in the view's own order.
//
// IT IS THE ONE PLACE THE TOLERANCE IS READ, and that is the whole of why it
// exists rather than being two Route calls: build.go's pool resolver wants ALL
// the rows and a reach wants the first, but both want them judged by the same
// number, and the one thing that has actually gone wrong here is the number.
//
// WHAT A ROW THAT DOES NOT DECODE MEANS IS THE CALLER'S, and the two callers
// answer differently on purpose — see [Node.routeTo] and fromDiscovery.Known.
func (n *Node) rowsFor(service string) []pkgstats.Node {
	if n.discovery == nil {
		return nil
	}
	return n.discovery.Route(service, n.cfg.Discovery.Freshness())
}

// openReach wires every locator this node has, BEFORE anything that uses one.
//
// They are built here and not lazily because each is a value other objects are
// handed; what they read is lazy instead — every one of the three closures
// below reads a field of the node at CALL time, so a service opened after this
// is still found by a reach built before it.
func (n *Node) openReach() {
	n.registers = rpc.Reach(n.opts.host, Register,
		n.routeTo(Register, ErrNoRegister, "this node runs neither a register nor discovery to find one"),
		func() (registerSeam, bool) {
			// A LOCAL REGISTER IS NOT A CACHE, IT IS THE ABSENCE OF A HOP.
			if n.register == nil {
				return nil, false
			}
			return n.register.Local(), true
		},
		func(id libp2ppeer.ID) (registerSeam, error) {
			c, err := registerService.NewClient(n.opts.host, id)
			if err != nil {
				return nil, err
			}
			return c, nil
		})

	n.openPrimitivesReach()
}

// closeReach releases every stream the locators hold. A local service is never
// closed here — it belongs to the node that runs it, and is in n.closers.
//
// It is tolerant of a half-built node because New closes one: a construction
// that failed before openReach ran has nothing to give back.
func (n *Node) closeReach() {
	if n.registers != nil {
		n.registers.Close()
	}
	if n.sends != nil {
		n.sends.Close()
	}
	if n.clocks != nil {
		n.clocks.Close()
	}
}

// client is the register this node reads from.
func (s *source) client(ctx context.Context) (registerReader, error) {
	return s.node.registers.Now(ctx)
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
