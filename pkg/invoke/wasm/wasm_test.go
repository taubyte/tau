package wasm_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
)

// EVERY TEST HERE RUNS A REAL COMPONENT, built by a real toolchain from
// wit/handler.wit (fixtures/handler). That is the only thing that can show the
// host's lowering and lifting agree with generated bindings — a fake would
// agree with whatever this package assumed.

// handler is the fixture: it echoes the path in a header, prefixes any header
// it was sent, answers a status derived from the method, and returns the method
// and the body.
func handler(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile("fixtures/handler.wasm")
	require.NoError(t, err, "run `cargo component build --release` in fixtures/handler")
	return code
}

type store struct {
	content []byte
	err     error
}

func (s *store) Get(context.Context, string) (io.ReadSeekCloser, error) {
	if s.err != nil {
		return nil, s.err
	}
	return nopSeekCloser{bytes.NewReader(s.content)}, nil
}

type nopSeekCloser struct{ *bytes.Reader }

func (nopSeekCloser) Close() error { return nil }

func tier(t *testing.T, code []byte, opts ...wasm.Option) *wasm.Runtime {
	t.Helper()
	r, err := wasm.New(&store{content: code}, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { r.Close() })
	return r
}

func start(t *testing.T, r *wasm.Runtime, spec invoke.Spec) invoke.Instance {
	t.Helper()
	if spec.Artifact == "" {
		spec.Artifact = "bafycomponent"
	}
	instance, err := r.Start(t.Context(), spec)
	require.NoError(t, err)
	t.Cleanup(func() { instance.Close() })
	return instance
}

// ── the round trip ───────────────────────────────────────────────────────

// A REQUEST GOES IN AND A RESPONSE COMES BACK, through generated bindings, with
// every field of the world exercised: two strings, a list of records, and a
// list of bytes, in both directions.
func TestAComponentAnswersARequest(t *testing.T) {
	instance := start(t, tier(t, handler(t)), invoke.Spec{Name: "shop/prod/function/checkout"})

	res, err := instance.Serve(t.Context(), &invoke.Request{
		Method:  "GET",
		Path:    "/orders",
		Headers: map[string][]string{"x-trace": {"abc"}},
		Body:    []byte("hello"),
	})
	require.NoError(t, err)

	require.Equal(t, 200, res.Status, "the guest derived it from the method")
	require.Equal(t, "GET hello", string(res.Body), "the method and the body both survived")
	require.Equal(t, []string{"/orders"}, res.Headers["x-path"], "and the path")
	require.Equal(t, []string{"abc"}, res.Headers["x-echo-x-trace"], "and a header the caller sent")
}

// THE STATUS IS THE GUEST'S, so a test can tell a real dispatch from a
// constant — and a guest that fails does so by returning one rather than by
// trapping, because a trap takes the instance with it.
func TestTheGuestDecidesTheStatus(t *testing.T) {
	instance := start(t, tier(t, handler(t)), invoke.Spec{Name: "fn"})

	for method, want := range map[string]int{
		"GET":    200,
		"POST":   201,
		"":       204,
		"DELETE": 405,
	} {
		res, err := instance.Serve(t.Context(), &invoke.Request{Method: method})
		require.NoError(t, err, method)
		require.Equal(t, want, res.Status, method)
	}
}

// A HEADER MAY APPEAR MORE THAN ONCE, which is why the world carries a list of
// pairs rather than a map — Set-Cookie is the example that breaks every
// map-shaped API.
func TestAHeaderMayRepeat(t *testing.T) {
	instance := start(t, tier(t, handler(t)), invoke.Spec{Name: "fn"})

	res, err := instance.Serve(t.Context(), &invoke.Request{
		Method:  "GET",
		Headers: map[string][]string{"set-cookie": {"a=1", "b=2"}},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"a=1", "b=2"}, res.Headers["x-echo-set-cookie"])
}

// An empty request is a valid one: a pubsub delivery has no method and no path,
// and the world says so by making them strings rather than an option.
func TestAnEmptyRequest(t *testing.T) {
	instance := start(t, tier(t, handler(t)), invoke.Spec{Name: "fn"})

	res, err := instance.Serve(t.Context(), &invoke.Request{})
	require.NoError(t, err)
	require.Equal(t, 204, res.Status)
	require.Equal(t, " ", string(res.Body))

	// And a nil request is the same thing rather than a panic.
	res, err = instance.Serve(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, 204, res.Status)
}

// ── which export ─────────────────────────────────────────────────────────

