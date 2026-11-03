package instance

import (
	"context"
	"errors"
	"fmt"
	"time"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/instance/primitive"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/invoke"
	primitivesService "github.com/taubyte/tau/services/primitives"
	"github.com/taubyte/tau/services/worker"
)

// Starts the primitives service and decides how everything here reaches one: guest
// sends and appends, the dispatcher's claims, and the leader's minute. The adapter is
// instance/primitive.
//
// It is the local service or a node in the fleet, never a local fallback: a queue
// answered either way would be two queues that cannot see each other. Which node is
// asked per call from the discovery view. There are two reaches because a send
// prefers this node and a window must be free to leave it. A routed node may be a
// follower that refuses the write; the caller retries.

// ErrNoPrimitives is a node that needs the `sync` kind and can reach nothing
// serving it. It is a composition that runs a worker without a primitives
// service and has no view of the fleet to find one in.
var ErrNoPrimitives = errors.New("node: no primitives service is reachable")

// openPrimitivesReach wires the two reaches into the primitives service. They
// are two because the CLOCK'S POLICY IS NOT THE SEND'S — see [Node.clockNow] —
// and the pair costs nothing: a node that runs the service never dials for a
// send, and a node that does not never dials for a clock, because it refuses
// the window before asking. So at most one of the two ever holds a stream.
func (n *Instance) openPrimitivesReach() {
	pick := n.routeTo(Primitives, ErrNoPrimitives, "this node runs neither one nor discovery to find one")

	// A LOCAL SERVICE IS THE ABSENCE OF A HOP AND NOT A SHORTCUT, which is
	// locate.go's sentence about the register and services/database's about an
	// engine: a node holding the namespace that dialled itself to reach it
	// would add a stream and a serialization to every send.
	n.sends = rpc.Reach(n.opts.host, Primitives, pick,
		func() (primitive.Seam, bool) {
			if n.primitives == nil {
				return nil, false
			}
			return n.primitives.Local(), true
		},
		func(id libp2ppeer.ID) (primitive.Seam, error) {
			c, err := primitivesService.NewClient(n.opts.host, id)
			if err != nil {
				return nil, err
			}
			return c, nil
		})

	n.clocks = rpc.ReachRouted(n.opts.host, Primitives, pick,
		func() (primitivesService.Clocks, bool) {
			if n.primitives == nil {
				return nil, false
			}
			return n.primitives.Local(), true
		},
		func(id libp2ppeer.ID) (primitivesService.Clocks, error) {
			c, err := primitivesService.NewClient(n.opts.host, id)
			if err != nil {
				return nil, err
			}
			return c, nil
		})
}

// taskCron hands one window of the clock to a node running the primitives service,
// chosen from the discovery view rather than preferring this node. The same-node case
// pays no round trip.
func (n *Instance) taskCron(ctx context.Context, since, upto time.Time) error {
	clock, err := n.clockNow(ctx)
	if err != nil {
		return err
	}
	return clock.CronTask(ctx, since, upto)
}

// clockNow is the node this minute's window goes to, through rpc.ReachRouted: routed,
// not local-first, and an empty route means this node, so a single-node cloud keeps
// firing. A node with no service to take the window refuses here.
func (n *Instance) clockNow(ctx context.Context) (primitivesService.Clocks, error) {
	if n.primitives == nil {
		return nil, fmt.Errorf("%w: this node hands out minutes and runs no service to take one", ErrNoPrimitives)
	}
	return n.clocks.Now(ctx)
}

// openPrimitives starts this node's primitives service: the machine's resources are
// the node's, the declarations the register's. The group opens on Start in the
// background.
func (n *Instance) openPrimitives() error {
	if !n.cfg.runs(Primitives) {
		return nil
	}
	// BOTH HALVES, AND REFUSED AT CONSTRUCTION RATHER THAN PER CALL: a queue's
	// items and a log's head are ordered by a raft group, and a log's records
	// live in the replicated store beside every other kind's bytes. A node
	// missing either can serve neither of this kind's two types.
	if n.opts.raft == nil {
		return fmt.Errorf("node: %s: %w", Primitives, ErrNoRaft)
	}
	if n.opts.databases == nil {
		return fmt.Errorf("node: %s: %w", Primitives, ErrNoCacheStore)
	}

	options := []primitivesService.Option{
		primitivesService.WithRegister(n.registerSource()),
		primitivesService.WithConsensus(n.opts.raft),
		primitivesService.WithRecords(n.opts.databases),
	}

	// And the clock, on a node that also runs a worker: which crons exist and how to
	// reach a workload are read through fields, since openWorker has not run yet. Where a
	// minute goes is Node.taskCron. A fire is a write, so a tasked follower forwards the
	// window; a leader running no worker refuses it (ErrNoClock) until another election.
	if n.cfg.runs(Worker) {
		options = append(options,
			// THE NIL CHECK IS THAT FIELD READ, and it is made here where the
			// type is still concrete: a nil *worker.Service handed over as an
			// interface is not a nil interface, so the adapter could not make
			// it on the other side of the call.
			primitivesService.WithSchedules(func(ctx context.Context) []primitivesService.Schedule {
				if n.worker == nil {
					return nil
				}
				return primitive.Schedules(ctx, n.worker)
			}),
			primitivesService.WithFire(n.fire),
			primitivesService.WithTasking(n.taskCron))
	}
	// THE NODE'S CLOCK, when it was given one. Everything the lifecycle pass
	// decides — what retention has outlived, what a claim has outlasted — is
	// decided against it, so a universe driving the clock has to drive this
	// one too.
	if n.opts.now != nil {
		options = append(options, primitivesService.WithClock(n.opts.now))
	}

	svc, err := primitivesService.New(n.opts.host, options...)
	if err != nil {
		return err
	}

	n.primitives = svc
	n.closers = append(n.closers, svc)
	return nil
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
