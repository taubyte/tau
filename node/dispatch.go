package node

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	primitivesService "github.com/taubyte/tau/services/primitives"
	"github.com/taubyte/tau/services/worker"
)

// dispatch.go is the other half of a queue: something has to CLAIM.
//
// `send` is a host function a guest calls and `receive` is a delivery — the
// platform takes an item and invokes a workload with it — so the second verb
// was never going to be an ABI. This is it.
//
// # It is here rather than in the worker
//
// The worker knows WHICH workloads consume (services/worker/consume.go) and the
// node holds the queues, so this is the one place both are in scope — the same
// reason every other backing is composed here.
//
// # step is one cycle and loop is a ticker
//
// The split is services/build/backend's, for its reason: a claim loop reachable
// only through a real clock has branches nothing can test, and the branches
// that matter here are the ones a test has to be able to drive — a follower
// that claims nothing, a workload that fails, an item that exhausts its
// attempts.
//
// # A follower does nothing, and no election decides that
//
// A claim is a write and pkg/queue refuses a write off the raft leader. So
// every node serving a deployment runs this loop and all but one get
// ErrNotLeader and return. Agreeing on a consumer would be a second election
// answering a question raft already answers.
//
// # It claims through the primitives service and holds no queue of its own
//
// The items are in that service's namespace, and what this loop holds is the
// seam onto it: Claim, Settle and nothing else. So the config the queue runs
// under is the service's read of the DECLARATION rather than anything this
// node describes, and the pass that buries an exhausted item nobody is claiming
// is the service's lifecycle rather than a Sweep taken here before every cycle
// — which this used to do, and which pkg/queue already runs at the top of every
// Claim anyway.

// dispatcher drains the queues of the deployments this node serves.
type dispatcher struct {
	node *Node

	// perCycle bounds how many items one delivery goroutine takes in a cycle,
	// so a deep queue cannot hold the tick while the others wait.
	perCycle int
}

const (
	// claimEvery is how often a node looks for work.
	//
	// IT IS A POLL AND THAT IS THE CEILING. pkg/queue is a replicated store
	// with no notification, so an item pushed just after a cycle waits up to
	// this long — which is what "a global FIFO with a visibility timeout"
	// costs and is the same interval services/build runs at.
	claimEvery = 2 * time.Second

	// perCycle is how many items one goroutine takes before yielding.
	perCycle = 32
)

// deliveryHeaders are what a consumer is told about the item it was handed.
//
// A WORKLOAD CONSUMING TWO QUEUES HAS TO KNOW WHICH ONE THIS IS, and a WASM
// consumer has no path to tell them apart by — it is dispatched at "/" unless
// it declared otherwise. The queue's LABEL is what is sent, because that is
// what the author wrote and what the same workload passes to `send`; the handle
// is the platform's and means nothing to a guest.
const (
	headerQueue    = "X-Taubyte-Queue"
	headerDelivery = "X-Taubyte-Delivery"
	headerAttempt  = "X-Taubyte-Attempt"
)

// step performs exactly ONE cycle and reports how many items it delivered.
func (d *dispatcher) step(ctx context.Context) int {
	if d.node.worker == nil || d.node.syncs == nil {
		return 0
	}
	work := d.node.syncs

	var delivered atomic.Int64
	var wg sync.WaitGroup

	for _, consumer := range d.node.worker.Consumers(ctx) {
		if consumer.Queue.Refused != "" {
			// The grant says this cloud will not serve the target — an engine
			// it has no driver for, a store of many instances nobody admits.
			// Draining it would be serving what the resolve refused.
			continue
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			delivered.Add(int64(d.drain(ctx, consumer, work)))
		}()
	}

	wg.Wait()
	return int(delivered.Load())
}

// ordinaryRefusal reports whether a refused claim is one NOT worth a line every
// two seconds.
//
// A FOLLOWER CLAIMS NOTHING BY DESIGN, which is this file's whole point, and a
// group still forming or a fleet with no service in it yet clears on its own
// with the next cycle as the retry. Saying any of it twice a second would bury
// the refusals that do not clear.
//
// IT IS MATCHED ON BOTH ARMS OR IT IS MATCHED ON NEITHER, and which names
// travel is services/primitives/status.go's table to say rather than this
// file's to guess. A status that exactly one refusal claims comes back AS that
// refusal — ErrNotLeading does — and a status several share carries no name at
// all, which is the case for ErrForming and ErrClosed: both are Unavailable,
// so the code is what is left of them. So this matches the TIER's own arm for a
// node that may not write, the two names, and the code — and that service's
// own property test is what keeps the three answers the same on both arms.
func ordinaryRefusal(err error) bool {
	switch {
	case errors.Is(err, ErrNoPrimitives),
		errors.Is(err, wasmtier.ErrNotWritable),
		errors.Is(err, primitivesService.ErrForming),
		errors.Is(err, primitivesService.ErrClosed):
		return true
	default:
		// UNAVAILABLE IS THE ANSWER FOR BOTH OF THE LAST TWO over the wire, and
		// for a node that is shutting down — every one of them a caller acts on
		// by asking again somewhere.
		return connect.CodeOf(err) == connect.CodeUnavailable
	}
}