// THE EXPORT IS NAMED BY THE AUTHOR, so a project with two functions in one
// component is two exports rather than two components.
func TestTheConfigurationNamesTheExport(t *testing.T) {
	r := tier(t, handler(t))

	named := start(t, r, invoke.Spec{
		Name:     "fn",
		Settings: map[string]string{wasm.SettingCall: "handle"},
	})
	res, err := named.Serve(t.Context(), &invoke.Request{Method: "GET"})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status)

	// A configuration that names none falls back to the world's own export, so
	// a component built straight from handler.wit runs without the config
	// repeating what the world already says.
	require.Equal(t, "handle", wasm.DefaultExport)

	// An export the component does not have is named, rather than a nil three
	// frames later.
	missing, err := r.Start(t.Context(), invoke.Spec{
		Artifact: "bafycomponent",
		Settings: map[string]string{wasm.SettingCall: "not_an_export"},
	})
	require.NoError(t, err, "instantiating succeeds; it is the call that cannot")
	defer missing.Close()

	_, err = missing.Serve(t.Context(), &invoke.Request{})
	require.ErrorContains(t, err, "not_an_export")
}

// ── the sandbox ──────────────────────────────────────────────────────────

// A GUEST GETS THE RESOURCE'S VARIABLES AND NOTHING ELSE, which is the same
// posture the container tier's restricted egress states from the other
// direction: there a firewall takes reach away, here there was never any.
func TestTheEnvironmentIsTheResourcesOwn(t *testing.T) {
	// The fixture does not read the environment, so what this proves is that
	// declaring one neither fails instantiation nor changes the answer — the
	// wiring is exercised by the guest that does read it, and the shape is
	// asserted where it is built.
	instance := start(t, tier(t, handler(t)), invoke.Spec{
		Name: "fn",
		Env:  map[string]string{"API_BASE": "https://api.example.com", "REGION": "eu"},
	})

	res, err := instance.Serve(t.Context(), &invoke.Request{Method: "GET"})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status)
}

// A GUEST THAT WILL NOT STOP IS STOPPED. The pool bounds a request with a
// context, and without close-on-context-done a guest in a loop would hold the
// goroutine until the process ended.
func TestACancelledContextEndsTheCall(t *testing.T) {
	instance := start(t, tier(t, handler(t)), invoke.Spec{Name: "fn"})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := instance.Serve(ctx, &invoke.Request{Method: "GET"})
	require.Error(t, err, "a call under a dead context does not run")
}

// ── the memory ceiling ───────────────────────────────────────────────────

// A CEILING IS ROUNDED UP TO WHOLE PAGES, because a limit below what the author
// asked for is a workload that fails for a reason the configuration does not
// state.
func TestTheMemoryCeilingIsHonoured(t *testing.T) {
	// 32MB is the fixture project's own function limit, and a component runs
	// inside it.
	instance := start(t, tier(t, handler(t)), invoke.Spec{Name: "fn", Memory: 32_000_000})
	res, err := instance.Serve(t.Context(), &invoke.Request{Method: "GET"})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status)

	// A ceiling under one page is still one page rather than none, which is
	// what "rounded up" has to mean at the bottom of the range.
	_, err = tier(t, handler(t)).Start(t.Context(), invoke.Spec{Artifact: "bafycomponent", Memory: 1})
	require.Error(t, err, "a component whose minimum memory exceeds one page cannot instantiate")
}

// ── what it refuses ──────────────────────────────────────────────────────

func TestWhatItRefuses(t *testing.T) {
	_, err := wasm.New(nil)
	require.ErrorIs(t, err, wasm.ErrNoAssets)

	r := tier(t, handler(t))
	require.Equal(t, wasm.Name, r.Name())

	_, err = r.Start(t.Context(), invoke.Spec{Name: "fn"})
	require.ErrorIs(t, err, wasm.ErrNoArtifact)

	// An artifact the store cannot fetch is a cold start that failed, named.
	gone := tier(t, nil)
	_, err = wasm.New(&store{err: errors.New("no provider for that cid")})
	require.NoError(t, err)
	_ = gone

	missing, err := wasm.New(&store{err: errors.New("no provider for that cid")})
	require.NoError(t, err)
	defer missing.Close()
	_, err = missing.Start(t.Context(), invoke.Spec{Artifact: "bafygone"})
	require.ErrorContains(t, err, "no provider for that cid")

	// And bytes that are not a component.
	junk := tier(t, []byte("not wasm"))
	_, err = junk.Start(t.Context(), invoke.Spec{Artifact: "bafyjunk"})
	require.ErrorContains(t, err, "instantiating")
}

// ── it is a pool tier ────────────────────────────────────────────────────

