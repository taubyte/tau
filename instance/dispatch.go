package instance

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/taubyte/tau/pkg/specs"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	primitivesService "github.com/taubyte/tau/services/primitives"
	"github.com/taubyte/tau/services/worker"
	"github.com/taubyte/tau/services/worker/primitive"
)

// The claiming half of a queue: `receive` is a delivery, so it is not an ABI. Here
// because the worker knows which workloads consume and the node reaches the queues.
// step is one cycle and loop a ticker, so the branches are testable. A claim is a
// write, refused off the raft leader, so followers get ErrNotLeader and do nothing.
// It claims through the primitives service, whose declaration read sets the config
// and whose lifecycle buries exhausted items.

// dispatcher drains the queues of the deployments this node serves.
type dispatcher struct {
	node *Instance

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
	// costs and is the same interval services/plm runs at.
	claimEvery = 2 * time.Second

	// perCycle is how many items one goroutine takes before yielding.
	perCycle = 32
)

// deliveryHeaders are what a consumer is told about its item, including the queue's
// label (as the author wrote it and passes to `send`), since a wasm consumer is
// dispatched at "/" and needs another way to tell two queues apart.
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

// ordinaryRefusal reports whether a refused claim clears on its own (a follower, a
// forming group, no service yet) and is not worth logging every cycle. It matches the
// tier's arm, the two names and the Unavailable code, since which names survive the
// wire is services/primitives/status.go's to say.
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

// drain takes items from one queue until it is empty, as many at once as the
// workload's `limits/concurrency` allows.
func (d *dispatcher) drain(ctx context.Context, consumer worker.Consumer, work primitive.Seam) int {
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
				// One at a time though the seam takes a batch: a claim starts the visibility timer,
				// so a batch delivered serially could time out its last items into duplicates.
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

// invoke runs a workload through placement: claims are leader-only, so running what
// was claimed locally would put all queued work on one node. A node with no stats can
// only reach itself.
func (d *dispatcher) invoke(ctx context.Context, at worker.Target, req *invoke.Request) (*invoke.Response, error) {
	if d.node.workers == nil {
		return d.node.worker.Invoke(ctx, at, req)
	}
	return d.node.workers.Invoke(ctx, at, req)
}

// deliver hands one item to the workload and settles on the answer; 2xx acknowledges.
// A failure is nacked only when a retry interval is declared: without one the
// vocabulary says the visibility timeout governs, and an immediate nack would burn
// every attempt in milliseconds. The claim is left to expire instead.
func (d *dispatcher) deliver(ctx context.Context, consumer worker.Consumer, work primitive.Seam, item primitivesService.Delivery) {
	// The claim's deadline bounds the invocation, as a duration on the injected queue
	// clock rather than an instant compared against the wall clock.
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

// acknowledge ends a successful delivery. A refused ack means the work will be
// delivered again, so it is logged.
func (d *dispatcher) acknowledge(ctx context.Context, consumer worker.Consumer, work primitive.Seam, item primitivesService.Delivery) {
	if err := work.Settle(ctx, consumer.Queue, item, true); err != nil {
		logger.Warnf("dispatch: acknowledging %s/%s: %s", consumer.Queue.Label, item.ID, err)
	}
}

// failed settles a failed delivery (see deliver). The interval comes off the grant:
// the decision is the consumer's.
func (d *dispatcher) failed(ctx context.Context, consumer worker.Consumer, work primitive.Seam, item primitivesService.Delivery) {
	if !spaced(consumer.Queue.Settings[settingRetryEvery]) {
		return
	}
	if err := work.Settle(ctx, consumer.Queue, item, false); err != nil {
		logger.Warnf("dispatch: returning %s/%s: %s", consumer.Queue.Label, item.ID, err)
	}
}

// settingRetryEvery is the vocabulary's path for a queue's retry interval, the one
// setting this node still reads off a grant (see failed).
const settingRetryEvery = specs.SyncQueueKeyRetryInterval

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
