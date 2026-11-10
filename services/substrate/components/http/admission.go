package http

import (
	"context"
	"errors"
	"os"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shirou/gopsutil/v4/mem"
	"github.com/taubyte/tau/pkg/platformerr"
)

const (
	// admissionWait absorbs a brief surge: a request waits this long for a slot
	// before it is refused, so only sustained overload gets the 503.
	admissionWait = 75 * time.Millisecond

	windowSamples = 64
	// latencySlack keeps scheduler jitter on a 1 ms baseline from reading as congestion.
	latencySlack = 5 * time.Millisecond
	// baselineDrift is slow on purpose: under real overload the limit must
	// reach the floor, and show the true baseline again, before the baseline
	// has climbed to the inflated latency and made it look normal.
	baselineDrift = 64
)

var errNodeBusy = platformerr.New(platformerr.NodeBusy, errors.New("node in-flight request limit reached"))

// maxInFlight is where the limit starts. Requests spend most of their time
// waiting on the database or the network, so more than a few dozen per CPU only
// queue up behind each other and push every request's latency (and the node's
// memory) up. 64 per CPU matches each function's own queue; 8 MiB per request
// keeps the total inside RAM. 2 CPUs and 2 GB give min(128, 256) = 128.
func maxInFlight(cpus int, memBytes uint64) int {
	return max(1, min(cpus*64, int(memBytes>>23)))
}

// limits bounds the adaptive limit. RAM sets the ceiling, since memory is the
// one thing latency will not warn about before the kernel kills the node; the
// floor keeps a node that cut itself down able to do work and measure recovery.
func limits(cpus int, memBytes uint64) (floor, start, ceiling int) {
	start = maxInFlight(cpus, memBytes)
	return min(start, cpus*8), start, max(start, int(memBytes>>22))
}

func newAdmission() *limiter {
	total := uint64(2 << 30) // shortcut: assume 2 GB if the OS will not say
	if v, err := mem.VirtualMemory(); err == nil {
		total = v.Total
	}
	// TAU_MAX_INFLIGHT pins a fixed limit, so the load test can compare.
	if n, err := strconv.Atoi(os.Getenv("TAU_MAX_INFLIGHT")); err == nil && n > 0 {
		return newLimiter(n, n, n)
	}
	return newLimiter(limits(runtime.NumCPU(), total))
}

// limiter is a concurrency limit that follows the node's latency, AIMD style.
// Every window it compares the 10th percentile of request time with a decaying
// minimum of past windows: near the minimum the limit grows, well above it the
// limit is cut.
//
// A low percentile because requests differ wildly in length: a percentile near
// the middle would move with the mix of 1 ms lookups and slow database calls,
// while the fast end moves only when the node itself queues, which delays every
// request alike. It holds while 10% of requests are fast; a mix that loses its
// fast requests reads as congestion until the baseline drifts up to it.
//
// Free slots are tokens in a channel, so a slot freed goes straight to the
// longest-waiting request and changing the limit needs no wake-up logic.
type limiter struct {
	tokens         chan struct{} // capacity ceiling; free + held = limit + owed
	floor, ceiling int64
	limit          atomic.Int64 // only written under mu
	waiting        atomic.Int64

	mu       sync.Mutex
	owed     int64 // slots to drop on release: a cut cannot recall those in use
	win      [windowSamples]time.Duration
	n        int
	seen     int64
	timedOut int64
	peak     int64
	base     time.Duration
}

func newLimiter(floor, start, ceiling int) *limiter {
	l := &limiter{tokens: make(chan struct{}, ceiling), floor: int64(floor), ceiling: int64(ceiling)}
	l.limit.Store(int64(start))
	for range start {
		l.tokens <- struct{}{}
	}
	return l
}

// acquire takes a slot, waiting up to admissionWait for one. At most limit
// callers wait, which is more than can be served within the wait anyway.
func (l *limiter) acquire(ctx context.Context) error {
	select {
	case <-l.tokens:
		return nil
	default:
	}
	if l.waiting.Add(1) > l.limit.Load() {
		l.waiting.Add(-1)
		return errNodeBusy
	}
	defer l.waiting.Add(-1)
	wait := time.NewTimer(admissionWait)
	defer wait.Stop()
	select {
	case <-l.tokens:
		return nil
	case <-wait.C:
		return errNodeBusy
	case <-ctx.Done():
		return ctx.Err()
	}
}

// release frees a slot and records how the request went. took is zero when the
// request says nothing about load.
func (l *limiter) release(took time.Duration, timedOut bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	busy := l.limit.Load() + l.owed - int64(len(l.tokens))
	if l.owed > 0 {
		l.owed--
	} else {
		l.tokens <- struct{}{}
	}
	l.observe(took, timedOut, busy)
}

// observe adds one request to the window and, when it is full, moves the limit.
// mu must be held.
func (l *limiter) observe(took time.Duration, timedOut bool, busy int64) {
	switch {
	case timedOut:
		l.timedOut++
	case took <= 0:
		return
	default:
		if l.n < len(l.win) {
			l.win[l.n] = took
			l.n++
		}
	}
	l.seen++
	l.peak = max(l.peak, busy)

	limit := l.limit.Load()
	// A window of about one round of in-flight requests, so a change shows up
	// in the latency before the next one is made.
	if l.seen < max(windowSamples, limit) {
		return
	}

	next := limit
	// More than 1 in 16: one function that always hangs must not sink the node.
	congested := l.timedOut*16 > l.seen
	if l.n > 0 {
		slices.Sort(l.win[:l.n])
		p := l.win[l.n/10]
		if l.base == 0 || p < l.base {
			l.base = p
		} else {
			l.base += (p - l.base) / baselineDrift
		}
		tolerance := l.base/2 + latencySlack
		congested = congested || p > l.base+2*tolerance
		// Only a limit in use has proved anything: an idle node must not
		// climb to the ceiling and then meet a surge there.
		if p <= l.base+tolerance && l.peak*2 >= limit {
			next = limit + max(1, limit/16)
		}
	}
	if congested {
		next = limit - max(1, limit/10)
	}
	l.setLimit(min(max(next, l.floor), l.ceiling))
	l.n, l.seen, l.timedOut, l.peak = 0, 0, 0, 0
}

// setLimit moves the limit to next. mu must be held.
func (l *limiter) setLimit(next int64) {
	cur := l.limit.Load()
	for ; cur < next; cur++ {
		if l.owed > 0 {
			l.owed--
		} else {
			l.tokens <- struct{}{}
		}
	}
	for ; cur > next; cur-- {
		select {
		case <-l.tokens:
		default:
			l.owed++
		}
	}
	l.limit.Store(next)
}

// admitted runs run holding an in-flight slot, or fails with NodeBusy when none
// frees up in time. The slot is freed on return, panic and client disconnect
// alike. A nil limiter (no limit set) admits everything.
//
// Time is counted from admission, so the wait for a slot is not fed back into
// the limit, and includes the wait for a function instance, which is queueing
// too. Cold starts are slow outliers the low percentile ignores. Failures are
// not samples: a request that fails in a microsecond would poison the minimum.
func admitted(ctx context.Context, l *limiter, run func() error) error {
	if l == nil {
		return run()
	}
	if err := l.acquire(ctx); err != nil {
		return err
	}
	var took time.Duration
	var timedOut bool
	defer func() { l.release(took, timedOut) }()

	start := time.Now()
	err := run()
	if err == nil {
		took = time.Since(start)
	}
	timedOut = platformerr.CodeOf(err) == platformerr.FunctionTimeout
	return err
}
