package node

import (
	"context"
	"errors"
	"fmt"
	"time"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/node/primitive"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/invoke"
	primitivesService "github.com/taubyte/tau/services/primitives"
	"github.com/taubyte/tau/services/worker"
)

// primitives.go starts this node's primitives service, and decides how
// everything here reaches one: the queues a guest sends to, the logs it appends
// to, the claim half the dispatcher drives, and the window a leader hands out.
//
// The adapter every one of those callers holds is node/primitive. What is left
// here is the part only the root can do — name the machine's resources, name
// which arm answers, and say where a minute goes.
//
// # The service owns the namespace, and the node owns the machine
//
// services/primitives holds the raft namespace a project's queues run in, the
// store its logs' records live in, the declarations both are configured from
// and the lifecycle pass that moves them along. What is left here is what a
// composition root has: a host, a way to open a group, a store factory, and the
// decision about which of the two arms below a caller gets.
//
// # It is the local service or it is a node in the fleet, and never a third
// thing
//
// node/database.go's kvBacking falls back to this node's own engines when it
// runs no database service, and that is benign there because a kv is node-local
// either way. A QUEUE IS NOT. A node that fell back to a group of its own would
// send into one namespace while a node with the service sent into another, and
// the dispatcher on the first would drain a group that will never see the
// second's items — one declared queue silently becoming two queues that cannot
// see each other, with nothing red anywhere. That is what the worker's own raft
// group was, and it is gone. So there is no local fallback: a node that runs
// the service uses it with no hop, and a node that does not dials one that
// does.
//
// WHICH NODE IS A ROUTING QUESTION AND NOT A WIRING ONE, so it is asked per
// call of the same discovery view every other locator here reads, and the stream is
// cached by peer. Neither half is written here any more: p2p/rpc holds the
// rule and node/locate.go holds the view, and what this file states is the one
// thing that is the `sync` kind's own — that there are TWO reaches, because a
// send prefers this node and a window must be free to leave it.
//
// WHAT IS NOT CLOSED YET: the routed node may be a FOLLOWER of the namespace,
// which refuses the write and names the leader in its sentence. The caller
// retries and the next placement may land elsewhere; what would remove the
// retry is a route that knows about leadership, and that is a routing change
// rather than one here.
//
// THE CLOCK'S TASKING IS THE ONE CALL THAT ALREADY DOES THAT, and the asymmetry
// is deliberate rather than an oversight to copy. A window is ONE message a
// minute with no caller waiting on it, so the service sends it on to the node
// that may write; a guest's send is on the request path and per call, where a
// hop the caller did not ask for is latency it cannot see or retry around.

// ErrNoPrimitives is a node that needs the `sync` kind and can reach nothing
// serving it. It is a composition that runs a worker without a primitives
// service and has no view of the fleet to find one in.
var ErrNoPrimitives = errors.New("node: no primitives service is reachable")