// drain takes items from one queue until it is empty.
//
// AS MANY AT ONCE AS THE WORKLOAD SAID IT MAY HOLD. `limits/concurrency` is the
// vocabulary's own number for exactly this, and the author who wrote it is
// describing a consumer the platform pushes to — a dispatcher that ignored it
// would deliver one item at a time to a workload that declared it can take
// eighty.
func (d *dispatcher) drain(ctx context.Context, consumer worker.Consumer, work syncSeam) int {
	holding := max(consumer.Concurrency, 1)

	take := d.perCycle
	if take <= 0 {
		take = perCycle
	}

	var delivered atomic.Int64
	var wg sync.WaitGroup
	for range holding {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range take {
				if ctx.Err() != nil {
					return
				}
				// ONE AT A TIME, THOUGH THE SEAM TAKES A BATCH. A claim starts
				// the item's visibility timer, so asking for thirty-two and
				// then delivering them one after another would run the last
				// one's timer out while the first were still being invoked —
				// the item would be handed to somebody else and the ack
				// refused. What a batch would save is round trips on a node
				// that reaches the service over the swarm; what it would cost
				// is a duplicate delivery, so it is not taken.
				claimed, err := work.Claim(ctx, consumer.Queue, 1)
				if err != nil {
					if !ordinaryRefusal(err) {
						logger.Warnf("dispatch: claiming %q for %s: %s",
							consumer.Queue.Label, consumer.Target.Name, err)
					}
					return
				}
				if len(claimed) == 0 {
					// NOTHING DELIVERABLE IS NOT AN EMPTY QUEUE and it is not a
					// refusal either: items may be delayed, backing off, or
					// held by somebody else. The next cycle is the retry.
					return
				}
				d.deliver(ctx, consumer, work, claimed[0])
				delivered.Add(1)
			}
		}()
	}
	wg.Wait()
	return int(delivered.Load())
}

// invoke runs a workload wherever it should run.
//
// THROUGH PLACEMENT, AND THAT IS THE DIFFERENCE between a queue that is
// replicated and work that is distributed. A claim is leader-only — pkg/queue
// refuses a write off the leader, which is what makes a claim exclusive without
// a second election — so a dispatcher that also RAN what it claimed put every
// step of every workflow in the cloud on one node, with the rest of the fleet
// idle. The queue was distributed; the work was not.
//
// It was the only path that did this. A request arriving at a gateway, a
// connection at a proxy, a dial on a channel and a local model's socket are all
// placed already; a delivery was the one kind of work that stayed where it was
// claimed.
//
// A NODE WITH NO STATS CAN ONLY REACH ITSELF, which openPlacement says in as
// many words, so that one still runs what it holds rather than refusing it.
// Placement keeps the local worker as a candidate, so a single node is not a
// round trip to itself.
func (d *dispatcher) invoke(ctx context.Context, at worker.Target, req *invoke.Request) (*invoke.Response, error) {
	if d.node.workers == nil {
		return d.node.worker.Invoke(ctx, at, req)
	}
	return d.node.workers.Invoke(ctx, at, req)
}

