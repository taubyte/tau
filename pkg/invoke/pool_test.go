package invoke

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeRuntime starts in nanoseconds, so every rule about WHEN things start is a
// fast unit test rather than a container fixture.
type fakeRuntime struct {
	name string

	mu       sync.Mutex
	started  int
	closed   int
	live     int
	maxLive  int
	startErr error

	serve func(ctx context.Context, req *Request) (*Response, error)

	// pipe makes Dial hand back a net.Pipe, which cannot half-close — the case
	// that separates "forwarded the answer" from "always said yes".
	pipe bool
}

func newFakeRuntime(name string) *fakeRuntime {
	return &fakeRuntime{name: name}
}

func (e *fakeRuntime) Name() string { return e.name }

func (e *fakeRuntime) Start(ctx context.Context, spec Spec) (Instance, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.startErr != nil {
		return nil, e.startErr
	}
	e.started++
	e.live++
	if e.live > e.maxLive {
		e.maxLive = e.live
	}
	return &fakeInstance{rt: e, spec: spec}, nil
}

func (e *fakeRuntime) counts() (started, closed int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.started, e.closed
}

type fakeInstance struct {
	rt   *fakeRuntime
	spec Spec

	inFlight atomic.Int32
	maxSeen  atomic.Int32
	closedAt atomic.Bool
}

func (i *fakeInstance) Serve(ctx context.Context, req *Request) (*Response, error) {
	n := i.inFlight.Add(1)
	for {
		m := i.maxSeen.Load()
		if n <= m || i.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	defer i.inFlight.Add(-1)

	if i.rt.serve != nil {
		return i.rt.serve(ctx, req)
	}
	return &Response{Status: 200, Body: []byte(i.spec.Revision)}, nil
}

func (i *fakeInstance) Close() error {
	i.closedAt.Store(true)
	i.rt.mu.Lock()
	defer i.rt.mu.Unlock()
	i.rt.closed++
	i.rt.live--
	return nil
}

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock { return &testClock{t: time.Unix(1_700_000_000, 0)} }

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newPool(t *testing.T, e Runtime, adjust func() []Option) (*Pool, *testClock) {
	t.Helper()
	c := newTestClock()
	opts := []Option{WithRuntime(e), WithClock(c.now)}
	if adjust != nil {
		opts = append(opts, adjust()...)
	}
	p, err := NewPool(opts...)
	require.NoError(t, err)
	t.Cleanup(func() { p.Close() })
	return p, c
}

func spec(name, rev string) Spec {
	return Spec{Runtime: "fake", Name: name, Revision: rev}
}

// TestAWarmInstanceIsReused — the whole reason a pool exists. The second
// request must not pay a cold start.
func TestAWarmInstanceIsReused(t *testing.T) {
	e := newFakeRuntime("fake")
	p, _ := newPool(t, e, nil)
	ctx := context.Background()

	for range 5 {
		resp, err := p.Do(ctx, spec("shop", "r1"), &Request{})
		require.NoError(t, err)
		require.Equal(t, 200, resp.Status)
	}

	started, _ := e.counts()
	require.Equal(t, 1, started, "five requests, one cold start")
	require.Equal(t, 1, p.Warm(spec("shop", "r1")))
}

// TestANewRevisionNeverSharesAnInstance. This is what makes a deploy a deploy:
// new code runs in new instances, and nothing reaches into a running process to
// swap what it executes.
func TestANewRevisionNeverSharesAnInstance(t *testing.T) {
	e := newFakeRuntime("fake")
	p, _ := newPool(t, e, nil)
	ctx := context.Background()

	old, err := p.Do(ctx, spec("shop", "r1"), &Request{})
	require.NoError(t, err)
	require.Equal(t, "r1", string(old.Body))

	fresh, err := p.Do(ctx, spec("shop", "r2"), &Request{})
	require.NoError(t, err)
	require.Equal(t, "r2", string(fresh.Body))

	started, _ := e.counts()
	require.Equal(t, 2, started)
	require.Equal(t, 1, p.Warm(spec("shop", "r1")), "and the old revision is still warm")
	require.Equal(t, 1, p.Warm(spec("shop", "r2")),
		"which is what makes a rollback warm rather than a cold start")
}

// TestConcurrencyOneMeansOneRequestAtATimePerInstance. Code written for a
// serverless platform is routinely not safe to enter twice; a platform that
// quietly shares an instance turns that into corruption rather than a queue.
func TestConcurrencyOneMeansOneRequestAtATimePerInstance(t *testing.T) {
	e := newFakeRuntime("fake")
	release := make(chan struct{})
	var instances sync.Map
	e.serve = func(ctx context.Context, req *Request) (*Response, error) {
		<-release
		return &Response{Status: 200}, nil
	}
	p, _ := newPool(t, e, nil)

	s := spec("shop", "r1")
	s.Concurrency = 1

	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.Do(context.Background(), s, &Request{})
		}()
	}

	require.Eventually(t, func() bool { return p.Warm(s) == 3 }, 3*time.Second, time.Millisecond,
		"three concurrent requests at concurrency 1 need three instances")
	close(release)
	wg.Wait()
	instances.Range(func(any, any) bool { return true })

	started, _ := e.counts()
	require.Equal(t, 3, started)
}

