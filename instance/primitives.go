package instance

import (
	"context"
	"errors"
	"fmt"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/invoke"
	primitivesService "github.com/taubyte/tau/services/primitives"
	"github.com/taubyte/tau/services/worker"
	"github.com/taubyte/tau/services/worker/primitive"
)

// How everything here reaches the primitives service: guest sends and appends, and the
// dispatcher's claims. The adapter is instance/primitive.
//
// It is the local service or a node in the fleet, never a local fallback: a queue
// answered either way would be two queues that cannot see each other. Which node is
// asked per call from the discovery view. A routed node may be a follower that
// refuses the write; the caller retries.

// ErrNoPrimitives is a node that needs the `sync` kind and can reach nothing
// serving it. It is a composition that runs a worker without a primitives
// service and has no view of the fleet to find one in.
var ErrNoPrimitives = errors.New("node: no primitives service is reachable")

// openPrimitivesReach wires the send reach into the primitives service.
func (n *Instance) openPrimitivesReach() {
	pick := n.routeTo(Primitives, ErrNoPrimitives, "this node runs neither one nor discovery to find one")

	// A LOCAL SERVICE IS THE ABSENCE OF A HOP AND NOT A SHORTCUT, which is
	// locate.go's sentence about the register and services/database's about an
	// engine: a node holding the namespace that dialled itself to reach it
	// would add a stream and a serialization to every send.
	n.sends = rpc.Reach(n.Host(), Primitives, pick,
		func() (primitive.Seam, bool) {
			if n.primitives == nil {
				return nil, false
			}
			return n.primitives.Local(), true
		},
		func(id libp2ppeer.ID) (primitive.Seam, error) {
			c, err := primitivesService.NewClient(n.Host(), id)
			if err != nil {
				return nil, err
			}
			return c, nil
		})
}

// workloads is the worker as the primitives clock fires through it: which crons
// exist, read off the worker, and where a fire runs, through placement.
type workloads struct{ n *Instance }

var _ primitivesService.Workloads = workloads{}

func (w workloads) Schedules(ctx context.Context) []primitivesService.Schedule {
	if w.n.worker == nil {
		return nil
	}
	return primitive.Schedules(ctx, w.n.worker)
}

func (w workloads) Fire(ctx context.Context, at primitivesService.Target, req *invoke.Request) (*invoke.Response, error) {
	return w.n.fire(ctx, at, req)
}

// syncBacking is this node's one reach into the primitives service, built during
// construction only: once Start spawns the dispatcher the field is read concurrently.
func (n *Instance) syncBacking() (*primitive.Backing, error) {
	if n.syncs == nil {
		backing, err := primitive.New(n.sends)
		if err != nil {
			return nil, err
		}
		n.syncs = backing
	}
	return n.syncs, nil
}

// fire runs what a claimed fire names, wherever it should run.
//
// THROUGH PLACEMENT, AND THAT IS WHERE THE WORK SPREADS. A claim is
// leader-only, so every fire in the cloud is handed out by one node; a firer
// that also RAN what it claimed would leave the rest of the fleet idle. This is
// instance/dispatch.go's invoke, for its reasons and with its one exception — a
// node with no discovery can only reach itself, so it runs what it claimed rather
// than refusing it.
func (n *Instance) fire(ctx context.Context, at primitivesService.Target, req *invoke.Request) (*invoke.Response, error) {
	target := worker.Target{
		Project:     at.Project,
		Environment: at.Environment,
		Scope:       at.Scope,
		Kind:        at.Kind,
		Name:        at.Name,
	}
	if n.worker == nil {
		return nil, fmt.Errorf("%w: this node fires for a worker it does not run", ErrNoPrimitives)
	}
	if n.workers == nil {
		return n.worker.Invoke(ctx, target, req)
	}
	return n.workers.Invoke(ctx, target, req)
}
