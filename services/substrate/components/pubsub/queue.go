package pubsub

import (
	"context"
	"path"
	"sync"
	"sync/atomic"
	"time"

	"github.com/taubyte/tau/core/services/substrate"
	iface "github.com/taubyte/tau/core/services/substrate/components/pubsub"
	"github.com/taubyte/tau/core/services/substrate/counters"
	"github.com/taubyte/tau/services/substrate/components/counters/metrics"
	"github.com/taubyte/tau/services/substrate/components/pubsub/common"
)

// MaxConcurrentMessages bounds the messages one subscription handles at once;
// each holds a VM instance.
var MaxConcurrentMessages = 64

// MaxQueuedMessages bounds the messages one subscription keeps waiting for a
// worker. The queue holds messages only, never a goroutine or a VM instance.
var MaxQueuedMessages = 1000

// MaxQueueWait is how long a message may wait for a worker before it is dropped.
var MaxQueueWait = 30 * time.Second

// Reasons a message is dropped.
const (
	DropQueueFull = "queue full"
	DropTooOld    = "waited too long"
	DropClosing   = "closing"
)

// Function names the function a subscription delivers to.
type Function struct{ Project, App, Function string }

type dropKey struct {
	Function
	Reason string
}

var drops sync.Map // dropKey -> *atomic.Uint64

// DroppedMessages is pubsub_dropped_messages_total{project,app,function,reason}.
func DroppedMessages(fn Function, reason string) uint64 {
	if v, ok := drops.Load(dropKey{fn, reason}); ok {
		return v.(*atomic.Uint64).Load()
	}
	return 0
}

// DropCount is one pubsub_dropped_messages_total series.
type DropCount struct {
	Function
	Reason string
	Count  uint64
}

// DroppedCounts lists every function that dropped messages, by reason.
func DroppedCounts() []DropCount {
	var out []DropCount
	drops.Range(func(k, v any) bool {
		key := k.(dropKey)
		out = append(out, DropCount{key.Function, key.Reason, v.(*atomic.Uint64).Load()})
		return true
	})
	return out
}

type queued struct {
	msg iface.Message
	at  time.Time
}

// queue runs at most MaxConcurrentMessages handlers for one subscription and
// keeps up to MaxQueuedMessages more waiting, in arrival order.
type queue struct {
	ctx    context.Context
	fn     Function
	handle func(iface.Message)
	// push reports drops to the substrate counter service when it has one.
	report func(reason string, n int)

	// limits, read once so a running queue never races a change to the vars
	maxBusy, maxWaiting int
	maxWait             time.Duration

	lock    sync.Mutex
	waiting []queued
	busy    int
	closed  bool

	lastWarn atomic.Int64
}

func newQueue(ctx context.Context, fn Function, handle func(iface.Message)) *queue {
	return &queue{ctx: ctx, fn: fn, handle: handle, maxBusy: MaxConcurrentMessages, maxWaiting: MaxQueuedMessages, maxWait: MaxQueueWait}
}

func (q *queue) push(msg iface.Message) {
	q.lock.Lock()
	switch {
	case q.closed || q.ctx.Err() != nil:
		q.lock.Unlock()
		q.drop(DropClosing, 1)
	case q.busy < q.maxBusy:
		q.busy++
		q.lock.Unlock()
		go q.work(msg)
	case len(q.waiting) < q.maxWaiting:
		q.waiting = append(q.waiting, queued{msg, time.Now()})
		q.lock.Unlock()
	default:
		q.lock.Unlock()
		q.drop(DropQueueFull, 1)
	}
}

// work handles msg, then takes waiting messages until none is left.
func (q *queue) work(msg iface.Message) {
	for {
		q.handle(msg)
		var ok bool
		if msg, ok = q.next(); !ok {
			return
		}
	}
}

func (q *queue) next() (iface.Message, bool) {
	q.lock.Lock()
	defer q.lock.Unlock()
	for len(q.waiting) > 0 && !q.closed {
		m := q.waiting[0]
		q.waiting[0] = queued{}
		q.waiting = q.waiting[1:]
		if time.Since(m.at) > q.maxWait {
			q.drop(DropTooOld, 1)
			continue
		}
		return m.msg, true
	}
	q.busy--
	return nil, false
}

// close drops what is still waiting; running handlers finish on their own.
func (q *queue) close() {
	q.lock.Lock()
	n := len(q.waiting)
	q.waiting, q.closed = nil, true
	q.lock.Unlock()
	if n > 0 {
		q.drop(DropClosing, n)
	}
}

func (q *queue) queued() int {
	q.lock.Lock()
	defer q.lock.Unlock()
	return len(q.waiting)
}

func (q *queue) drop(reason string, n int) {
	v, _ := drops.LoadOrStore(dropKey{q.fn, reason}, new(atomic.Uint64))
	v.(*atomic.Uint64).Add(uint64(n))
	if q.report != nil {
		q.report(reason, n)
	}

	now := time.Now().UnixNano()
	last := q.lastWarn.Load()
	if now-last < int64(10*time.Second) || !q.lastWarn.CompareAndSwap(last, now) {
		return
	}
	common.Logger.Warnf("pubsub dropped messages for function %s/%s/%s: queue full %d, waited too long %d, closing %d (%d handlers, %d queued max, %s max wait)",
		q.fn.Project, q.fn.App, q.fn.Function,
		DroppedMessages(q.fn, DropQueueFull), DroppedMessages(q.fn, DropTooOld), DroppedMessages(q.fn, DropClosing),
		q.maxBusy, q.maxWaiting, q.maxWait)
}

// counterReport pushes drops to the substrate counter service, next to the
// function's success/failure counters: <project>/<function>/pubsub_dropped/<reason>.
func counterReport(cs substrate.CounterService, fn Function) func(string, int) {
	if cs == nil || !cs.Implemented() {
		return nil
	}
	return func(reason string, n int) {
		go cs.Push(&counters.WrappedMetric{
			Key:    counters.NewPath(path.Join(fn.Project, fn.Function)).Join("pubsub_dropped").Join(reason).String(),
			Metric: metrics.NewSumMetric(uint64(n)),
		})
	}
}