// TestConcurrencyAboveOneSharesAnInstance, and never exceeds what the spec
// allows.
func TestConcurrencyAboveOneSharesAnInstance(t *testing.T) {
	e := newFakeRuntime("fake")
	release := make(chan struct{})
	e.serve = func(ctx context.Context, req *Request) (*Response, error) {
		<-release
		return &Response{Status: 200}, nil
	}
	p, _ := newPool(t, e, nil)

	s := spec("shop", "r1")
	s.Concurrency = 4

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); p.Do(context.Background(), s, &Request{}) }()
	}

	require.Eventually(t, func() bool {
		started, _ := e.counts()
		return started == 1 && p.Warm(s) == 1
	}, 3*time.Second, time.Millisecond, "four requests share one instance at concurrency 4")

	close(release)
	wg.Wait()
}

// TestABurstQueuesRatherThanFailing. A serverless platform that returned an
// error the moment every instance was busy would be useless under exactly the
// traffic it exists for.
func TestABurstQueuesRatherThanFailing(t *testing.T) {
	e := newFakeRuntime("fake")
	gate := make(chan struct{})
	var served atomic.Int32
	e.serve = func(ctx context.Context, req *Request) (*Response, error) {
		<-gate
		served.Add(1)
		return &Response{Status: 200}, nil
	}
	p, _ := newPool(t, e, func() []Option { return []Option{WithMaxInstances(2)} })

	s := spec("shop", "r1")
	s.Concurrency = 1

	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := p.Do(context.Background(), s, &Request{})
			errs <- err
		}()
	}

	require.Eventually(t, func() bool { return p.Warm(s) == 2 }, 3*time.Second, time.Millisecond)
	close(gate)
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err, "every request eventually ran; none was refused")
	}
	require.Equal(t, int32(6), served.Load())
	require.LessOrEqual(t, e.maxLive, 2, "and the instance cap held throughout")
}