// openPrimitivesReach wires the two reaches into the primitives service. They
// are two because the CLOCK'S POLICY IS NOT THE SEND'S — see [Node.clockNow] —
// and the pair costs nothing: a node that runs the service never dials for a
// send, and a node that does not never dials for a clock, because it refuses
// the window before asking. So at most one of the two ever holds a stream.
func (n *Node) openPrimitivesReach() {
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

// taskCron hands one window of the clock to a node running the primitives
// service: "the window `(since, upto]` is yours — generate and distribute what
// it owes."
//
// IT IS DELIBERATELY NOT THE SEND PATH'S REACH. That one prefers this node's
// own service because a send has no reason to leave; a tasking does — the whole
// point of the leader's minute being a MESSAGE is that the walk happens
// somewhere it chose. So the node comes from the same discovery view every other
// locator here reads, and it is this one only when the view says so.
//
// AND THE SAME-NODE CASE PAYS NO ROUND TRIP, which is the send path's rule read
// once more: a node that holds the namespace and dialled itself to reach it
// would add a stream and a serialization to a call that is already in the right
// process.
func (n *Node) taskCron(ctx context.Context, since, upto time.Time) error {
	clock, err := n.clockNow(ctx)
	if err != nil {
		return err
	}
	return clock.CronTask(ctx, since, upto)
}

// clockNow is the node this minute's window goes to.
//
// IT IS ROUTED AND NOT LOCAL-FIRST. [rpc.Reach] — what the send path takes —
// would keep every window on the node that handed it out, and the whole point
// of the leader's minute being a MESSAGE is that the walk happens somewhere it
// chose. [rpc.ReachRouted] is that policy, and its answer for an empty route is
// the one this call needs: a node with no view of a fleet is the whole fleet,
// so "nobody is visible" means "nobody but me" rather than "nowhere to send
// this", and refusing would stop every cron in a single-node cloud.
//
// The refusal below stays at the call site because it is a fact about this
// COMPOSITION rather than about the route: a node that hands out minutes and
// runs no service to take one has nothing to fall back to, and it says which of
// the two it is missing.
func (n *Node) clockNow(ctx context.Context) (primitivesService.Clocks, error) {
	if n.primitives == nil {
		return nil, fmt.Errorf("%w: this node hands out minutes and runs no service to take one", ErrNoPrimitives)
	}
	return n.clocks.Now(ctx)
}

// openPrimitives starts this node's primitives service, when it runs one.
//
// THE MACHINE'S RESOURCES ARE THIS NODE'S AND THE DECLARATIONS ARE THE
// REGISTER'S, which is the split the service was built for: it serves what a
// project declared, and it reads what a project declared rather than being
// told. Every one of the three things it is given here is a value openDatabase
// and openWorker already draw from the same place.
//
// THE GROUP IS OPENED ON START AND NOT HERE, by the service itself and in the
// background — raft.New blocks for up to its bootstrap timeout looking for
// peers, which is right at boot and wrong at construction, where the swarm is
// not up yet.
func (n *Node) openPrimitives() error {
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

	// AND THE CLOCK, on a node that also runs a worker. The service owns the
	// namespace a slot is claimed in and the queue a fire travels through; what
	// it cannot know is which crons exist — that is a walk of the deployments a
	// worker is already serving — or how to reach a workload. Both are read
	// through the field rather than captured, because openWorker has not run
	// yet: this is the same lazy shape n.registerSource() uses for a register
	// that may appear later.
	//
	// AND WHERE A MINUTE GOES, which is this node's answer and not the
	// service's: the leader hands one window a minute to a node running the
	// service, and which node that is comes from the same discovery view every
	// other locator here reads. See [Node.taskCron].
	//
	// WHAT IS STILL NOT CLOSED, stated where it is paid: the tasking now leaves
	// the leader, but a fire is a WRITE — pushing an item and claiming it back
	// are both refused off the namespace's leader and not forwarded — so a
	// tasked node that does not lead sends the window on to the one that does,
	// and the walk ends up there. A composition of Primitives without Worker is
	// the designed remote arm, and a node shaped that way that also LEADS has
	// no deployment view to enumerate crons from: it refuses the window by name
	// (ErrNoClock) rather than generating nothing, and every cron in the cloud
	// waits for an election. Closing that needs a follower that can commit,
	// which is pkg/raft's to give. See services/primitives/cron.go.
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

// syncBacking is this node's one reach into the primitives service, built on
// first use by the wasm tier that sends and by the dispatcher that claims.
//
// CONSTRUCTION ONLY, like busStreams. It writes a field, and once Start has
// spawned the dispatcher that field is being read from another goroutine — so
// openWorker asks for it whether or not this node has a wasm tier, and nothing
// calls it afterwards.
//
// THE REACH IS WHAT IT IS HANDED, and that reach is this node's answer to which
// arm serves the `sync` kind — see [Node.openPrimitivesReach]. The adapter never
// learns which one it got.
func (n *Node) syncBacking() (*primitive.Backing, error) {
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
// node/dispatch.go's invoke, for its reasons and with its one exception — a
// node with no discovery can only reach itself, so it runs what it claimed rather
// than refusing it.
func (n *Node) fire(ctx context.Context, at primitivesService.Target, req *invoke.Request) (*invoke.Response, error) {
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
