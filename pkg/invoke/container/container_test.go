package container

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/invoke"
)

// The fake backend's "container" is a real HTTP server on a real port, so the
// allocate, $PORT, dial and proxy path is exercised for real.

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

	// run is what a run-to-completion "container" does with its stdin: what it
	// writes and how it exits. Set, it replaces listening for every start.
	run   func(cfg *core.ContainerConfig, stdin []byte) (stdout, stderr string, code int)
	ended map[core.ContainerID]ran
	// block, set, holds Wait until the context ends: a run that never finishes.
	block bool
}

// ran is how a fake run ended.
type ran struct {
	stdout, stderr string
	code           int
}

// publishOnly holds a loopback port the way a published port with nothing
// listening behind it is held: bound, so nothing else can take it, and not
// listening, so a dial is refused.
func publishOnly(port int) (closer, error) {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, err
	}
	if err := syscall.Bind(fd, &syscall.SockaddrInet4{Port: port, Addr: [4]byte{127, 0, 0, 1}}); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("port %d is already allocated: %w", port, err)
	}
	return closerFunc(func() error { return syscall.Close(fd) }), nil
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
	run := f.run
	f.mu.Unlock()

	if run != nil {
		var stdin []byte
		if cfg.Stdin != nil {
			stdin, _ = io.ReadAll(cfg.Stdin)
		}
		stdout, stderr, code := run(cfg, stdin)
		f.mu.Lock()
		if f.ended == nil {
			f.ended = map[core.ContainerID]ran{}
		}
		f.ended[id] = ran{stdout, stderr, code}
		if !f.block {
			f.statuses[id] = "exited"
		}
		f.mu.Unlock()
		return nil
	}

	if listen == nil {
		listen = echoServer
	}

	// Every mapped port is bound, so a caller reaching the wrong socket finds nothing.
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
		// A container that binds nothing still has its port held, as docker's proxy does,
		// so no other process can answer the probe.
		if srv == nil {
			if srv, err = publishOnly(m.HostPort); err != nil {
				return err
			}
		} else if held, err := publishOnly(m.HostPort); err == nil {
			inner := srv
			srv = closerFunc(func() error { return errors.Join(inner.Close(), held.Close()) })
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
	code := 7
	if r, ok := f.ended[id]; ok {
		code = r.code
	}
	return &core.ContainerInfo{ID: id, Status: st, ExitCode: code}, nil
}

func (f *fakeBackend) Wait(ctx context.Context, id core.ContainerID) error {
	f.mu.Lock()
	block := f.block
	f.mu.Unlock()
	if block {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

// Logs frames a run's output the way both real backends do.
func (f *fakeBackend) Logs(_ context.Context, id core.ContainerID) (io.ReadCloser, error) {
	f.mu.Lock()
	r := f.ended[id]
	f.mu.Unlock()
	var buf bytes.Buffer
	frame(&buf, stdcopy.Stdout, r.stdout)
	frame(&buf, stdcopy.Stderr, r.stderr)
	return io.NopCloser(&buf), nil
}

// frame writes one docker log frame: the stream, three zero bytes, the length.
func frame(w *bytes.Buffer, stream stdcopy.StdType, payload string) {
	if payload == "" {
		return
	}
	header := [8]byte{byte(stream)}
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	w.Write(header[:])
	w.WriteString(payload)
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
	// The tier names no network mode (backend_reality_test.go asks a real daemon).
	require.Empty(t, created[0].Network.Mode,
		"naming a mode asks each backend for something it cannot do")
	require.True(t, created[0].Network.RestrictEgress,
		"and the intent that does travel is: keep it off the node's network")
}

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

func TestTheFreePortAllocatorReturnsSomethingBindable(t *testing.T) {
	p, err := freePort()
	require.NoError(t, err)
	require.NotZero(t, p)

	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
	require.NoError(t, err, "the port it handed back is actually free")
	l.Close()
}

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

// A container binding a fixed port, like postgres on 5432, must be mapped to what
// it actually binds.

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

// A proxy needs the socket, not a round trip.

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
