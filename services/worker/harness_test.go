package worker

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/register"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"

	"github.com/taubyte/tau/pkg/proto/gen/tau/worker/v1/workerv1connect"
)

// harness is a worker over a real register holding a really-compiled project,
// with a runtime that records what it was asked to start.
//
// THE PROJECT IS COMPILED BY tcc, not written by hand. What this service reads
// is the compiled vocabulary, so a fixture of my own would be a fixture of what
// I believe that vocabulary produces — and it would keep passing on the day it
// produced something else.
//
// The one thing that is fake is the tier. Starting a real container to observe
// that a memory limit reached it would be testing pkg/containers, and the
// property here is that the number came off the config and arrived intact.
type harness struct {
	Worker   *Service
	Register *countingRegister
	Runtime  *fakeRuntime
	Client   workerv1connect.WorkerServiceClient

	clock *fakeClock
}

// newHarness is a fully composed node: the fixture project needs both seams —
// `worker_pool` and every function are built from the repository rather than
// naming an image, and `api` has a variable whose value is a secret — so a
// worker with neither resolver can run almost nothing in it. Tests about the
// refusals use newBareHarness.
func newHarness(t testing.TB, opts ...Option) *harness {
	t.Helper()
	return newHarnessWith(t, Config{}, opts...)
}

// resolvers are the two seams a fully composed node fills in.
func resolvers() []Option {
	return []Option{
		WithArtifacts(artifactFunc(func(_ context.Context, project, environment, kind, id string) (string, error) {
			return "registry/" + project + "/" + environment + "/" + kind + "@sha256:" + id, nil
		})),
		WithSecrets(secretFunc(func(_ context.Context, _, _, id string) (string, error) {
			return "sk_live_" + id[:6], nil
		})),
	}
}

func newHarnessWith(t testing.TB, cfg Config, opts ...Option) *harness {
	t.Helper()
	return build(t, cfg, append(resolvers(), opts...)...)
}

// newBareHarness has neither resolver, so a resource that needs one is refused.
func newBareHarness(t testing.TB, opts ...Option) *harness {
	t.Helper()
	return build(t, Config{}, opts...)
}

func build(t testing.TB, cfg Config, opts ...Option) *harness {
	t.Helper()

	store := register.NewMemStore()
	reg, err := register.New(register.WithStore(store))
	require.NoError(t, err)

	_, err = reg.Publish(t.Context(), register.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled(t),
	})
	require.NoError(t, err)

	counting := &countingRegister{Register: reg}
	rt := &fakeRuntime{name: "container"}
	wasm := &fakeRuntime{name: "wasm"}

	pool, err := invoke.NewPool(invoke.WithRuntime(rt, wasm))
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })

	clock := &fakeClock{at: time.Unix(1_700_000_000, 0)}

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	server := newHost(t, mn)
	caller := newHost(t, mn)

	s, err := New(server, cfg, append([]Option{
		WithRegister(counting),
		WithPool(pool),
		WithClock(clock.Now),
	}, opts...)...)
	require.NoError(t, err)
	require.NoError(t, s.Start(t.Context()))
	t.Cleanup(func() { s.Close() })

	c, err := rpc.NewClient(caller, server.ID(), Protocol)
	require.NoError(t, err)
	t.Cleanup(c.Close)

	// Both tiers record into the same place, so a test asserts on what was
	// started without knowing which tier the config chose.
	wasm.shared = rt

	return &harness{
		Worker:   s,
		Register: counting,
		Runtime:  rt,
		Client:   workerv1connect.NewWorkerServiceClient(c.HTTP(), c.URL()),
		clock:    clock,
	}
}

func newHost(t testing.TB, mn mocknet.Mocknet) host.Host {
	t.Helper()
	h, err := mn.GenPeer()
	require.NoError(t, err)
	require.NoError(t, mn.LinkAll())
	return h
}

// compiled is tcc compiling the vocabulary's own fixture, marshalled the way
// the build service publishes it.
func compiled(t testing.TB) []byte {
	t.Helper()

	c, err := schema.New(
		schema.WithLocal("../../pkg/tcc/taubyte/v2/fixtures/basic"),
		schema.WithBranch("main"))
	require.NoError(t, err)

	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err)

	b, err := json.Marshal(obj.Flat())
	require.NoError(t, err)
	return b
}

// countingRegister is the register with a tally, so a test can say how many
// times a whole compiled project crossed the wire.
type countingRegister struct {
	*register.Register

	mu       sync.Mutex
	resolves int
	objects  int
	broken   bool
}

func (r *countingRegister) breakObjects(broken bool) {
	r.mu.Lock()
	r.broken = broken
	r.mu.Unlock()
}

func (r *countingRegister) Resolve(ctx context.Context, project, environment string) (register.Pointer, error) {
	r.mu.Lock()
	r.resolves++
	r.mu.Unlock()
	return r.Register.Resolve(ctx, project, environment)
}

func (r *countingRegister) Object(ctx context.Context, id register.ObjectID) ([]byte, error) {
	r.mu.Lock()
	r.objects++
	broken := r.broken
	r.mu.Unlock()
	if broken {
		return nil, errUnreachable
	}
	return r.Register.Object(ctx, id)
}

var errUnreachable = errors.New("the register is unreachable")

func (r *countingRegister) counts() (resolves, objects int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.resolves, r.objects
}

// fakeRuntime records what it was asked to start and answers with the spec, so
// a test reads back exactly what the config produced.
type fakeRuntime struct {
	name string

	// shared points a second tier at the first one's record, so both tiers'
	// starts land in one place.
	shared *fakeRuntime

	mu      sync.Mutex
	started []invoke.Spec
}

func (r *fakeRuntime) Name() string { return r.name }

func (r *fakeRuntime) Start(_ context.Context, spec invoke.Spec) (invoke.Instance, error) {
	sink := r
	if r.shared != nil {
		sink = r.shared
	}
	sink.mu.Lock()
	sink.started = append(sink.started, spec)
	sink.mu.Unlock()
	return &fakeInstance{spec: spec}, nil
}

func (r *fakeRuntime) specs() []invoke.Spec {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]invoke.Spec(nil), r.started...)
}

type fakeInstance struct{ spec invoke.Spec }

func (i *fakeInstance) Serve(_ context.Context, req *invoke.Request) (*invoke.Response, error) {
	body, _ := json.Marshal(i.spec)
	return &invoke.Response{
		Status:  200,
		Headers: map[string][]string{"X-Path": {req.Path}},
		Body:    body,
	}, nil
}

func (i *fakeInstance) Close() error { return nil }

type fakeClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(d)
	c.mu.Unlock()
}
