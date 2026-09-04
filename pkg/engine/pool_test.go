package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeEngine starts in nanoseconds, so every rule about WHEN things start is a
// fast unit test rather than a container fixture.
type fakeEngine struct {
	name string

	mu       sync.Mutex
	started  int
	closed   int
	live     int
	maxLive  int
	startErr error

	serve func(ctx context.Context, req *Request) (*Response, error)
}

func newFakeEngine(name string) *fakeEngine {
	return &fakeEngine{name: name}
}

func (e *fakeEngine) Name() string { return e.name }

func (e *fakeEngine) Start(ctx context.Context, spec Spec) (Instance, error) {
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
	return &fakeInstance{eng: e, spec: spec}, nil
}

func (e *fakeEngine) counts() (started, closed int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.started, e.closed
}

type fakeInstance struct {
	eng  *fakeEngine
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

	if i.eng.serve != nil {
		return i.eng.serve(ctx, req)
	}
	return &Response{Status: 200, Body: []byte(i.spec.Revision)}, nil
}

func (i *fakeInstance) Close() error {
	i.closedAt.Store(true)
	i.eng.mu.Lock()
	defer i.eng.mu.Unlock()
	i.eng.closed++
	i.eng.live--
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

func newPool(t *testing.T, e Engine, adjust func(*Options)) (*Pool, *testClock) {
	t.Helper()
	c := newTestClock()
	opts := Options{Engines: []Engine{e}, Now: c.now}
	if adjust != nil {
		adjust(&opts)
	}
	p, err := NewPool(opts)
	require.NoError(t, err)
	t.Cleanup(func() { p.Close() })
	return p, c
}

func spec(name, rev string) Spec {
	return Spec{Engine: "fake", Name: name, Revision: rev}
}

// TestAWarmInstanceIsReused — the whole reason a pool exists. The second
// request must not pay a cold start.
func TestAWarmInstanceIsReused(t *testing.T) {
	e := newFakeEngine("fake")
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
	e := newFakeEngine("fake")
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
	e := newFakeEngine("fake")
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
	e := newFakeEngine("fake")
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
	e := newFakeEngine("fake")
	gate := make(chan struct{})
	var served atomic.Int32
	e.serve = func(ctx context.Context, req *Request) (*Response, error) {
		<-gate
		served.Add(1)
		return &Response{Status: 200}, nil
	}
	p, _ := newPool(t, e, func(o *Options) { o.MaxInstances = 2 })

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
	e := newFakeEngine("fake")
	block := make(chan struct{})
	defer close(block)
	e.serve = func(ctx context.Context, req *Request) (*Response, error) {
		<-block
		return &Response{Status: 200}, nil
	}
	p, _ := newPool(t, e, func(o *Options) { o.MaxInstances = 1 })

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
	e := newFakeEngine("fake")
	p, clock := newPool(t, e, func(o *Options) { o.IdleTimeout = time.Minute })
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
	e := newFakeEngine("fake")
	block := make(chan struct{})
	e.serve = func(ctx context.Context, req *Request) (*Response, error) {
		<-block
		return &Response{Status: 200}, nil
	}
	p, clock := newPool(t, e, func(o *Options) { o.IdleTimeout = time.Second })

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
	e := newFakeEngine("fake")
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
	e := newFakeEngine("fake")
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
	e := newFakeEngine("fake")
	e.serve = func(ctx context.Context, req *Request) (*Response, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	p, _ := newPool(t, e, func(o *Options) { o.Timeout = 20 * time.Millisecond })

	_, err := p.Do(context.Background(), spec("shop", "r1"), &Request{})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestAnUnknownEngineIsRefusedRatherThanDefaulted. Running a container spec on
// the wasm engine would be a very confusing failure.
func TestAnUnknownEngineIsRefusedRatherThanDefaulted(t *testing.T) {
	p, _ := newPool(t, newFakeEngine("fake"), nil)

	s := spec("shop", "r1")
	s.Engine = "container"
	_, err := p.Do(context.Background(), s, &Request{})
	require.ErrorIs(t, err, ErrNoEngine)
}

// TestTwoEnginesCoexist — the point of the seam.
func TestTwoEnginesCoexist(t *testing.T) {
	wasm, ctr := newFakeEngine("wasm"), newFakeEngine("container")
	p, err := NewPool(Options{Engines: []Engine{wasm, ctr}})
	require.NoError(t, err)
	t.Cleanup(func() { p.Close() })

	_, err = p.Do(context.Background(), Spec{Engine: "wasm", Name: "fn", Revision: "r1"}, &Request{})
	require.NoError(t, err)
	_, err = p.Do(context.Background(), Spec{Engine: "container", Name: "api", Revision: "r1"}, &Request{})
	require.NoError(t, err)

	ws, _ := wasm.counts()
	cs, _ := ctr.counts()
	require.Equal(t, 1, ws)
	require.Equal(t, 1, cs)
}

func TestAStartFailureReachesTheCaller(t *testing.T) {
	e := newFakeEngine("fake")
	e.startErr = errors.New("image not found")
	p, _ := newPool(t, e, nil)

	_, err := p.Do(context.Background(), spec("shop", "r1"), &Request{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "image not found")
	require.Zero(t, p.Warm(spec("shop", "r1")))
}

func TestASpecMustIdentifyItself(t *testing.T) {
	p, _ := newPool(t, newFakeEngine("fake"), nil)
	ctx := context.Background()

	_, err := p.Do(ctx, Spec{Engine: "fake", Revision: "r1"}, &Request{})
	require.ErrorIs(t, err, ErrNoName)

	_, err = p.Do(ctx, Spec{Engine: "fake", Name: "shop"}, &Request{})
	require.ErrorIs(t, err, ErrNoRevision, "without it a deploy cannot be told from the one before")

	_, err = p.Do(ctx, Spec{Name: "shop", Revision: "r1"}, &Request{})
	require.Error(t, err)

	_, err = p.Do(ctx, Spec{Engine: "fake", Name: "shop", Revision: "r1", Concurrency: -1}, &Request{})
	require.Error(t, err)
}

func TestPoolOptionsAreValidated(t *testing.T) {
	_, err := NewPool(Options{IdleTimeout: -time.Second})
	require.Error(t, err)

	_, err = NewPool(Options{MaxInstances: -1})
	require.Error(t, err)

	_, err = NewPool(Options{Timeout: -time.Second})
	require.Error(t, err)

	_, err = NewPool(Options{Engines: []Engine{nil}})
	require.Error(t, err)

	e := newFakeEngine("fake")
	_, err = NewPool(Options{Engines: []Engine{e, newFakeEngine("fake")}})
	require.Error(t, err, "two engines answering to one name is ambiguous, not a merge")
}

func TestCloseShutsEverythingDownAndIsIdempotent(t *testing.T) {
	e := newFakeEngine("fake")
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
	e := newFakeEngine("fake")
	p, clock := newPool(t, e, func(o *Options) { o.IdleTimeout = time.Millisecond })

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
	p, _ := newPool(t, newFakeEngine("fake"), nil)
	require.Zero(t, p.Warm(spec("never", "r1")))
}

// TestAnInstanceStartedIntoAClosingPoolIsNotLeaked. A cold start happens
// OUTSIDE the pool's lock — deliberately, since holding it across a container
// start would block every other spec on the node — so a Close can land while an
// instance is coming up. The instance that arrives late has to be closed rather
// than parked in a pool nobody will drain.
func TestAnInstanceStartedIntoAClosingPoolIsNotLeaked(t *testing.T) {
	e := newFakeEngine("fake")
	starting := make(chan struct{})
	proceed := make(chan struct{})

	slow := &slowStartEngine{fakeEngine: e, starting: starting, proceed: proceed}
	p, err := NewPool(Options{Engines: []Engine{slow}})
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

type slowStartEngine struct {
	*fakeEngine
	starting chan struct{}
	proceed  chan struct{}
	once     sync.Once
}

func (e *slowStartEngine) Start(ctx context.Context, spec Spec) (Instance, error) {
	e.once.Do(func() { close(e.starting) })
	<-e.proceed
	return e.fakeEngine.Start(ctx, spec)
}

// TestABurstOnAColdSpecStartsOneInstance, not one per request. Without
// single-flight every arrival finds no warm slot and starts its own, so a spec
// whose concurrency could absorb the whole burst from one instance instead
// starts MaxInstances of them — seconds of latency and hundreds of megabytes
// each, once the engine is containers.
func TestABurstOnAColdSpecStartsOneInstance(t *testing.T) {
	e := newFakeEngine("fake")
	slow := make(chan struct{})
	gate := &gatedStartEngine{fakeEngine: e, gate: slow}

	p, err := NewPool(Options{Engines: []Engine{gate}})
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
	e := newFakeEngine("fake")
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
	e := newFakeEngine("fake")
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

type gatedStartEngine struct {
	*fakeEngine
	gate chan struct{}

	mu  sync.Mutex
	inW int
}

func (e *gatedStartEngine) waiting() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.inW
}

func (e *gatedStartEngine) Start(ctx context.Context, spec Spec) (Instance, error) {
	e.mu.Lock()
	e.inW++
	e.mu.Unlock()
	<-e.gate
	return e.fakeEngine.Start(ctx, spec)
}
