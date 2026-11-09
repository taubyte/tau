package hosted

import (
	"context"
	"errors"
	"fmt"

	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/pkg/invoke"
	primitivesService "github.com/taubyte/tau/services/primitives"
	"github.com/taubyte/tau/services/worker"
	"github.com/taubyte/tau/services/worker/primitive"
)

// How everything here reaches the primitives service: guest sends and appends, and the
// dispatcher's claims. The adapter is services/worker/primitive.
//
// It is the local service or a node in the fleet, never a local fallback: a queue
// answered either way would be two queues that cannot see each other. Which node is
// asked per call of the finder. A routed node may be a follower that
// refuses the write; the caller retries.

// ErrNoPrimitives is a worker that needs the `sync` kind and has nothing to reach
// it through.
var ErrNoPrimitives = errors.New("worker: no primitives service is reachable")

// openPrimitivesReach wires the send reach into the primitives service.
//
// A LOCAL SERVICE IS THE ABSENCE OF A HOP AND NOT A SHORTCUT: a node holding the
// namespace that dialled itself to reach it would add a stream and a serialization
// to every send.
func (n *Service) openPrimitivesReach() {
	n.sends = service.ReachAs(n.host, primitivesService.Protocol,
		func(s primitivesService.Service) primitive.Seam { return s.Local() },
		func(h host.Host, id libp2ppeer.ID) (primitive.Seam, error) {
			c, err := primitivesService.NewClient(h, id)
			if err != nil {
				return nil, err
			}
			return c, nil
		})
}

// workloads is the worker as the primitives clock fires through it: which crons
// exist, read off the worker, and where a fire runs, through placement.
type workloads struct{ n *Service }

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
func (n *Service) syncBacking() (*primitive.Backing, error) {
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
// dispatch.go's invoke, for its reasons and with its one exception — a
// node with no stats can place only on itself, so it runs what it claimed rather
// than refusing it.
func (n *Service) fire(ctx context.Context, at primitivesService.Target, req *invoke.Request) (*invoke.Response, error) {
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