// deliver hands one item to the workload and settles the claim on what came
// back.
//
// 2xx IS AN ACKNOWLEDGEMENT and everything else is a failure, which is the
// contract every push consumer in this market already speaks.
//
// # A failure is not always a nack, and that is the vocabulary's own sentence
//
// `retry.interval` is optional and its doc says what an absent one means: "the
// visibility timeout alone governs". A nack gives the item back NOW, so with no
// interval declared it lands ready immediately — and this loop drains until the
// queue is empty, so a consumer failing in a millisecond burns every attempt
// the author allowed in a millisecond and the item is in the dead letter before
// anybody could have fixed anything.
//
// So a failed delivery is nacked only when there IS an interval to space the
// retry by. Without one the claim is simply left to expire, which is the
// visibility timeout governing exactly as written — the attempt is already
// counted (Claim increments it) and pkg/queue's own sweep requeues it or buries
// it when the deadline passes.
func (d *dispatcher) deliver(ctx context.Context, consumer worker.Consumer, work syncSeam, item primitivesService.Delivery) {
	// THE CLAIM'S DEADLINE BOUNDS THE INVOCATION. Past it the item is
	// deliverable again, so a workload still running is a workload whose work
	// somebody else has already been handed — and the ack would be refused
	// anyway.
	//
	// AS A DURATION AND NOT AS AN INSTANT. The deadline is stamped on the
	// QUEUE'S clock, which this node injects — a test drives it and a dream
	// universe drives it — and context.WithDeadline compares against the wall
	// clock. Where the two read differently, handing it the instant either
	// cancels every delivery before it starts or gives it an unbounded one.
	// What the invocation is owed is the time left on the claim, measured on
	// the clock the claim was stamped by.
	budget := item.Deadline.Sub(d.node.clock())
	if budget <= 0 {
		// The claim expired between being handed out and reaching here.
		// Somebody else holds it or is about to.
		return
	}
	runCtx, stop := context.WithTimeout(ctx, budget)
	defer stop()

	// A BUILTIN IS RUN HERE AND NOT ON A TIER. `source: {kind: builtin}` names a
	// program the runtime ships, so there is no artifact to start and the
	// delivery that would have started one is this delivery. See step.go.
	if consumer.Builtin == stepBuiltin {
		if err := d.runStep(runCtx, consumer, item); err != nil {
			logger.Warnf("dispatch: %s on %s/%s: %s",
				consumer.Target.Name, consumer.Queue.Label, item.ID, err)
			d.failed(ctx, consumer, work, item)
			return
		}
		d.acknowledge(ctx, consumer, work, item)
		return
	}

	res, err := d.invoke(runCtx, consumer.Target, &invoke.Request{
		Method: http.MethodPost,
		Path:   consumer.Path,
		Headers: map[string][]string{
			headerQueue:    {consumer.Queue.Label},
			headerDelivery: {item.ID},
			headerAttempt:  {strconv.FormatUint(uint64(item.Attempt), 10)},
		},
		Body: item.Data,
	})

	switch {
	case err != nil:
		logger.Warnf("dispatch: %s failed on %s/%s: %s",
			consumer.Target.Name, consumer.Queue.Label, item.ID, err)
		d.failed(ctx, consumer, work, item)
	case res.Status < 200 || res.Status > 299:
		logger.Warnf("dispatch: %s answered %d for %s/%s",
			consumer.Target.Name, res.Status, consumer.Queue.Label, item.ID)
		d.failed(ctx, consumer, work, item)
	default:
		d.acknowledge(ctx, consumer, work, item)
	}
}

// acknowledge ends a delivery that succeeded.
//
// A REFUSED ACK IS WORTH A LINE, which it was not when the queue was in this
// process and an Ack was a local write. It is now a call that can be refused by
// a node that is no longer the leader or whose claim has already expired, and
// what follows is a SECOND delivery of work that has already been done — so
// somebody looking at a duplicate has the one line that says why.
func (d *dispatcher) acknowledge(ctx context.Context, consumer worker.Consumer, work syncSeam, item primitivesService.Delivery) {
	if err := work.Settle(ctx, consumer.Queue, item, true); err != nil {
		logger.Warnf("dispatch: acknowledging %s/%s: %s", consumer.Queue.Label, item.ID, err)
	}
}

// failed settles a delivery that did not succeed. See deliver's doc for why an
// undeclared retry interval means doing nothing at all.
//
// THE INTERVAL IS READ OFF THE GRANT AND NOT THE DECLARATION, which is the one
// place this node still reads a queue's config at all: the decision is the
// consumer's own — give it back now, or let the claim expire — and the number
// it is made with is the author's either way. See settingRetryEvery.
func (d *dispatcher) failed(ctx context.Context, consumer worker.Consumer, work syncSeam, item primitivesService.Delivery) {
	if !spaced(consumer.Queue.Settings[settingRetryEvery]) {
		return
	}
	if err := work.Settle(ctx, consumer.Queue, item, false); err != nil {
		logger.Warnf("dispatch: returning %s/%s: %s", consumer.Queue.Label, item.ID, err)
	}
}

// settingRetryEvery is the vocabulary's own path for a queue's retry interval,
// and it is THE ONE SETTING THIS NODE STILL READS OFF A GRANT.
//
// IT IS UNDER `queue/` BECAUSE `sync` IS A TYPED KIND: a queue is `sync` at
// `type: queue`, so every field it declares lives in the block named after the
// type. Everything else about a queue is read by the primitives service out of
// the DECLARATION, which is what stops a caller describing a queue into being
// something else. This one survives here because the decision it feeds is the
// CONSUMER'S own — hand the item back now, or let the claim expire — and the
// number it is made with is the author's either way.
const settingRetryEvery = "queue/retry/interval"

// spaced reports whether the author declared a retry interval to space a
// redelivery by. A written zero is a written answer — an author who means
// "immediately" gets it.
func spaced(declared string) bool {
	if declared == "" {
		return false
	}
	_, err := time.ParseDuration(declared)
	return err == nil
}

// loop cycles step until the node closes. It holds no logic of its own, so
// every branch of the claim path is reachable from a test without a goroutine,
// a sleep or a real clock.
func (d *dispatcher) loop(ctx context.Context, done chan<- struct{}) {
	defer close(done)

	tick := time.NewTicker(claimEvery)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			d.step(ctx)
		}
	}
}
