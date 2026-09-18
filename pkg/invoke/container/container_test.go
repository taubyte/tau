package container

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/invoke"
)

// THE FAKE RUNTIME'S "CONTAINER" IS A REAL HTTP SERVER ON A REAL PORT.
//
// That is what makes this package testable with no daemon anywhere while still
// exercising the part that actually matters: the engine allocates a port, tells
// the container about it via $PORT, dials until it answers, and proxies a real
// request over a real socket. A fake that returned a canned response would test
// none of that — the addressing scheme IS the design here.

type fakeBackend struct {
	mu sync.Mutex

	created  []*core.ContainerConfig
	started  []core.ContainerID
	stopped  []core.ContainerID
	removed  []core.ContainerID
	statuses map[core.ContainerID]string

	// listen decides what the "container" does when started. nil means it
	// serves 200 with the request echoed.
	listen func(port int) (closer, error)

	createErr error
	startErr  error
	stopErr   error

	// image behaviour
	imagePresent bool
	pulls        int
	pullErr      error

	servers []closer
	next    int
}

// closer is what a fake "container" hands back so a test can shut it down.
type closer interface{ Close() error }

// closerFn is a Close as a function.
type closerFn func() error

func (f closerFn) Close() error { return f() }

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

func newFakeBackend() *fakeBackend {
	return &fakeBackend{statuses: map[core.ContainerID]string{}}
}

func (f *fakeBackend) Create(_ context.Context, cfg *core.ContainerConfig) (core.ContainerID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return "", f.createErr
	}
	f.next++
	id := core.ContainerID(fmt.Sprintf("c%d", f.next))
	f.created = append(f.created, cfg)
	f.statuses[id] = "created"
	return id, nil
}

func (f *fakeBackend) Start(_ context.Context, id core.ContainerID) error {
	f.mu.Lock()
	if f.startErr != nil {
		err := f.startErr
		f.mu.Unlock()
		return err
	}
	f.started = append(f.started, id)
	f.statuses[id] = "running"
	cfg := f.configFor(id)
	listen := f.listen
	f.mu.Unlock()

	if listen == nil {
		listen = echoServer
	}

	// EVERY MAPPED PORT IS BOUND, not just the one in $PORT. A container that
	// declares two ports listens on two, and a fake that bound one would be
	// more permissive than the thing it stands in for in exactly the direction
	// that hides a bug — a caller reaching the wrong socket would still find
	// something answering. That is how the storage double let three bugs
	// through; see the sweep of 2026-09-05.
	bound := map[int]bool{}
	for _, m := range cfg.Network.PortMappings {
		if bound[m.HostPort] {
			continue
		}
		bound[m.HostPort] = true

		srv, err := listen(m.HostPort)
		if err != nil {
			return err
		}
		if srv != nil {
			f.mu.Lock()
			f.servers = append(f.servers, srv)
			f.mu.Unlock()
		}
	}
	return nil
}

// configFor finds the config a container was created with. The caller holds the
// lock. Ids are c1, c2, … in creation order.
func (f *fakeBackend) configFor(id core.ContainerID) *core.ContainerConfig {
	n, _ := strconv.Atoi(strings.TrimPrefix(string(id), "c"))
	if n >= 1 && n <= len(f.created) {
		return f.created[n-1]
	}
	return &core.ContainerConfig{}
}

func (f *fakeBackend) Stop(_ context.Context, id core.ContainerID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, id)
	f.statuses[id] = "exited"
	return f.stopErr
}

func (f *fakeBackend) Remove(_ context.Context, id core.ContainerID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, id)
	return nil
}

func (f *fakeBackend) Inspect(_ context.Context, id core.ContainerID) (*core.ContainerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.statuses[id]
	if !ok {
		return nil, errors.New("no such container")
	}
	return &core.ContainerInfo{ID: id, Status: st, ExitCode: 7}, nil
}

func (f *fakeBackend) Image(name string) core.Image { return &fakeImage{rt: f, name: name} }

func (f *fakeBackend) closeAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.servers {
		s.Close()
	}
	f.servers = nil
}

func (f *fakeBackend) snapshot() (created []*core.ContainerConfig, stopped, removed []core.ContainerID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*core.ContainerConfig(nil), f.created...),
		append([]core.ContainerID(nil), f.stopped...),
		append([]core.ContainerID(nil), f.removed...)
}