// TestAWaitingCallerThatGivesUpStopsWaiting.
func TestAWaitingCallerThatGivesUpStopsWaiting(t *testing.T) {
	e := newFakeRuntime("fake")
	block := make(chan struct{})
	defer close(block)
	e.serve = func(ctx context.Context, req *Request) (*Response, error) {
		<-block
		return &Response{Status: 200}, nil
	}
	p, _ := newPool(t, e, func() []Option { return []Option{WithMaxInstances(1)} })

	s := spec("shop", "r1")
	s.Concurrency = 1

	go p.Do(context.Background(), s, &Request{})
	require.Eventually(t, func() bool { return p.Warm(s) == 1 }, 3*time.Second, time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := p.Do(ctx, s, &Request{})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestAnIdleSpecCostsNothing — scale to zero, on an injected clock.
func TestAnIdleSpecCostsNothing(t *testing.T) {
	e := newFakeRuntime("fake")
	p, clock := newPool(t, e, func() []Option { return []Option{WithIdleTimeout(time.Minute)} })
	ctx := context.Background()

	_, err := p.Do(ctx, spec("shop", "r1"), &Request{})
	require.NoError(t, err)
	require.Equal(t, 1, p.Warm(spec("shop", "r1")))

	clock.advance(30 * time.Second)
	require.Zero(t, p.Reap(), "not idle long enough yet")
	require.Equal(t, 1, p.Warm(spec("shop", "r1")))

	clock.advance(31 * time.Second)
	require.Equal(t, 1, p.Reap())
	require.Zero(t, p.Warm(spec("shop", "r1")), "the spec now costs nothing")

	_, closed := e.counts()
	require.Equal(t, 1, closed)

	// And the next request pays a cold start, which is the deal.
	_, err = p.Do(ctx, spec("shop", "r1"), &Request{})
	require.NoError(t, err)
	started, _ := e.counts()
	require.Equal(t, 2, started)
}

// TestABusyInstanceIsNeverReaped even if it has been serving one long request
// since before the idle window.
func TestABusyInstanceIsNeverReaped(t *testing.T) {
	e := newFakeRuntime("fake")
	block := make(chan struct{})
	e.serve = func(ctx context.Context, req *Request) (*Response, error) {
		<-block
		return &Response{Status: 200}, nil
	}
	p, clock := newPool(t, e, func() []Option { return []Option{WithIdleTimeout(time.Second)} })

	s := spec("shop", "r1")
	go p.Do(context.Background(), s, &Request{})
	require.Eventually(t, func() bool { return p.Warm(s) == 1 }, 3*time.Second, time.Millisecond)

	clock.advance(time.Hour)
	require.Zero(t, p.Reap(), "a request is still running in it")
	require.Equal(t, 1, p.Warm(s))

	close(block)
}

// TestAFailedRequestRetiresTheInstance. The pool cannot tell a malformed
// request from a wedged process, and a broken instance kept warm serves every
// subsequent request badly — which reads as an intermittent fault rather than a
// dead worker.
func TestAFailedRequestRetiresTheInstance(t *testing.T) {
	e := newFakeRuntime("fake")
	boom := errors.New("the guest panicked")
	e.serve = func(ctx context.Context, req *Request) (*Response, error) { return nil, boom }
	p, _ := newPool(t, e, nil)

	_, err := p.Do(context.Background(), spec("shop", "r1"), &Request{})
	require.ErrorIs(t, err, boom)
	require.Zero(t, p.Warm(spec("shop", "r1")), "it is not kept warm")

	_, closed := e.counts()
	require.Equal(t, 1, closed)
}

// TestATimeoutIsEnforcedPerRequest.
func TestATimeoutIsEnforcedPerRequest(t *testing.T) {
	e := newFakeRuntime("fake")
	e.serve = func(ctx context.Context, req *Request) (*Response, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	p, _ := newPool(t, e, nil)

	s := spec("shop", "r1")
	s.Timeout = 20 * time.Millisecond

	_, err := p.Do(context.Background(), s, &Request{})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestTheDefaultTimeoutAppliesWhenTheSpecIsSilent(t *testing.T) {
	e := newFakeRuntime("fake")
	e.serve = func(ctx context.Context, req *Request) (*Response, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	p, _ := newPool(t, e, func() []Option { return []Option{WithTimeout(20 * time.Millisecond)} })

	_, err := p.Do(context.Background(), spec("shop", "r1"), &Request{})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestAnUnknownEngineIsRefusedRatherThanDefaulted. Running a container spec on
// the wasm engine would be a very confusing failure.
func TestAnUnknownEngineIsRefusedRatherThanDefaulted(t *testing.T) {
	p, _ := newPool(t, newFakeRuntime("fake"), nil)

	s := spec("shop", "r1")
	s.Runtime = "container"
	_, err := p.Do(context.Background(), s, &Request{})
	require.ErrorIs(t, err, ErrNoRuntime)
}

// TestTwoEnginesCoexist — the point of the seam.
func TestTwoEnginesCoexist(t *testing.T) {
	wasm, ctr := newFakeRuntime("wasm"), newFakeRuntime("container")
	p, err := NewPool(WithRuntime(wasm, ctr))
	require.NoError(t, err)
	t.Cleanup(func() { p.Close() })

	_, err = p.Do(context.Background(), Spec{Runtime: "wasm", Name: "fn", Revision: "r1"}, &Request{})
	require.NoError(t, err)
	_, err = p.Do(context.Background(), Spec{Runtime: "container", Name: "api", Revision: "r1"}, &Request{})
	require.NoError(t, err)

	ws, _ := wasm.counts()
	cs, _ := ctr.counts()
	require.Equal(t, 1, ws)
	require.Equal(t, 1, cs)
}

func TestAStartFailureReachesTheCaller(t *testing.T) {
	e := newFakeRuntime("fake")
	e.startErr = errors.New("image not found")
	p, _ := newPool(t, e, nil)

	_, err := p.Do(context.Background(), spec("shop", "r1"), &Request{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "image not found")
	require.Zero(t, p.Warm(spec("shop", "r1")))
}

func TestASpecMustIdentifyItself(t *testing.T) {
	p, _ := newPool(t, newFakeRuntime("fake"), nil)
	ctx := context.Background()

	_, err := p.Do(ctx, Spec{Runtime: "fake", Revision: "r1"}, &Request{})
	require.ErrorIs(t, err, ErrNoName)

	_, err = p.Do(ctx, Spec{Runtime: "fake", Name: "shop"}, &Request{})
	require.ErrorIs(t, err, ErrNoRevision, "without it a deploy cannot be told from the one before")

	_, err = p.Do(ctx, Spec{Name: "shop", Revision: "r1"}, &Request{})
	require.Error(t, err)

	_, err = p.Do(ctx, Spec{Runtime: "fake", Name: "shop", Revision: "r1", Concurrency: -1}, &Request{})
	require.Error(t, err)
}

func TestPoolOptionsAreValidated(t *testing.T) {
	_, err := NewPool(WithRuntime(newFakeRuntime("f")), WithIdleTimeout(-time.Second))
	require.Error(t, err)

	_, err = NewPool(WithRuntime(newFakeRuntime("f")), WithMaxInstances(-1))
	require.Error(t, err)

	_, err = NewPool(WithRuntime(newFakeRuntime("f")), WithTimeout(-time.Second))
	require.Error(t, err)

	_, err = NewPool(WithRuntime(nil))
	require.Error(t, err)

	e := newFakeRuntime("fake")
	_, err = NewPool(WithRuntime(e, newFakeRuntime("fake")))
	require.Error(t, err, "two runtimes answering to one name is ambiguous, not a merge")
}

func TestCloseShutsEverythingDownAndIsIdempotent(t *testing.T) {
	e := newFakeRuntime("fake")
	p, _ := newPool(t, e, nil)
	ctx := context.Background()

	_, err := p.Do(ctx, spec("a", "r1"), &Request{})
	require.NoError(t, err)
	_, err = p.Do(ctx, spec("b", "r1"), &Request{})
	require.NoError(t, err)

	require.NoError(t, p.Close())
	_, closed := e.counts()
	require.Equal(t, 2, closed)

	require.NoError(t, p.Close())
	_, err = p.Do(ctx, spec("a", "r1"), &Request{})
	require.ErrorIs(t, err, ErrPoolClosed)
}

// TestTheReaperRunsOnItsOwn.
func TestTheReaperRunsOnItsOwn(t *testing.T) {
	e := newFakeRuntime("fake")
	p, clock := newPool(t, e, func() []Option { return []Option{WithIdleTimeout(time.Millisecond)} })

	_, err := p.Do(context.Background(), spec("shop", "r1"), &Request{})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.StartReaper(ctx, time.Millisecond)
	clock.advance(time.Second)

	require.Eventually(t, func() bool { return p.Warm(spec("shop", "r1")) == 0 },
		3*time.Second, time.Millisecond)
}

func TestWarmIsZeroForSomethingNeverRun(t *testing.T) {
	p, _ := newPool(t, newFakeRuntime("fake"), nil)
	require.Zero(t, p.Warm(spec("never", "r1")))
}

// TestAnInstanceStartedIntoAClosingPoolIsNotLeaked. A cold start happens
// OUTSIDE the pool's lock — deliberately, since holding it across a container
// start would block every other spec on the node — so a Close can land while an
// instance is coming up. The instance that arrives late has to be closed rather
// than parked in a pool nobody will drain.
func TestAnInstanceStartedIntoAClosingPoolIsNotLeaked(t *testing.T) {
	e := newFakeRuntime("fake")
	starting := make(chan struct{})
	proceed := make(chan struct{})

	slow := &slowStartRuntime{fakeRuntime: e, starting: starting, proceed: proceed}
	p, err := NewPool(WithRuntime(slow))
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, err := p.Do(context.Background(), spec("shop", "r1"), &Request{})
		done <- err
	}()

	<-starting
	require.NoError(t, p.Close())
	close(proceed)

	require.ErrorIs(t, <-done, ErrPoolClosed)

	started, closed := e.counts()
	require.Equal(t, 1, started)
	require.Equal(t, 1, closed, "the late arrival was closed, not parked")
}

type slowStartRuntime struct {
	*fakeRuntime
	starting chan struct{}
	proceed  chan struct{}
	once     sync.Once
}

func (e *slowStartRuntime) Start(ctx context.Context, spec Spec) (Instance, error) {
	e.once.Do(func() { close(e.starting) })
	<-e.proceed
	return e.fakeRuntime.Start(ctx, spec)
}

// TestABurstOnAColdSpecStartsOneInstance, not one per request. Without
// single-flight every arrival finds no warm slot and starts its own, so a spec
// whose concurrency could absorb the whole burst from one instance instead
// starts MaxInstances of them — seconds of latency and hundreds of megabytes
// each, once the engine is containers.
func TestABurstOnAColdSpecStartsOneInstance(t *testing.T) {
	e := newFakeRuntime("fake")
	slow := make(chan struct{})
	gate := &gatedStartRuntime{fakeRuntime: e, gate: slow}

	p, err := NewPool(WithRuntime(gate))
	require.NoError(t, err)
	t.Cleanup(func() { p.Close() })

	s := spec("shop", "r1")
	s.Concurrency = 8

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := p.Do(context.Background(), s, &Request{})
			require.NoError(t, err)
		}()
	}

	// Let every request pile up behind the one cold start.
	require.Eventually(t, func() bool { return gate.waiting() >= 1 }, 3*time.Second, time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	close(slow)
	wg.Wait()

	started, _ := e.counts()
	require.Equal(t, 1, started, "eight concurrent cold requests, one cold start")
	require.Equal(t, 1, p.Warm(s))
}

// TestAWaiterStartsItsOwnWhenTheFirstInstanceIsFull. Single-flight must not
// become a cap: at concurrency 1 the second request needs a second instance.
func TestAWaiterStartsItsOwnWhenTheFirstInstanceIsFull(t *testing.T) {
	e := newFakeRuntime("fake")
	hold := make(chan struct{})
	e.serve = func(ctx context.Context, req *Request) (*Response, error) {
		<-hold
		return &Response{Status: 200}, nil
	}
	p, _ := newPool(t, e, nil)

	s := spec("shop", "r1")
	s.Concurrency = 1

	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() { defer wg.Done(); p.Do(context.Background(), s, &Request{}) }()
	}

	require.Eventually(t, func() bool { return p.Warm(s) == 3 }, 3*time.Second, time.Millisecond,
		"three requests at concurrency 1 still need three instances")
	close(hold)
	wg.Wait()
}

// TestAFailedColdStartReleasesTheWaiters rather than leaving them parked.
func TestAFailedColdStartReleasesTheWaiters(t *testing.T) {
	e := newFakeRuntime("fake")
	e.startErr = errors.New("image not found")
	p, _ := newPool(t, e, nil)

	s := spec("shop", "r1")
	s.Concurrency = 4

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := p.Do(context.Background(), s, &Request{})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.Error(t, err, "every waiter is told, none is left hanging")
	}
}

type gatedStartRuntime struct {
	*fakeRuntime
	gate chan struct{}

	mu  sync.Mutex
	inW int
}

func (e *gatedStartRuntime) waiting() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.inW
}

func (e *gatedStartRuntime) Start(ctx context.Context, spec Spec) (Instance, error) {
	e.mu.Lock()
	e.inW++
	e.mu.Unlock()
	<-e.gate
	return e.fakeRuntime.Start(ctx, spec)
}

// TestEveryPoolOptionRefusesWhatItCannotUse.
func TestEveryPoolOptionRefusesWhatItCannotUse(t *testing.T) {
	for _, tc := range []struct {
		name string
		opt  Option
	}{
		{"a nil runtime", WithRuntime(nil)},
		{"zero idle timeout", WithIdleTimeout(0)},
		{"a negative idle timeout", WithIdleTimeout(-time.Second)},
		{"no instances", WithMaxInstances(0)},
		{"a negative timeout", WithTimeout(-time.Second)},
		{"a nil clock", WithClock(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewPool(WithRuntime(newFakeRuntime("fake")), tc.opt)
			require.Error(t, err)
		})
	}
}

// TestAPoolWithNoRuntimesCanRunNothing, and says so at construction rather than
// on the first request.
func TestAPoolWithNoRuntimesCanRunNothing(t *testing.T) {
	_, err := NewPool()
	require.ErrorIs(t, err, ErrNoRuntimes)

	_, err = NewPool(WithIdleTimeout(time.Minute))
	require.ErrorIs(t, err, ErrNoRuntimes)
}

func TestPoolOptionsThatAreAcceptedTakeEffect(t *testing.T) {
	c := newTestClock()
	p, err := NewPool(
		WithRuntime(newFakeRuntime("fake")),
		WithIdleTimeout(7*time.Minute),
		WithMaxInstances(9),
		WithTimeout(11*time.Second),
		WithClock(c.now),
	)
	require.NoError(t, err)
	t.Cleanup(func() { p.Close() })

	require.Equal(t, 7*time.Minute, p.opts.idleTimeout)
	require.Equal(t, 9, p.opts.maxInstances)
	require.Equal(t, 11*time.Second, p.opts.timeout)
	require.Equal(t, c.now(), p.opts.now())
}

// ── transformers ─────────────────────────────────────────────────────────

// A TRANSFORMER IS POOLED FOR THE SAME REASONS A HANDLER IS, and that claim is
// the whole argument for Transform living here rather than in the tier. It runs
// on every request and nothing about its answer can be kept, so the cold start
// it would otherwise pay each time — measured at ~13ms for a small component —
// is exactly what these warm slots exist to remove.

func (i *fakeInstance) Transform(_ context.Context, req *Request) (*Outcome, error) {
	if req.Path == "/reject" {
		return &Outcome{Answer: &Response{Status: 403}}, nil
	}
	forwarded := *req
	forwarded.Path = req.Path + "/shaped"
	return &Outcome{Forward: &forwarded}, nil
}

// TestFIVETRANSFORMSAreONEColdStart.
func TestTransformReusesAWarmInstance(t *testing.T) {
	e := newFakeRuntime("wasm")
	p, err := NewPool(WithRuntime(e))
	require.NoError(t, err)
	defer p.Close()

	spec := Spec{Runtime: "wasm", Name: "redact", Artifact: "bafy", Revision: "1"}
	for range 5 {
		out, err := p.Transform(t.Context(), spec, &Request{Path: "/checkout"})
		require.NoError(t, err)
		require.Equal(t, "/checkout/shaped", out.Forward.Path)
	}

	started, _ := e.counts()
	require.Equal(t, 1, started, "five transforms, one cold start")
}

// TestATransformerAndAHandlerOfONESpecShareNothing, which is not a subtlety:
// they are different specs — a gateway names a different function for each — so
// they are different groups, and a transformer's instances never serve a
// request.
func TestATransformerIsItsOwnGroup(t *testing.T) {
	e := newFakeRuntime("wasm")
	p, err := NewPool(WithRuntime(e))
	require.NoError(t, err)
	defer p.Close()

	_, err = p.Transform(t.Context(), Spec{Runtime: "wasm", Name: "redact", Artifact: "bafy", Revision: "1"}, &Request{Path: "/"})
	require.NoError(t, err)
	_, err = p.Do(t.Context(), Spec{Runtime: "wasm", Name: "checkout", Artifact: "bafy", Revision: "1"}, &Request{Path: "/"})
	require.NoError(t, err)

	started, _ := e.counts()
	require.Equal(t, 2, started, "two functions, two groups")
}

// TestTHEANSWERArmStopsTheChain, through the pool, so the outcome is what the
// caller receives rather than something the pool flattens.
func TestTransformCarriesAnAnswerBack(t *testing.T) {
	e := newFakeRuntime("wasm")
	p, err := NewPool(WithRuntime(e))
	require.NoError(t, err)
	defer p.Close()

	out, err := p.Transform(t.Context(), Spec{Runtime: "wasm", Name: "redact", Artifact: "bafy", Revision: "1"}, &Request{Path: "/reject"})
	require.NoError(t, err)
	require.Nil(t, out.Forward)
	require.Equal(t, 403, out.Answer.Status)
}

// TestATIERTHATCANNOTTRANSFORMSaysSo rather than serving the request as if the
// transformer were not there. A container is a process that answers requests and
// has no second export to call.
func TestATierThatCannotTransformIsRefused(t *testing.T) {
	e := &handlerOnlyRuntime{name: "container"}
	p, err := NewPool(WithRuntime(e))
	require.NoError(t, err)
	defer p.Close()

	_, err = p.Transform(t.Context(), Spec{Runtime: "container", Name: "sidecar", Artifact: "img", Revision: "1"}, &Request{Path: "/"})
	require.ErrorIs(t, err, ErrNoRuntime)
	require.ErrorContains(t, err, "container")
}

// handlerOnlyRuntime starts instances that serve and nothing else — a container.
type handlerOnlyRuntime struct{ name string }

func (e *handlerOnlyRuntime) Name() string { return e.name }

func (e *handlerOnlyRuntime) Start(context.Context, Spec) (Instance, error) {
	return &handlerOnlyInstance{}, nil
}

type handlerOnlyInstance struct{}

func (handlerOnlyInstance) Serve(context.Context, *Request) (*Response, error) {
	return &Response{Status: 200}, nil
}
func (handlerOnlyInstance) Close() error { return nil }

// ── connections ──────────────────────────────────────────────────────────

// A CONNECTION OUTLIVES THE CALL THAT OPENED IT, which is the one thing here
// that Do and Transform do not do. The slot goes back when the connection
// closes, so an instance somebody is talking to is not idle and a spec whose
// connections are all long-lived reaches its ceiling and queues.

func (i *fakeInstance) Dial(_ context.Context, port string) (net.Conn, error) {
	if port == "refused" {
		return nil, errors.New("no such port")
	}
	if i.rt.pipe {
		client, server := net.Pipe()
		go func() { io.Copy(io.Discard, server); server.Close() }()
		return client, nil
	}

	// A REAL SOCKET, because what is being pooled here is a connection and a
	// pipe is not one: it cannot half-close, which is the property the thing
	// above this cares about most.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	go func() {
		defer l.Close()
		if c, err := l.Accept(); err == nil {
			io.Copy(io.Discard, c)
			c.Close()
		}
	}()
	return net.Dial("tcp", l.Addr().String())
}

func connSpec() Spec {
	return Spec{Runtime: "wasm", Name: "db", Artifact: "img", Revision: "1", Concurrency: 1}
}

// TestTheINSTANCEIsHeldWhileTheConnectionIsOpen.
func TestAConnectionHoldsItsInstance(t *testing.T) {
	e := newFakeRuntime("wasm")
	// ONE INSTANCE, ONE AT A TIME. The pool's ceiling is MaxInstances ×
	// Concurrency, so a second connection would otherwise start a second
	// instance — which is right, and not what this is about.
	p, err := NewPool(WithRuntime(e), WithMaxInstances(1))
	require.NoError(t, err)
	defer p.Close()

	first, err := p.Conn(t.Context(), connSpec(), "pg")
	require.NoError(t, err)

	// A SECOND CONNECTION CANNOT PROCEED while the first is open — the ceiling
	// means the same thing for a socket as it does for a request.
	second := make(chan error, 1)
	go func() {
		c, err := p.Conn(t.Context(), connSpec(), "pg")
		if c != nil {
			c.Close()
		}
		second <- err
	}()

	select {
	case <-second:
		t.Fatal("a second connection was served while the first held the only slot")
	case <-time.After(150 * time.Millisecond):
	}

	require.NoError(t, first.Close())
	require.NoError(t, <-second, "closing the first handed the slot back")

	started, _ := e.counts()
	require.Equal(t, 1, started, "one instance served both, one after the other")
}

// TestCLOSINGTwiceReleasesOnce. Close on a net.Conn is routinely called twice —
// a defer and an explicit one — and releasing twice would hand one slot to two
// callers at the same ceiling.
func TestClosingAConnectionTwiceReleasesOnce(t *testing.T) {
	e := newFakeRuntime("wasm")
	p, err := NewPool(WithRuntime(e), WithMaxInstances(1))
	require.NoError(t, err)
	defer p.Close()

	c, err := p.Conn(t.Context(), connSpec(), "pg")
	require.NoError(t, err)
	require.NoError(t, c.Close())

	// THE SECOND CLOSE MAY WELL ERROR — a real socket says "use of closed
	// network connection" where a pipe says nothing — and that is not what this
	// is about. What must not happen is a second RELEASE.
	_ = c.Close()

	// If the second Close released again, this one would find a free slot that
	// does not exist and the ceiling would be two.
	held, err := p.Conn(t.Context(), connSpec(), "pg")
	require.NoError(t, err)
	defer held.Close()

	blocked := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		if _, err := p.Conn(ctx, connSpec(), "pg"); err != nil {
			close(blocked)
		}
	}()
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("the ceiling was two, so a release happened twice")
	}
}

