package node

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/queue"
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
	if d.node.worker == nil || d.node.work == nil {
		return 0
	}

	var delivered atomic.Int64
	var wg sync.WaitGroup

	for _, consumer := range d.node.worker.Consumers(ctx) {
		if consumer.Queue.Refused != "" {
			// The grant says this cloud will not serve the target — an engine
			// it has no driver for, a store of many instances nobody admits.
			// Draining it would be serving what the resolve refused.
			continue
		}

		held, err := d.node.work.queue(consumer.Queue)
		if err != nil {
			// A cluster still forming is not worth a line every two seconds:
			// it clears on its own and the next cycle is the retry.
			if !errors.Is(err, ErrQueuesForming) {
				logger.Warnf("dispatch: opening the queue %q for %s: %s",
					consumer.Queue.Label, consumer.Target.Name, err)
			}
			continue
		}

		// SWEEP BEFORE CLAIMING, and not only because Claim runs the same
		// pass: a queue whose items are ALL claimed and expiring has nothing
		// to claim, which is exactly when an exhausted item needs moving to
		// the dead letter.
		held.Sweep()

		wg.Add(1)
		go func() {
			defer wg.Done()
			delivered.Add(int64(d.drain(ctx, consumer, held)))
		}()
	}

	wg.Wait()
	return int(delivered.Load())
}

// drain takes items from one queue until it is empty.
//
// AS MANY AT ONCE AS THE WORKLOAD SAID IT MAY HOLD. `limits/concurrency` is the
// vocabulary's own number for exactly this, and the author who wrote it is
// describing a consumer the platform pushes to — a dispatcher that ignored it
// would deliver one item at a time to a workload that declared it can take
// eighty.
func (d *dispatcher) drain(ctx context.Context, consumer worker.Consumer, held *queue.Queue) int {
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
				item, err := held.Claim()
				if err != nil {
					// Nothing ready, or this node is not the leader. Both are
					// ordinary and the next cycle is the retry.
					return
				}
				d.deliver(ctx, consumer, held, item)
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
func (d *dispatcher) deliver(ctx context.Context, consumer worker.Consumer, held *queue.Queue, item queue.Delivery) {
	// THE CLAIM'S DEADLINE BOUNDS THE INVOCATION. Past it the item is
	// deliverable again, so a workload still running is a workload whose work
	// somebody else has already been handed — and the ack would be refused
	// anyway.
	runCtx, stop := context.WithDeadline(ctx, item.Deadline)
	defer stop()

	// A BUILTIN IS RUN HERE AND NOT ON A TIER. `source: {kind: builtin}` names a
	// program the runtime ships, so there is no artifact to start and the
	// delivery that would have started one is this delivery. See step.go.
	if consumer.Builtin == stepBuiltin {
		if err := d.runStep(runCtx, consumer, item); err != nil {
			logger.Warnf("dispatch: %s on %s/%s: %s",
				consumer.Target.Name, consumer.Queue.Label, item.ID, err)
			d.failed(consumer, held, item)
			return
		}
		held.Ack(item)
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
		d.failed(consumer, held, item)
	case res.Status < 200 || res.Status > 299:
		logger.Warnf("dispatch: %s answered %d for %s/%s",
			consumer.Target.Name, res.Status, consumer.Queue.Label, item.ID)
		d.failed(consumer, held, item)
	default:
		held.Ack(item)
	}
}

// failed settles a delivery that did not succeed. See deliver's doc for why an
// undeclared retry interval means doing nothing at all.
func (d *dispatcher) failed(consumer worker.Consumer, held *queue.Queue, item queue.Delivery) {
	if spaced(consumer.Queue.Settings[settingRetryEvery]) {
		held.Nack(item)
	}
}

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