type fakeImage struct {
	rt   *fakeBackend
	name string
}

func (i *fakeImage) Pull(context.Context) error {
	i.rt.mu.Lock()
	defer i.rt.mu.Unlock()
	i.rt.pulls++
	if i.rt.pullErr != nil {
		return i.rt.pullErr
	}
	i.rt.imagePresent = true
	return nil
}
func (i *fakeImage) Build(context.Context, *core.DockerfileBuild) error { return nil }
func (i *fakeImage) Exists(context.Context) bool {
	i.rt.mu.Lock()
	defer i.rt.mu.Unlock()
	return i.rt.imagePresent
}
func (i *fakeImage) Remove(context.Context) error { return nil }
func (i *fakeImage) Name() string                 { return i.name }
func (i *fakeImage) Digest(context.Context) (string, error) {
	return "sha256:" + i.name, nil
}
func (i *fakeImage) Tags(context.Context) ([]string, error) { return []string{i.name}, nil }

var _ core.Image = (*fakeImage)(nil)

func portFromEnv(env []string) int {
	for _, kv := range env {
		if strings.HasPrefix(kv, "PORT=") {
			n, _ := strconv.Atoi(strings.TrimPrefix(kv, "PORT="))
			return n
		}
	}
	return 0
}

// echoServer is a "container": it binds the port it was told to use and answers.
func echoServer(port int) (closer, error) {
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Echo-Path", r.URL.Path)
		w.Header().Set("X-Echo-Method", r.Method)
		if v := r.Header.Get("X-Probe"); v != "" {
			w.Header().Set("X-Probe-Seen", v)
		}
		body := make([]byte, r.ContentLength)
		if r.ContentLength > 0 {
			r.Body.Read(body)
		}
		w.WriteHeader(http.StatusTeapot)
		w.Write(body)
	})}
	go srv.Serve(l)
	return closerFunc(func() error { return srv.Close() }), nil
}

func newRuntime(t *testing.T, rt *fakeBackend, img Image, adjust func() []Option) *Runtime {
	t.Helper()
	opts := []Option{WithBackend(rt), WithReadyInterval(2 * time.Millisecond), WithReadyTimeout(3 * time.Second)}
	if adjust != nil {
		opts = append(opts, adjust()...)
	}
	e, err := New(func(invoke.Spec) (Image, error) { return img, nil }, opts...)
	require.NoError(t, err)
	t.Cleanup(rt.closeAll)
	return e
}

func spec() invoke.Spec {
	return invoke.Spec{Runtime: Name, Name: "shop", Revision: "r1", Concurrency: 1}
}

