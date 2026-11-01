package worker

import (
	"net/http"
	"testing"
	"time"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"

	workerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/worker/v2"
)

func newBits(t *testing.T) (*fakeRuntime, Pool) {
	t.Helper()
	rt := &fakeRuntime{name: "container"}
	pool, err := invoke.NewPool(invoke.WithRuntime(rt))
	require.NoError(t, err)
	t.Cleanup(func() { pool.Close() })
	return rt, pool
}

func TestNewOpensNothing(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	_, pool := newBits(t)

	s, err := New(newHost(t, mn), Config{},
		WithRegister(&countingRegister{}), WithPool(pool))
	require.NoError(t, err)
	require.Nil(t, s.server)
	require.NoError(t, s.Close())
}

func TestNewRefusesWhatItCannotWorkWithout(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)
	_, pool := newBits(t)

	_, err := New(nil, Config{}, WithRegister(&countingRegister{}), WithPool(pool))
	require.ErrorIs(t, err, ErrNoHost, "a service that can invent its own identity starts two ways")

	_, err = New(h, Config{}, WithPool(pool))
	require.ErrorIs(t, err, ErrNoRegister)

	_, err = New(h, Config{}, WithRegister(&countingRegister{}))
	require.ErrorIs(t, err, ErrNoPool)

	// A nil handed to an option is caught by the option, so the message names
	// the setting rather than surfacing later as a missing dependency.
	for _, opt := range []Option{
		WithRegister(nil), WithPool(nil), WithClock(nil), WithArtifacts(nil), WithSecrets(nil),
	} {
		_, err := New(h, Config{}, WithRegister(&countingRegister{}), WithPool(pool), opt)
		require.Error(t, err)
	}
}

func TestTheConfigRefusesNumbersThatMeanNothing(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)
	_, pool := newBits(t)

	for _, cfg := range []Config{
		{PointerTTL: -time.Second},
		{MaxProjects: -1},
	} {
		_, err := New(h, cfg, WithRegister(&countingRegister{}), WithPool(pool))
		require.Error(t, err, "%+v", cfg)
	}

	// And the defaults are applied rather than left zero.
	s, err := New(h, Config{}, WithRegister(&countingRegister{}), WithPool(pool))
	require.NoError(t, err)
	require.Equal(t, DefaultPointerTTL, s.cfg.PointerTTL)
	require.Equal(t, DefaultMaxProjects, s.cfg.MaxProjects)
	require.NoError(t, s.Close())
}

func TestServerOptionsReachListen(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	_, pool := newBits(t)

	s, err := New(newHost(t, mn), Config{},
		WithRegister(&countingRegister{}), WithPool(pool),
		WithServerOptions(rpc.WithHTTPServer(func(hs *http.Server) { hs.Handler = http.NotFoundHandler() })))
	require.NoError(t, err)

	// rpc.Listen owns http.Server.Handler and refuses an option that sets it,
	// which is how this test knows the option was not dropped on the floor.
	require.ErrorIs(t, s.Start(t.Context()), rpc.ErrServerFieldOwned)
	require.NoError(t, s.Close())
}

func TestTheLifecycleIsNotForgiving(t *testing.T) {
	h := newHarness(t)

	require.Error(t, h.Worker.Start(t.Context()), "a second listener is an error, not a leak")
	require.NoError(t, h.Worker.Close())
	require.NoError(t, h.Worker.Close(), "close is idempotent")
	require.Error(t, h.Worker.Start(t.Context()), "a closed worker does not start again")
}

func TestTheProtocolIsDerived(t *testing.T) {
	require.Equal(t, "/tau/worker/v2", string(Protocol))
	require.Equal(t, Protocol, Descriptor.Protocol)
	require.Equal(t, IngressNone, Descriptor.Ingress,
		"a request from the internet arrives through a gateway, which is what decides what may be reached")
}

func TestARuntimeIsNeverGuessed(t *testing.T) {
	got, err := runtimeOf("application", specs.ReadWorkloadBlock(specs.Resource{}))
	require.NoError(t, err)
	require.Equal(t, "container", got, "a kind with one tier runs on it")

	for kind, tier := range map[string]string{"function": "js", "worker": "container"} {
		got, err = runtimeOf(kind, specs.ReadWorkloadBlock(specs.Resource{"runtime": tier}))
		require.NoError(t, err)
		require.Equal(t, tier, got, "the resource's own choice wins over the kind's set")

		_, err = runtimeOf(kind, specs.ReadWorkloadBlock(specs.Resource{}))
		require.ErrorIs(t, err, ErrAmbiguous, "%s runs on more than one tier and this one says none", kind)
	}

	_, err = runtimeOf("cache", specs.ReadWorkloadBlock(specs.Resource{}))
	require.ErrorIs(t, err, ErrNoRuntime, "nothing runs a cache")
}

func TestHeadersSurviveTheRoundTrip(t *testing.T) {
	h := newHarness(t)

	res, err := h.Client.Invoke(t.Context(), connect.NewRequest(&workerv2.InvokeRequest{
		Target: target("application", "api"),
		Method: "POST",
		Path:   "/checkout",
		Headers: map[string]*workerv2.Headers{
			"Accept": {Values: []string{"application/json", "text/plain"}},
		},
		Body: []byte("an order"),
	}))
	require.NoError(t, err)
	require.Equal(t, []string{"/checkout"}, res.Msg.GetHeaders()["X-Path"].GetValues())

	require.Nil(t, fromWire(nil), "no headers is nil rather than an empty map")
	require.Nil(t, toWire(nil))
	require.Equal(t,
		map[string][]string{"Accept": {"application/json", "text/plain"}},
		fromWire(map[string]*workerv2.Headers{"Accept": {Values: []string{"application/json", "text/plain"}}}))
}

func TestOnlyRunTimeEnvironmentVariablesAreHandedOver(t *testing.T) {
	s := &Service{}

	env, err := s.envOf(t.Context(), Target{Name: "api"}, specs.ReadWorkloadBlock(specs.Resource{
		"variables": map[string]any{
			"LOG_LEVEL": map[string]any{"as": "env", "at": "run", "value": "info"},
			"BUILT_AT":  map[string]any{"as": "env", "at": "build", "value": "2026"},
			"CERT":      map[string]any{"as": "file", "at": "run", "value": "-----BEGIN"},
		},
	}).Variables)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"LOG_LEVEL": "info"}, env)

	// No variables at all is nil rather than an empty map, so a spec with no
	// environment says so.
	env, err = s.envOf(t.Context(), Target{Name: "api"}, nil)
	require.NoError(t, err)
	require.Nil(t, env)
}
