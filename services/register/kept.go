package register

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	pkgregister "github.com/taubyte/tau/pkg/register"
)

// kept.go is the readers' half of the cache: answers a node keeps, found or absent,
// until the register says what they were read from changed.
//
// AN ANSWER IS KEPT ONLY IF THE REGISTER THAT GAVE IT IS WATCHING IT. A read names a
// session and an entry; the answer is kept when it comes back bound, and dropped when
// that session names the entry, ends, or stops beating. A register node that answered
// from a replica behind the others is told of the change when it merges it, because
// it is the node watching. With no session the answer is kept for the deploy budget
// alone, as before there were sessions.

// read is one kind of question a reader keeps the answer to. A read is its own key.
type read[V any] interface {
	comparable
	from(ctx context.Context, c *Client, b bindAs) (V, bool, error)
}

type hostRead struct{ fqdn string }

func (r hostRead) from(ctx context.Context, c *Client, b bindAs) (hostAnswer, bool, error) {
	return c.resolveHostAs(ctx, r.fqdn, b)
}

type viewRead struct{ project, environment, destination string }

func (r viewRead) from(ctx context.Context, c *Client, b bindAs) (pkgregister.ViewPointer, bool, error) {
	return c.resolveViewAs(ctx, r.project, r.environment, r.destination, b)
}

type fleetRead struct{}

func (fleetRead) from(ctx context.Context, c *Client, b bindAs) (pkgregister.Pointer, bool, error) {
	return c.resolveFleetAs(ctx, b)
}

// keptLimits bound one kind of kept answer.
type keptLimits struct {
	// max is how many answers are kept.
	max int
	// watched is how long a watched answer is kept at most: a register that never
	// says otherwise is still asked again this often.
	watched time.Duration
	// unwatched is how long an answer no session watches is kept.
	unwatched time.Duration
}

var defaultKeptLimits = keptLimits{max: 1 << 14, watched: 10 * time.Minute, unwatched: pkgregister.DefaultTTL}

type kept[R read[V], V any] struct {
	w      *watcher
	limits keptLimits
	now    func() time.Time

	answers sync.Map // R -> *answer[R, V]

	mu    sync.Mutex
	order []*answer[R, V]
}

// answer is one kept answer. Everything but changed is written before done closes
// and read after.
type answer[R read[V], V any] struct {
	r     R
	in    *kept[R, V]
	done  chan struct{}
	value V
	err   error
	at    time.Time
	// ok is an answer, found or absent, rather than a failure to look.
	ok bool
	// s is the session watching it, nil for an answer kept unwatched.
	s       *watchSession
	entry   uint64
	changed atomic.Bool
}

func newKept[R read[V], V any](w *watcher, limits keptLimits, now func() time.Time) *kept[R, V] {
	return &kept[R, V]{w: w, limits: limits, now: now}
}

// get is r's answer, kept or read.
func (k *kept[R, V]) get(ctx context.Context, r R) (V, error) {
	if held, ok := k.answers.Load(r); ok {
		a := held.(*answer[R, V])
		if err := a.wait(ctx); err != nil {
			var zero V
			return zero, err
		}
		if k.fresh(a, k.now()) {
			return a.value, a.err
		}
		k.drop(a)
	}

	a := &answer[R, V]{r: r, in: k, done: make(chan struct{})}
	if held, loaded := k.answers.LoadOrStore(r, a); loaded {
		// Somebody else is reading it: their answer is ours.
		other := held.(*answer[R, V])
		if err := other.wait(ctx); err != nil {
			var zero V
			return zero, err
		}
		if other.ok && !other.changed.Load() {
			return other.value, other.err
		}
		return k.unkept(ctx, r)
	}
	k.load(ctx, a)
	return a.value, a.err
}

func (a *answer[R, V]) wait(ctx context.Context) error {
	select {
	case <-a.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (k *kept[R, V]) fresh(a *answer[R, V], now time.Time) bool {
	if !a.ok || a.changed.Load() {
		return false
	}
	if a.s == nil {
		return now.Sub(a.at) < k.limits.unwatched
	}
	return !a.s.dead.Load() && now.Sub(a.at) < k.limits.watched
}

// load reads a's answer and keeps it as far as it may be kept.
func (k *kept[R, V]) load(ctx context.Context, a *answer[R, V]) {
	defer close(a.done)

	s := k.w.pick()
	var b bindAs
	if s != nil {
		a.entry = k.w.ids.Add(1)
		if s.hold(a.entry, a) {
			a.s, b = s, bindAs{session: s.id, entry: a.entry}
		} else {
			s = nil
		}
	}
	c, err := k.w.clientFor(s)
	if err != nil {
		a.err = err
		k.answers.CompareAndDelete(a.r, a)
		return
	}

	value, bound, err := a.r.from(ctx, c, b)
	a.value, a.err, a.at = value, err, k.now()
	if !answers(err) {
		// No answer: nothing to keep, and the register bound nothing.
		if s != nil {
			s.release(a.entry)
		} else {
			k.w.failedThrough(c)
		}
		k.answers.CompareAndDelete(a.r, a)
		return
	}
	a.ok = true
	if s != nil && !bound {
		s.release(a.entry)
		a.s = nil
	}
	if a.changed.Load() {
		// Told before it was read back: it is the answer of a moment already past.
		k.answers.CompareAndDelete(a.r, a)
		return
	}
	k.admit(a)
}

// unkept reads r without keeping it: an answer somebody else read changed before it
// could be handed on.
func (k *kept[R, V]) unkept(ctx context.Context, r R) (V, error) {
	c, err := k.w.clientFor(nil)
	if err != nil {
		var zero V
		return zero, err
	}
	v, _, err := r.from(ctx, c, bindAs{})
	return v, err
}

// admit counts a kept answer against the bound, dropping the oldest past it.
func (k *kept[R, V]) admit(a *answer[R, V]) {
	k.mu.Lock()
	k.order = append(k.order, a)
	var evicted []*answer[R, V]
	for len(k.order) > k.limits.max {
		evicted = append(evicted, k.order[0])
		k.order[0] = nil
		k.order = k.order[1:]
	}
	k.mu.Unlock()
	for _, old := range evicted {
		k.drop(old)
	}
}

// drop lets a go: unkept, and released on its session if it is still bound there.
func (k *kept[R, V]) drop(a *answer[R, V]) {
	k.answers.CompareAndDelete(a.r, a)
	if a.s != nil && a.s.release(a.entry) {
		a.s.forgetLater(a.entry)
	}
}

// told is the register's word that a's answer changed: it is already released there.
func (a *answer[R, V]) told() {
	a.changed.Store(true)
	a.in.answers.CompareAndDelete(a.r, a)
}

// len is how many answers are kept, for a test and an operator.
func (k *kept[R, V]) len() int {
	n := 0
	k.answers.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}