// TestARequestReachesTheContainerAndComesBack, over a real socket, through the
// port the engine chose.
func TestARequestReachesTheContainerAndComesBack(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true
	e := newRuntime(t, rt, Image{Ref: "acme/shop:1"}, nil)

	inst, err := e.Start(context.Background(), spec())
	require.NoError(t, err)
	defer inst.Close()

	resp, err := inst.Serve(context.Background(), &invoke.Request{
		Method:  "POST",
		Path:    "/checkout",
		Headers: map[string][]string{"X-Probe": {"hello"}},
		Body:    []byte("payload"),
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusTeapot, resp.Status)
	require.Equal(t, "payload", string(resp.Body))
	require.Equal(t, "/checkout", http.Header(resp.Headers).Get("X-Echo-Path"))
	require.Equal(t, "POST", http.Header(resp.Headers).Get("X-Echo-Method"))
	require.Equal(t, "hello", http.Header(resp.Headers).Get("X-Probe-Seen"), "headers reach the container")
}

// TestTheEngineTellsTheContainerWhichPortToUse — the whole addressing scheme.
// PORT is the convention every PaaS uses, which is why an imported app needs no
// change.
func TestTheEngineTellsTheContainerWhichPortToUse(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true
	e := newRuntime(t, rt, Image{Ref: "acme/shop:1", Env: map[string]string{"DATABASE_URL": "x"}}, nil)

	inst, err := e.Start(context.Background(), spec())
	require.NoError(t, err)
	defer inst.Close()

	created, _, _ := rt.snapshot()
	require.Len(t, created, 1)
	port := portFromEnv(created[0].Env)
	require.NotZero(t, port, "the container is told the port")
	require.Contains(t, created[0].Env, "DATABASE_URL=x", "and keeps its own environment")

	require.Equal(t, port, created[0].Network.PortMappings[0].HostPort)
	require.Equal(t, port, created[0].Network.PortMappings[0].ContainerPort,
		"the number means the same thing inside and out")
	// THE TIER NAMES NO NETWORK MODE, and that is load-bearing rather than an
	// omission. It used to ask for "host", which docker refuses to combine with
	// restricted egress and containerd refuses to combine with a port mapping —
	// so every container workload failed to create on both, and this package
	// could not see it because the backend here is a fake that accepts
	// anything. What the tier states is the intent; the backend realises it.
	// See backend_reality_test.go, which asks a real daemon.
	require.Empty(t, created[0].Network.Mode,
		"naming a mode asks each backend for something it cannot do")
	require.True(t, created[0].Network.RestrictEgress,
		"and the intent that does travel is: keep it off the node's network")
}

// TestAnImageCannotOverridePort. The engine dials the port it allocated, so a
// container listening elsewhere never becomes ready — a confusing timeout
// instead of a clear refusal.
func TestAnImageCannotOverridePort(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true
	e := newRuntime(t, rt, Image{Ref: "acme/shop:1", Env: map[string]string{"PORT": "9999"}}, nil)

	inst, err := e.Start(context.Background(), spec())
	require.NoError(t, err)
	defer inst.Close()

	created, _, _ := rt.snapshot()
	var ports []string
	for _, kv := range created[0].Env {
		if strings.HasPrefix(kv, "PORT=") {
			ports = append(ports, kv)
		}
	}
	require.Len(t, ports, 1, "exactly one PORT, and it is the engine's")
	require.NotEqual(t, "PORT=9999", ports[0])
}

// TestUntrustedCodeGetsConfinedByDefault. These are set here rather than left to
// a backend default, and they are NOT copied from pkg/builder, which runs a
// repository's own script under a different threat model.
func TestUntrustedCodeGetsConfinedByDefault(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true
	e := newRuntime(t, rt, Image{Ref: "acme/shop:1", Memory: 256 << 20}, nil)

	inst, err := e.Start(context.Background(), spec())
	require.NoError(t, err)
	defer inst.Close()

	created, _, _ := rt.snapshot()
	require.True(t, created[0].Network.RestrictEgress,
		"a tenant's code must not reach the metadata endpoint or the private network")
	require.Nil(t, created[0].Privileges,
		"no capability, no host device, no unconfined profile — the build path needs those, this does not")
	require.NotNil(t, created[0].Resources)
	require.Equal(t, int64(256<<20), created[0].Resources.Memory)
}

// TestStartWaitsUntilTheContainerIsListening. A container that has STARTED is
// not one that is LISTENING, and returning early hands the first request to a
// socket nobody is on.
func TestStartWaitsUntilTheContainerIsListening(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true

	var bound sync.WaitGroup
	bound.Add(1)
	rt.listen = func(port int) (closer, error) {
		// Bind late, as a real runtime would.
		var srv closer
		var err error
		go func() {
			time.Sleep(120 * time.Millisecond)
			srv, err = echoServer(port)
			bound.Done()
		}()
		bound.Wait()
		return srv, err
	}

	e := newRuntime(t, rt, Image{Ref: "acme/shop:1"}, nil)
	start := time.Now()
	inst, err := e.Start(context.Background(), spec())
	require.NoError(t, err)
	defer inst.Close()

	require.GreaterOrEqual(t, time.Since(start), 100*time.Millisecond,
		"Start did not return before the container was listening")

	resp, err := inst.Serve(context.Background(), &invoke.Request{Path: "/"})
	require.NoError(t, err)
	require.Equal(t, http.StatusTeapot, resp.Status)
}

// TestAContainerThatNeverListensFailsWithinTheBudget rather than hanging.
func TestAContainerThatNeverListensFailsWithinTheBudget(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true
	rt.listen = func(int) (closer, error) { return nil, nil } // starts, binds nothing

	e := newRuntime(t, rt, Image{Ref: "acme/shop:1"}, func() []Option { return []Option{WithReadyTimeout(150 * time.Millisecond)} })

	_, err := e.Start(context.Background(), spec())
	require.ErrorIs(t, err, ErrNotReady)

	_, stopped, removed := rt.snapshot()
	require.Len(t, stopped, 1, "and the container it gave up on is cleaned up")
	require.Len(t, removed, 1)
}

// TestAContainerThatExitsIsReportedAsExitedNotAsSlow. An entrypoint that dies
// immediately would otherwise burn the whole ready budget and then say the wrong
// thing.
func TestAContainerThatExitsIsReportedAsExitedNotAsSlow(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true
	rt.listen = func(int) (closer, error) { return nil, nil }

	// A long budget, to prove it does not wait the whole thing out.
	e := newRuntime(t, rt, Image{Ref: "acme/shop:1"}, func() []Option {
		return []Option{WithReadyTimeout(10 * time.Second)}
	})

	// The container is running when Start begins, and dies right after.
	go func() {
		time.Sleep(20 * time.Millisecond)
		rt.mu.Lock()
		for id := range rt.statuses {
			rt.statuses[id] = "exited"
		}
		rt.mu.Unlock()
	}()

	start := time.Now()
	_, err := e.Start(context.Background(), spec())
	require.ErrorIs(t, err, ErrExited)
	require.Contains(t, err.Error(), "exit code 7", "the exit code is what somebody debugging needs")
	require.Less(t, time.Since(start), 3*time.Second, "it did not wait out the ready budget")
}

// TestEachInstanceGetsItsOwnPort. The pool starts N instances of one spec for
// concurrency; two containers on one port is the bug this prevents.
func TestEachInstanceGetsItsOwnPort(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true
	e := newRuntime(t, rt, Image{Ref: "acme/shop:1"}, nil)

	a, err := e.Start(context.Background(), spec())
	require.NoError(t, err)
	defer a.Close()
	b, err := e.Start(context.Background(), spec())
	require.NoError(t, err)
	defer b.Close()

	created, _, _ := rt.snapshot()
	require.Len(t, created, 2)
	require.NotEqual(t, portFromEnv(created[0].Env), portFromEnv(created[1].Env))
}

// TestAnAbsentImageIsPulled, and a present one is not.
func TestAnAbsentImageIsPulled(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = false
	e := newRuntime(t, rt, Image{Ref: "acme/shop:1"}, nil)

	inst, err := e.Start(context.Background(), spec())
	require.NoError(t, err)
	defer inst.Close()

	rt.mu.Lock()
	pulls := rt.pulls
	rt.mu.Unlock()
	require.Equal(t, 1, pulls, "a genuinely cold node has to pull")

	inst2, err := e.Start(context.Background(), spec())
	require.NoError(t, err)
	defer inst2.Close()

	rt.mu.Lock()
	pulls = rt.pulls
	rt.mu.Unlock()
	require.Equal(t, 1, pulls, "and does not pull again once it is there")
}

func TestAFailedPullReachesTheCaller(t *testing.T) {
	rt := newFakeBackend()
	rt.pullErr = errors.New("manifest unknown")
	e := newRuntime(t, rt, Image{Ref: "acme/nope:1"}, nil)

	_, err := e.Start(context.Background(), spec())
	require.Error(t, err)
	require.Contains(t, err.Error(), "manifest unknown")
}

func TestCloseStopsAndRemoves(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true
	e := newRuntime(t, rt, Image{Ref: "acme/shop:1"}, nil)

	inst, err := e.Start(context.Background(), spec())
	require.NoError(t, err)

	require.NoError(t, inst.Close())
	_, stopped, removed := rt.snapshot()
	require.Len(t, stopped, 1)
	require.Len(t, removed, 1)

	require.NoError(t, inst.Close(), "closing twice is not an error")

	_, err = inst.Serve(context.Background(), &invoke.Request{Path: "/"})
	require.ErrorIs(t, err, ErrClosed)
}

// TestRemoveHappensEvenWhenStopFails. A node that left a container behind every
// time a stop timed out would fill its disk — and the pool retires instances
// routinely, since every failed request is one.
func TestRemoveHappensEvenWhenStopFails(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true
	rt.stopErr = errors.New("container is wedged")
	e := newRuntime(t, rt, Image{Ref: "acme/shop:1"}, nil)

	inst, err := e.Start(context.Background(), spec())
	require.NoError(t, err)

	err = inst.Close()
	require.Error(t, err, "the stop failure is reported")
	_, _, removed := rt.snapshot()
	require.Len(t, removed, 1, "and it is removed anyway")
}

func TestStartFailuresAreReported(t *testing.T) {
	t.Run("no image ref", func(t *testing.T) {
		rt := newFakeBackend()
		e := newRuntime(t, rt, Image{}, nil)
		_, err := e.Start(context.Background(), spec())
		require.ErrorIs(t, err, ErrNoImage)
	})

	t.Run("create failed", func(t *testing.T) {
		rt := newFakeBackend()
		rt.imagePresent = true
		rt.createErr = errors.New("no such image")
		e := newRuntime(t, rt, Image{Ref: "acme/shop:1"}, nil)
		_, err := e.Start(context.Background(), spec())
		require.Error(t, err)
		require.Contains(t, err.Error(), "no such image")
	})

	t.Run("start failed", func(t *testing.T) {
		rt := newFakeBackend()
		rt.imagePresent = true
		rt.startErr = errors.New("port in use")
		e := newRuntime(t, rt, Image{Ref: "acme/shop:1"}, nil)
		_, err := e.Start(context.Background(), spec())
		require.Error(t, err)
		require.Contains(t, err.Error(), "port in use")

		_, _, removed := rt.snapshot()
		require.Len(t, removed, 1, "a container that would not start is not left behind")
	})

	t.Run("the image cannot be resolved", func(t *testing.T) {
		rt := newFakeBackend()
		e, err := New(func(invoke.Spec) (Image, error) {
			return Image{}, errors.New("no build for this revision")
		}, WithBackend(rt))
		require.NoError(t, err)
		_, err = e.Start(context.Background(), spec())
		require.Error(t, err)
		require.Contains(t, err.Error(), "no build for this revision")
	})

	t.Run("port allocation failed", func(t *testing.T) {
		rt := newFakeBackend()
		rt.imagePresent = true
		e := newRuntime(t, rt, Image{Ref: "acme/shop:1"}, func() []Option {
			return []Option{WithPortAllocator(func() (int, error) {
				return 0, errors.New("no ports left")
			})}
		})
		_, err := e.Start(context.Background(), spec())
		require.Error(t, err)
		require.Contains(t, err.Error(), "no ports left")
	})
}

func TestOptionsAreValidated(t *testing.T) {
	_, err := New(func(invoke.Spec) (Image, error) { return Image{}, nil })
	require.ErrorIs(t, err, ErrNoBackend)

	_, err = New(nil, WithBackend(newFakeBackend()))
	require.Error(t, err, "an engine that cannot say what to run is not an engine")

	_, err = New(func(invoke.Spec) (Image, error) { return Image{}, nil },
		WithBackend(newFakeBackend()), WithReadyTimeout(-time.Second))
	require.Error(t, err)

	e, err := New(func(invoke.Spec) (Image, error) { return Image{}, nil },
		WithBackend(newFakeBackend()))
	require.NoError(t, err)
	require.Equal(t, DefaultReadyTimeout, e.opts.readyTimeout)
	require.Equal(t, Name, e.Name())
}

// TestItIsAnEngineThePoolCanUse — the point of the whole package. This is the
// pool's warm-reuse, concurrency and scale-to-zero running over real containers.
func TestItIsAnEngineThePoolCanUse(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true
	e := newRuntime(t, rt, Image{Ref: "acme/shop:1"}, nil)

	pool, err := invoke.NewPool(invoke.WithRuntime(e))
	require.NoError(t, err)
	defer pool.Close()

	s := spec()
	for range 3 {
		resp, err := pool.Do(context.Background(), s, &invoke.Request{Path: "/ping"})
		require.NoError(t, err)
		require.Equal(t, http.StatusTeapot, resp.Status)
	}

	created, _, _ := rt.snapshot()
	require.Len(t, created, 1, "three requests, one container: the pool kept it warm")
	require.Equal(t, 1, pool.Warm(s))
}

// TestTheFreePortAllocatorReturnsSomethingBindable — the production path, which
// no other test uses because they all inject.
func TestTheFreePortAllocatorReturnsSomethingBindable(t *testing.T) {
	p, err := freePort()
	require.NoError(t, err)
	require.NotZero(t, p)

	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
	require.NoError(t, err, "the port it handed back is actually free")
	l.Close()
}

// TestEveryOptionRefusesWhatItCannotUse. An option that silently accepted a
// nonsense value would push the failure to the first request, where it reads as
// a runtime fault rather than a configuration one.
func TestEveryOptionRefusesWhatItCannotUse(t *testing.T) {
	resolve := func(invoke.Spec) (Image, error) { return Image{}, nil }

	for _, tc := range []struct {
		name string
		opt  Option
	}{
		{"a nil backend", WithBackend(nil)},
		{"a ready timeout of zero", WithReadyTimeout(0)},
		{"a negative ready timeout", WithReadyTimeout(-time.Second)},
		{"a negative ready interval", WithReadyInterval(-time.Second)},
		{"a negative stop timeout", WithStopTimeout(-time.Second)},
		{"a nil port allocator", WithPortAllocator(nil)},
		{"a nil dialer", WithDialer(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(resolve, WithBackend(newFakeBackend()), tc.opt)
			require.Error(t, err)
		})
	}
}

func TestTheOptionsThatAreAcceptedTakeEffect(t *testing.T) {
	rt := newFakeBackend()
	e, err := New(func(invoke.Spec) (Image, error) { return Image{}, nil },
		WithBackend(rt),
		WithReadyTimeout(11*time.Second),
		WithReadyInterval(12*time.Millisecond),
		WithStopTimeout(13*time.Second),
		WithDialer(func(ctx context.Context, network, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		}),
	)
	require.NoError(t, err)
	require.Equal(t, 11*time.Second, e.opts.readyTimeout)
	require.Equal(t, 12*time.Millisecond, e.opts.readyInterval)
	require.Equal(t, 13*time.Second, e.opts.stopTimeout)
	require.NotNil(t, e.opts.dial)
}

// THE VOCABULARY PROMISES MORE THAN THIS TIER DELIVERS, and that is worth a
// line in an operator's log rather than silence: `egress` is a per-workload
// allowlist layered on the node-wide deny floor, and what a container gets is
// the floor and then the whole public internet.
func TestAnEgressAllowlistIsReportedRatherThanEnforced(t *testing.T) {
	var said []string
	e := newRuntime(t, newFakeBackend(), Image{Ref: "ghcr.io/acme/api:1.4"}, func() []Option {
		return []Option{WithWarn(func(format string, args ...any) {
			said = append(said, fmt.Sprintf(format, args...))
		})}
	})

	_, _ = e.Start(t.Context(), invoke.Spec{
		Name:     "shop/prod/application/api",
		Artifact: "ghcr.io/acme/api:1.4",
		Egress:   []string{"api.stripe.com", "hooks.slack.com"},
	})

	require.Len(t, said, 1)
	require.Contains(t, said[0], "shop/prod/application/api")
	require.Contains(t, said[0], "api.stripe.com")
	require.Contains(t, said[0], "hooks.slack.com")
	require.Contains(t, said[0], "cannot narrow")
}

// A resource that declared none is not warned about, because there is nothing
// it asked for that it did not get.
func TestNoAllowlistIsNotWarnedAbout(t *testing.T) {
	var said []string
	e := newRuntime(t, newFakeBackend(), Image{Ref: "ghcr.io/acme/api:1.4"}, func() []Option {
		return []Option{WithWarn(func(format string, args ...any) {
			said = append(said, fmt.Sprintf(format, args...))
		})}
	})

	_, _ = e.Start(t.Context(), invoke.Spec{Name: "fn", Artifact: "ghcr.io/acme/api:1.4"})
	require.Empty(t, said)
}

func TestWithWarnRefusesNil(t *testing.T) {
	_, err := New(ImageFromSpec, WithWarn(nil))
	require.Error(t, err)
}

// ── declared ports ───────────────────────────────────────────────────────

// A CONTAINER THAT BINDS A FIXED PORT IS THE ORDINARY CASE AND IT DID NOT WORK.
// The tier allocated one ephemeral port, told the workload about it in $PORT and
// mapped that number to ITSELF — which serves a program written to be told where
// to listen and serves nothing else. postgres binds 5432 because that is what
// postgres does, and every off-the-shelf image anybody would import is in that
// second group.

// TestADeclaredPortIsMappedToWhatTheContainerActuallyBinds.
func TestADeclaredPortIsTranslated(t *testing.T) {
	backend := newFakeBackend()
	e := newRuntime(t, backend, Image{Ref: "ghcr.io/acme/postgres:16"}, nil)

	_, _ = e.Start(t.Context(), invoke.Spec{
		Name:     "shop/prod/application/db",
		Artifact: "ghcr.io/acme/postgres:16",
		Ports:    map[string]int{"pg": 5432},
	})

	created, _, _ := backend.snapshot()
	require.Len(t, created, 1)
	mapped := created[0].Network.PortMappings
	require.Len(t, mapped, 1)
	require.Equal(t, 5432, mapped[0].ContainerPort, "what postgres is actually listening on")
	require.NotZero(t, mapped[0].HostPort)
	require.NotEqual(t, 5432, mapped[0].HostPort,
		"the host side is allocated, so two workloads that both bind 5432 do not collide")
}

// TestEVERYDeclaredPortIsMapped, not just one — a workload listening on two is
// reachable on two.
func TestEveryDeclaredPortIsMapped(t *testing.T) {
	backend := newFakeBackend()
	e := newRuntime(t, backend, Image{Ref: "ghcr.io/acme/many:1"}, nil)

	_, _ = e.Start(t.Context(), invoke.Spec{
		Name:     "shop/prod/application/many",
		Artifact: "ghcr.io/acme/many:1",
		Ports:    map[string]int{"http": 8080, "admin": 9090, "metrics": 9100},
	})

	created, _, _ := backend.snapshot()
	require.Len(t, created, 1)

	inside := map[int]bool{}
	hosts := map[int]bool{}
	for _, m := range created[0].Network.PortMappings {
		inside[m.ContainerPort] = true
		require.False(t, hosts[m.HostPort], "two ports were given the same host side")
		hosts[m.HostPort] = true
	}
	require.Equal(t, map[int]bool{8080: true, 9090: true, 9100: true}, inside)
}

// TestTheMAPPINGDoesNotDependOnAMapWalk. Two starts of one spec allocating host
// ports in a different order would make a log read as if something had changed.
func TestTheMappingIsOrderedByName(t *testing.T) {
	backend := newFakeBackend()
	e := newRuntime(t, backend, Image{Ref: "ghcr.io/acme/many:1"}, nil)

	spec := invoke.Spec{
		Name:     "shop/prod/application/many",
		Artifact: "ghcr.io/acme/many:1",
		Ports:    map[string]int{"http": 8080, "admin": 9090, "metrics": 9100},
	}
	_, _ = e.Start(t.Context(), spec)
	_, _ = e.Start(t.Context(), spec)

	created, _, _ := backend.snapshot()
	require.Len(t, created, 2)

	order := func(c *core.ContainerConfig) []int {
		var out []int
		for _, m := range c.Network.PortMappings {
			out = append(out, m.ContainerPort)
		}
		return out
	}
	// admin, http, metrics — by NAME, so 9090 first.
	require.Equal(t, []int{9090, 8080, 9100}, order(created[0]))
	require.Equal(t, order(created[0]), order(created[1]))
}

// TestAWorkloadThatDECLARESNoneKeepsThePORTContract, which is still right for
// something built for this platform: it is told a number and binds it.
func TestNoDeclaredPortsKeepsTheEphemeralContract(t *testing.T) {
	backend := newFakeBackend()
	e := newRuntime(t, backend, Image{Ref: "ghcr.io/acme/api:1.4"}, nil)

	_, _ = e.Start(t.Context(), invoke.Spec{Name: "fn", Artifact: "ghcr.io/acme/api:1.4"})

	created, _, _ := backend.snapshot()
	require.Len(t, created, 1)
	mapped := created[0].Network.PortMappings
	require.Len(t, mapped, 1)
	require.Equal(t, mapped[0].HostPort, mapped[0].ContainerPort,
		"nothing was declared, so the number it is told is the number it is reached on")
	require.Equal(t, mapped[0].HostPort, portFromEnv(created[0].Env))
}

// TestPORTIsStillSetWhenPortsAreDeclared, because an image that reads it is not
// made to stop by an author declaring where it listens.
func TestPortIsSetToADeclaredPortsHostSide(t *testing.T) {
	backend := newFakeBackend()
	e := newRuntime(t, backend, Image{Ref: "ghcr.io/acme/postgres:16"}, nil)

	_, _ = e.Start(t.Context(), invoke.Spec{
		Name: "db", Artifact: "ghcr.io/acme/postgres:16",
		Ports: map[string]int{"pg": 5432},
	})

	created, _, _ := backend.snapshot()
	require.Equal(t, created[0].Network.PortMappings[0].HostPort, portFromEnv(created[0].Env))
}

// ── dialling a named port ────────────────────────────────────────────────

// A PROXY CARRIES BYTES IT CANNOT PARSE, for as long as the client holds the
// socket open, so it needs the socket rather than a round-trip. Serve buffers
// both halves and answers a request; Dial hands over the connection.

// TestDialReachesTheNamedPortAndNotWhicheverSortedFirst.
func TestDialReachesTheNamedPort(t *testing.T) {
	backend := newFakeBackend()
	// Two ports, and the one we want is NOT the first by name: `admin` sorts
	// before `pg`, so a Dial that ignored the name would reach the wrong one.
	backend.listen = func(port int) (closer, error) { return echoOn(port, "listener-"+strconv.Itoa(port)) }
	e := newRuntime(t, backend, Image{Ref: "ghcr.io/acme/db:16"}, nil)

	inst, err := e.Start(t.Context(), invoke.Spec{
		Name: "db", Artifact: "ghcr.io/acme/db:16",
		Ports: map[string]int{"pg": 5432, "admin": 9090},
	})
	require.NoError(t, err)
	defer inst.Close()

	dials, ok := inst.(invoke.Dials)
	require.True(t, ok, "a container instance dials")

	created, _, _ := backend.snapshot()
	want := map[int]int{}
	for _, m := range created[0].Network.PortMappings {
		want[m.ContainerPort] = m.HostPort
	}

	conn, err := dials.Dial(t.Context(), "pg")
	require.NoError(t, err)
	defer conn.Close()
	require.Equal(t, want[5432], conn.RemoteAddr().(*net.TCPAddr).Port,
		"the host side of the port named pg, not the one that sorted first")
}

// TestDialWithNoNameReachesTheONEPortAWorkloadThatDeclaredNoneWasGiven, which
// keeps a proxy with no `forward.port` working against a $PORT-style workload.
func TestDialWithNoNameReachesTheOnlyPort(t *testing.T) {
	backend := newFakeBackend()
	e := newRuntime(t, backend, Image{Ref: "ghcr.io/acme/api:1.4"}, nil)

	inst, err := e.Start(t.Context(), invoke.Spec{Name: "fn", Artifact: "ghcr.io/acme/api:1.4"})
	require.NoError(t, err)
	defer inst.Close()

	created, _, _ := backend.snapshot()
	conn, err := inst.(invoke.Dials).Dial(t.Context(), "")
	require.NoError(t, err)
	defer conn.Close()
	require.Equal(t, portFromEnv(created[0].Env), conn.RemoteAddr().(*net.TCPAddr).Port)
}

// TestANameTHATWASNEVERDECLAREDSaysSo rather than connecting to something else.
func TestDialRefusesAnUndeclaredPort(t *testing.T) {
	e := newRuntime(t, newFakeBackend(), Image{Ref: "ghcr.io/acme/db:16"}, nil)

	inst, err := e.Start(t.Context(), invoke.Spec{
		Name: "db", Artifact: "ghcr.io/acme/db:16", Ports: map[string]int{"pg": 5432},
	})
	require.NoError(t, err)
	defer inst.Close()

	_, err = inst.(invoke.Dials).Dial(t.Context(), "nope")
	require.ErrorIs(t, err, ErrNoSuchPort)
	require.ErrorContains(t, err, "db", "and says which workload")
}

// echoOn binds one port and answers every connection with a fixed line, so a
// test can tell which socket it reached.
func echoOn(port int, say string) (closer, error) {
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { c.Write([]byte(say)); c.Close() }()
		}
	}()
	return closerFn(func() error { return l.Close() }), nil
}
