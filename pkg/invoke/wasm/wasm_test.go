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

// Every test runs a real component built from wit/handler.wit (fixtures/handler), so
// lowering and lifting agree with generated bindings.

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

func TestTheEngineModeIsTheOperators(t *testing.T) {
	require.Equal(t, wasm.ModeCompiler, tier(t, handler(t)).Mode())

	interpreted := tier(t, handler(t), wasm.WithMode(wasm.ModeInterpreter))
	require.Equal(t, wasm.ModeInterpreter, interpreted.Mode())
	res, err := start(t, interpreted, invoke.Spec{Name: "shop/prod/function/checkout"}).
		Serve(t.Context(), &invoke.Request{Method: "GET", Path: "/orders", Body: []byte("hello")})
	require.NoError(t, err)
	require.Equal(t, "GET hello", string(res.Body))

	_, err = wasm.New(&store{}, wasm.WithMode("jit"))
	require.ErrorIs(t, err, wasm.ErrUnknownMode)
	require.ErrorContains(t, err, `"jit"`)
}

func TestAnotherTiersSpecIsRefusedByName(t *testing.T) {
	r := tier(t, handler(t))
	spec := invoke.Spec{Runtime: "js", Name: "shop/prod/function/checkout", Artifact: "bafycomponent"}

	refused := func(err error) {
		t.Helper()
		require.ErrorIs(t, err, invoke.ErrNoRuntime)
		require.ErrorContains(t, err, `shop/prod/function/checkout runs on the "js" tier, and this is the wasm tier`)
	}
	_, err := r.Start(t.Context(), spec)
	refused(err)
	_, err = r.Authorize(t.Context(), spec, aQuestion())
	refused(err)
	_, err = r.Admit(t.Context(), spec, wasm.Opening{})
	refused(err)
	_, err = r.AuthorizeRequest(t.Context(), spec, &invoke.Request{Method: "GET", Path: "/"})
	refused(err)
	_, err = r.Resolve(t.Context(), spec, invoke.Question{Name: "a.example.com", Type: "A"})
	refused(err)

	// and this tier's own name is what it runs
	spec.Runtime = wasm.Name
	start(t, r, spec)
}

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

func TestAHeaderMayRepeat(t *testing.T) {
	instance := start(t, tier(t, handler(t)), invoke.Spec{Name: "fn"})

	res, err := instance.Serve(t.Context(), &invoke.Request{
		Method:  "GET",
		Headers: map[string][]string{"set-cookie": {"a=1", "b=2"}},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"a=1", "b=2"}, res.Headers["x-echo-set-cookie"])
}

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

func TestTheConfigurationNamesTheExport(t *testing.T) {
	r := tier(t, handler(t))

	named := start(t, r, invoke.Spec{
		Name:     "fn",
		Settings: map[string]string{wasm.SettingCall: "handle"},
	})
	res, err := named.Serve(t.Context(), &invoke.Request{Method: "GET"})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status)

	// No export configured falls back to the world's own.
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

func TestTheEnvironmentIsTheResourcesOwn(t *testing.T) {
	// The fixture ignores the environment; this only proves declaring one changes
	// nothing.
	instance := start(t, tier(t, handler(t)), invoke.Spec{
		Name: "fn",
		Env:  map[string]string{"API_BASE": "https://api.example.com", "REGION": "eu"},
	})

	res, err := instance.Serve(t.Context(), &invoke.Request{Method: "GET"})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status)
}

func TestACancelledContextEndsTheCall(t *testing.T) {
	instance := start(t, tier(t, handler(t)), invoke.Spec{Name: "fn"})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := instance.Serve(ctx, &invoke.Request{Method: "GET"})
	require.Error(t, err, "a call under a dead context does not run")
}

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

	// A new revision is a new instance.
	next := spec
	next.Revision = "rev2"
	_, err = pool.Do(t.Context(), next, &invoke.Request{Method: "GET"})
	require.NoError(t, err)
	require.Equal(t, 1, pool.Warm(spec))
	require.Equal(t, 1, pool.Warm(next))
}

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

func TestAWorldThisTierDoesNotServe(t *testing.T) {
	r := tier(t, handler(t))

	// Start refuses the worlds with their own entry points (authorizer, instance
	// validator); the pooled ones start like a handler.
	for _, world := range []string{"authorizer", "instance-validator"} {
		_, err := r.Start(t.Context(), invoke.Spec{
			Name:     "shop/prod/function/gate",
			Artifact: "bafycomponent",
			Settings: map[string]string{wasm.SettingImplements: world},
		})
		require.ErrorIs(t, err, wasm.ErrUnsupportedWorld, world)
		require.ErrorContains(t, err, world)
	}

	// `handler` is served, and so is an empty setting, which the schema defaults.
	for _, world := range []string{wasm.WorldHandler, ""} {
		instance, err := r.Start(t.Context(), invoke.Spec{
			Artifact: "bafycomponent",
			Settings: map[string]string{wasm.SettingImplements: world},
		})
		require.NoError(t, err, world)
		instance.Close()
	}
}

func TestTheEntrypointComesFromTheConfiguration(t *testing.T) {
	instance := start(t, tier(t, handler(t)), invoke.Spec{
		Name:     "shop/prod/function/checkout",
		Settings: map[string]string{wasm.SettingCall: "handle", wasm.SettingImplements: "handler"},
	})

	res, err := instance.Serve(t.Context(), &invoke.Request{Method: "GET"})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status)
}

func TestAComponentMayUseWebAssembly3(t *testing.T) {
	code, err := os.ReadFile("fixtures/v3.wasm")
	require.NoError(t, err, "run `wasm-tools parse v3.wat -o v3.wasm` in fixtures")
	instance := start(t, tier(t, code), invoke.Spec{Name: "shop/prod/function/python"})

	res, err := instance.Serve(t.Context(), &invoke.Request{Method: "GET", Path: "/"})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status)
	require.Equal(t, "ok", string(res.Body))
}
