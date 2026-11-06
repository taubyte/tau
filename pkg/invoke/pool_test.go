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

func newPool(t testing.TB, e Runtime, adjust func() []Option) (*Pool, *testClock) {
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

// A code push is a new artifact under the same revision: the configuration did not
// change, the code did.
func TestACodePushNeverSharesAnInstance(t *testing.T) {
	e := newFakeRuntime("fake")
	p, _ := newPool(t, e, nil)
	ctx := context.Background()

	built, pushed := spec("shop", "r1"), spec("shop", "r1")
	built.Artifact, pushed.Artifact = "bafy-built", "bafy-pushed"

	_, err := p.Do(ctx, built, &Request{})
	require.NoError(t, err)
	_, err = p.Do(ctx, pushed, &Request{})
	require.NoError(t, err)

	started, _ := e.counts()
	require.Equal(t, 2, started, "the pushed code started an instance of its own")
	require.Equal(t, 1, p.Warm(built), "and the build before it is still warm, for a rollback")
	require.Equal(t, 1, p.Warm(pushed))
}

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

func TestAnUnknownEngineIsRefusedRatherThanDefaulted(t *testing.T) {
	p, _ := newPool(t, newFakeRuntime("fake"), nil)

	s := spec("shop", "r1")
	s.Runtime = "container"
	_, err := p.Do(context.Background(), s, &Request{})
	require.ErrorIs(t, err, ErrNoRuntime)
}

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

func TestEveryPoolOptionRefusesWhatItCannotUse(t *testing.T) {
	type testCase struct {
		name string
		opt  Option
	}
	for _, tc := range []testCase{
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

// A transformer is pooled because it runs on every request (~13ms cold start for a
// small component).

func (i *fakeInstance) Transform(_ context.Context, req *Request) (*Outcome, error) {
	if req.Path == "/reject" {
		return &Outcome{Answer: &Response{Status: 403}}, nil
	}
	forwarded := *req
	forwarded.Path = req.Path + "/shaped"
	return &Outcome{Forward: &forwarded}, nil
}

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

// A connection outlives the call that opened it: its slot returns on close.

func (i *fakeInstance) Dial(_ context.Context, port string) (net.Conn, error) {
	if port == "refused" {
		return nil, errors.New("no such port")
	}
	if i.rt.pipe {
		client, server := net.Pipe()
		go func() { io.Copy(io.Discard, server); server.Close() }()
		return client, nil
	}

	// A real socket, since a pipe cannot half-close.
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

func TestClosingAConnectionTwiceReleasesOnce(t *testing.T) {
	e := newFakeRuntime("wasm")
	p, err := NewPool(WithRuntime(e), WithMaxInstances(1))
	require.NoError(t, err)
	defer p.Close()

	c, err := p.Conn(t.Context(), connSpec(), "pg")
	require.NoError(t, err)
	require.NoError(t, c.Close())

	// The second Close may error; what must not happen is a second release.
	c.Close()

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

func TestATierThatCannotDialIsRefused(t *testing.T) {
	p, err := NewPool(WithRuntime(&handlerOnlyRuntime{name: "container"}))
	require.NoError(t, err)
	defer p.Close()

	_, err = p.Conn(t.Context(), Spec{Runtime: "container", Name: "x", Artifact: "img", Revision: "1"}, "")
	require.ErrorIs(t, err, ErrNoRuntime)
}

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

func TestASettingReadAsANumber(t *testing.T) {
	type testCase struct {
		setting string
		want    int64
	}
	for _, c := range []testCase{
		{"size", 1024},
		{"negative", -1},
		{"zero", 0},
		{"empty", 0},
		{"words", 0},
		{"fractional", 0},
		{"absent", 0},
		{"huge", 0},
	} {
		g := Grant{Settings: map[string]string{
			"size":       "1024",
			"negative":   "-1",
			"zero":       "0",
			"empty":      "",
			"words":      "a lot",
			"fractional": "10.5",
			"huge":       "99999999999999999999",
		}}
		require.Equal(t, c.want, g.Number(c.setting), c.setting)
	}

	// AND A GRANT THAT CARRIES NO SETTINGS AT ALL is the same answer rather than
	// a panic: a refused grant has none, and every backing reads one.
	require.Zero(t, Grant{}.Number("size"))
}

func TestALingeringCallAnswersAtOnceAndHoldsItsSlot(t *testing.T) {
	e := newFakeRuntime("fake")
	settle := make(chan func(bool), 2)
	e.serve = func(ctx context.Context, req *Request) (*Response, error) {
		done, ok := Linger(ctx)
		require.True(t, ok)
		settle <- done
		return &Response{Status: 200}, nil
	}
	p, clock := newPool(t, e, func() []Option {
		return []Option{WithMaxInstances(1), WithIdleTimeout(time.Second)}
	})
	s := spec("shop", "r1")

	resp, err := p.Do(t.Context(), s, &Request{})
	require.NoError(t, err)
	require.Equal(t, 200, resp.Status, "the answer came back while the work it left is still running")
	done := <-settle

	clock.advance(time.Hour)
	require.Zero(t, p.Reap(), "a lingering instance is still in use")

	second := make(chan error, 1)
	go func() {
		_, err := p.Do(t.Context(), s, &Request{})
		second <- err
	}()
	select {
	case <-second:
		t.Fatal("a second request was served while the first one's work held the only slot")
	case <-time.After(150 * time.Millisecond):
	}

	done(false)
	require.NoError(t, <-second, "settling the work handed the slot back")
	(<-settle)(false)

	started, closed := e.counts()
	require.Equal(t, 1, started, "one instance served both")
	require.Zero(t, closed)
}

func TestALingerThatFailsRetiresTheInstance(t *testing.T) {
	e := newFakeRuntime("fake")
	settle := make(chan func(bool), 1)
	e.serve = func(ctx context.Context, req *Request) (*Response, error) {
		done, _ := Linger(ctx)
		settle <- done
		return &Response{Status: 200}, nil
	}
	p, _ := newPool(t, e, nil)
	s := spec("shop", "r1")

	_, err := p.Do(t.Context(), s, &Request{})
	require.NoError(t, err)
	require.Equal(t, 1, p.Warm(s))

	(<-settle)(true)
	require.Eventually(t, func() bool { _, closed := e.counts(); return closed == 1 }, 3*time.Second, time.Millisecond)
	require.Zero(t, p.Warm(s))
}

func TestAFailedCallReleasesWhatItClaimed(t *testing.T) {
	e := newFakeRuntime("fake")
	boom := errors.New("the handler failed after claiming")
	settle := make(chan func(bool), 1)
	e.serve = func(ctx context.Context, req *Request) (*Response, error) {
		done, _ := Linger(ctx)
		settle <- done
		return nil, boom
	}
	p, _ := newPool(t, e, nil)

	_, err := p.Do(t.Context(), spec("shop", "r1"), &Request{})
	require.ErrorIs(t, err, boom)
	_, closed := e.counts()
	require.Equal(t, 1, closed, "retired at once, not when the work settles")

	done := <-settle
	done(false)
	done(true)
	_, closed = e.counts()
	require.Equal(t, 1, closed)
}

func TestLingerNeedsAPooledCall(t *testing.T) {
	done, ok := Linger(t.Context())
	require.False(t, ok)
	require.Nil(t, done)
}

// A context the guest's host derives from its call's still finds the linger, and
// still ends with the call.
type valueKey struct{}

func TestAContextDerivedFromACallStillLingersAndEndsWithIt(t *testing.T) {
	e := newFakeRuntime("fake")
	derived := make(chan context.Context, 1)
	e.serve = func(ctx context.Context, req *Request) (*Response, error) {
		child, cancel := context.WithCancel(context.WithValue(ctx, valueKey{}, "kept"))
		t.Cleanup(cancel)
		_, ok := Linger(child)
		require.True(t, ok, "the linger is reached through the contexts above it")
		require.Equal(t, "kept", child.Value(valueKey{}))
		derived <- child
		return &Response{Status: 200}, nil
	}
	p, _ := newPool(t, e, nil)
	ctx := context.WithValue(t.Context(), valueKey{}, "outer")

	_, err := p.Do(ctx, spec("shop", "r1"), &Request{})
	require.NoError(t, err)
	child := <-derived
	select {
	case <-child.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("a context derived from the call outlived it")
	}
	require.ErrorIs(t, child.Err(), context.Canceled)
}