// TestADIALThatFailsGivesTheSlotBack rather than leaking it.
func TestAFailedDialReleasesTheSlot(t *testing.T) {
	e := newFakeRuntime("wasm")
	p, err := NewPool(WithRuntime(e))
	require.NoError(t, err)
	defer p.Close()

	_, err = p.Conn(t.Context(), connSpec(), "refused")
	require.Error(t, err)

	// The slot came back, so this proceeds rather than queueing forever.
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	c, err := p.Conn(ctx, connSpec(), "pg")
	require.NoError(t, err)
	c.Close()
}

// TestATIERTHATHANDSOUTNoConnectionSaysSo — a component has no listening
// socket, so asking one for a connection is a question it cannot answer.
func TestATierThatCannotDialIsRefused(t *testing.T) {
	p, err := NewPool(WithRuntime(&handlerOnlyRuntime{name: "container"}))
	require.NoError(t, err)
	defer p.Close()

	_, err = p.Conn(t.Context(), Spec{Runtime: "container", Name: "x", Artifact: "img", Revision: "1"}, "")
	require.ErrorIs(t, err, ErrNoRuntime)
}

// TestAPooledConnectionCanStillBeHALFClosed.
//
// This wrapper embeds the net.Conn INTERFACE, and promotion through an
// interface gives only that interface's methods — so CloseWrite was invisible
// through it and a caller type-asserting for one found nothing. What that cost
// was not obvious: the caller fell back to closing the whole connection, which
// ends the half a query protocol is waiting on. The request was delivered and
// the answer never came.
func TestAPooledConnectionForwardsCloseWrite(t *testing.T) {
	e := newFakeRuntime("wasm")
	p, err := NewPool(WithRuntime(e))
	require.NoError(t, err)
	defer p.Close()

	c, err := p.Conn(t.Context(), connSpec(), "pg")
	require.NoError(t, err)
	defer c.Close()

	cw, ok := c.(interface{ CloseWrite() error })
	require.True(t, ok, "a pooled connection must still look half-closable")
	require.NoError(t, cw.CloseWrite())
}

// TestAndItSaysSoWhenItCannot, rather than reporting a half-close that never
// happened — a caller whose only alternative is a full close needs to know,
// because the far end is otherwise waiting for an EOF nobody sent.
func TestAPooledConnectionReportsAnImpossibleHalfClose(t *testing.T) {
	e := newFakeRuntime("wasm")
	e.pipe = true
	p, err := NewPool(WithRuntime(e))
	require.NoError(t, err)
	defer p.Close()

	c, err := p.Conn(t.Context(), connSpec(), "pg")
	require.NoError(t, err)
	defer c.Close()

	err = c.(interface{ CloseWrite() error }).CloseWrite()
	require.Error(t, err, "net.Pipe cannot half-close and must not pretend to")
}