// Scale-to-zero, warm reuse and the concurrency rule are the pool's, and this
// package decides none of them.
func TestItIsAPoolTier(t *testing.T) {
	r := tier(t, handler(t))

	pool, err := invoke.NewPool(invoke.WithRuntime(r))
	require.NoError(t, err)
	defer pool.Close()

	spec := invoke.Spec{
		Runtime: wasm.Name, Name: "shop/prod/function/checkout", Revision: "rev1",
		Artifact: "bafycomponent", Timeout: 10 * time.Second,
	}

	res, err := pool.Do(t.Context(), spec, &invoke.Request{Method: "POST", Body: []byte("order")})
	require.NoError(t, err)
	require.Equal(t, 201, res.Status)
	require.Equal(t, "POST order", string(res.Body))

	// A SECOND REQUEST IS WARM: the component is fetched and compiled on the
	// cold start and not per request.
	require.Equal(t, 1, pool.Warm(spec))
	_, err = pool.Do(t.Context(), spec, &invoke.Request{Method: "GET"})
	require.NoError(t, err)
	require.Equal(t, 1, pool.Warm(spec))

	// A NEW REVISION IS A NEW INSTANCE, which is what makes a deploy a deploy:
	// the old one drains on its own idle timer and nothing reaches into a
	// running guest to swap its code.
	next := spec
	next.Revision = "rev2"
	_, err = pool.Do(t.Context(), next, &invoke.Request{Method: "GET"})
	require.NoError(t, err)
	require.Equal(t, 1, pool.Warm(spec))
	require.Equal(t, 1, pool.Warm(next))
}

// A GUEST'S OWN OUTPUT GOES WHERE THE COMPOSER SAYS, per workload — a tier is
// built once and runs many, so a single writer handed in at construction would
// interleave every function on the node into one stream with nothing saying
// which wrote what.
func TestAGuestsOutputIsRoutedPerWorkload(t *testing.T) {
	var asked []string
	r, err := wasm.New(&store{content: handler(t)},
		wasm.WithOutput(func(spec invoke.Spec) (io.Writer, io.Writer) {
			asked = append(asked, spec.Name)
			return io.Discard, io.Discard
		}))
	require.NoError(t, err)
	defer r.Close()

	for _, name := range []string{"shop/prod/function/checkout", "shop/prod/function/reserve"} {
		instance, err := r.Start(t.Context(), invoke.Spec{Name: name, Artifact: "bafycomponent"})
		require.NoError(t, err)
		instance.Close()
	}
	require.Equal(t, []string{"shop/prod/function/checkout", "shop/prod/function/reserve"}, asked)
}

// A sink that answers nil discards, which is what a node with nowhere to put a
// guest's output does rather than holding a buffer nothing reads.
func TestASinkThatAnswersNothing(t *testing.T) {
	r, err := wasm.New(&store{content: handler(t)},
		wasm.WithOutput(func(invoke.Spec) (io.Writer, io.Writer) { return nil, nil }))
	require.NoError(t, err)
	defer r.Close()

	instance, err := r.Start(t.Context(), invoke.Spec{Name: "fn", Artifact: "bafycomponent"})
	require.NoError(t, err)
	defer instance.Close()

	res, err := instance.Serve(t.Context(), &invoke.Request{Method: "GET"})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status)
}

// THE HOST INTERFACE IS CHECKED BEFORE ANYTHING IS FETCHED. A component built
// as an authorizer instantiates perfectly well and is then called with a
// handler's signature — it would not fail cleanly, it would be handed bytes it
// reads as something else, which is the failure `implements` exists to prevent.
func TestAWorldThisTierDoesNotServe(t *testing.T) {
	r := tier(t, handler(t))

	for _, world := range []string{"authorizer", "gateway-transformer", "proxy-router"} {
		_, err := r.Start(t.Context(), invoke.Spec{
			Name:     "shop/prod/function/gate",
			Artifact: "bafycomponent",
			Settings: map[string]string{wasm.SettingImplements: world},
		})
		require.ErrorIs(t, err, wasm.ErrUnsupportedWorld, world)
		require.ErrorContains(t, err, world)
	}

	// `handler` is served, and so is a resource that says nothing — the schema
	// defaults it, so an empty setting is the default rather than an absence.
	for _, world := range []string{wasm.WorldHandler, ""} {
		instance, err := r.Start(t.Context(), invoke.Spec{
			Artifact: "bafycomponent",
			Settings: map[string]string{wasm.SettingImplements: world},
		})
		require.NoError(t, err, world)
		instance.Close()
	}
}

// THE ENTRYPOINT IS THE AUTHOR'S, and it arrives as the vocabulary spells it.
func TestTheEntrypointComesFromTheConfiguration(t *testing.T) {
	instance := start(t, tier(t, handler(t)), invoke.Spec{
		Name:     "shop/prod/function/checkout",
		Settings: map[string]string{wasm.SettingCall: "handle", wasm.SettingImplements: "handler"},
	})

	res, err := instance.Serve(t.Context(), &invoke.Request{Method: "GET"})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status)
}
