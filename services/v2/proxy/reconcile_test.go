package proxy_test

import (
	"context"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/services/v2/proxy"
)

// deployments is what this node holds, and it changes between sweeps the way a
// deploy makes it change.
type deployments struct {
	mu   sync.Mutex
	held []proxy.Bind
}

func (d *deployments) Proxies(context.Context) []proxy.Bind {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]proxy.Bind(nil), d.held...)
}

func (d *deployments) set(b ...proxy.Bind) {
	d.mu.Lock()
	d.held = b
	d.mu.Unlock()
}

func sweeping(t *testing.T, res map[string]any, held ...proxy.Bind) (*proxy.Service, *deployments, *warnings) {
	t.Helper()

	d := &deployments{}
	d.set(held...)

	said := &warnings{}
	s, err := proxy.New(proxy.Config{CloudFqdn: "tau.example.io"},
		proxy.WithProjects(projects{p: compiled(t, res)}),
		proxy.WithWorkers(&workers{}),
		proxy.WithDeployments(d),
		proxy.WithWarn(said.add),
	)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s, d, said
}

func pgwire() proxy.Bind {
	return proxy.Bind{Project: "shop", Environment: "prod", Name: "pgwire"}
}

func listening(t *testing.T, port uint16) bool {
	t.Helper()
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))), time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// TestASWEEPSTARTSWHATTHISNODEHOLDS, and starts it once: a second listener for
// one resource would take half the connections and appear in DNS for none.
func TestASweepServesWhatIsHeldExactlyOnce(t *testing.T) {
	s, _, _ := sweeping(t, servable(), pgwire())

	require.Equal(t, 1, s.Sweep(t.Context()), "the first sweep starts it")
	require.Len(t, s.Serving(), 1)

	require.Equal(t, 0, s.Sweep(t.Context()), "and the second starts nothing")
	require.Len(t, s.Serving(), 1)
}

// TestAPROXYTHATSTOPSBEINGHELDSTOPSLISTENING. A node that dropped a deployment
// and kept the port would go on being sent clients by DNS and refusing every
// one of them, which is worse than not answering.
func TestAProxyNoLongerHeldIsClosed(t *testing.T) {
	s, d, _ := sweeping(t, servable(), pgwire())

	s.Sweep(t.Context())
	port := s.Serving()[0].Port
	require.True(t, listening(t, port))

	d.set()
	s.Sweep(t.Context())

	require.Empty(t, s.Serving(), "and it is published nowhere")
	require.False(t, listening(t, port), "the socket is gone")
}

// TestADEPLOYISNOTICEDBYTHENEXTSWEEP — there is no edge to react to, so the
// sweep is what converges.
func TestAProxyDeployedBetweenSweepsIsPickedUp(t *testing.T) {
	s, d, _ := sweeping(t, servable())

	require.Equal(t, 0, s.Sweep(t.Context()))
	require.Empty(t, s.Serving())

	d.set(pgwire())
	require.Equal(t, 1, s.Sweep(t.Context()))
	require.Len(t, s.Serving(), 1)
}

// TestONEREASONISSAIDONCE. A sweep every ten seconds repeating one sentence
// would bury the deploy that fixes it.
func TestAnUnservableProxyIsReportedOnceAndRetriedForever(t *testing.T) {
	res := servable()
	res["tls"] = map[string]any{"mode": "terminate"}
	s, _, said := sweeping(t, res, pgwire())

	for range 5 {
		require.Equal(t, 0, s.Sweep(t.Context()))
	}
	require.Equal(t, 1, said.len(), "the same refusal about the same resource is said once")
	require.Empty(t, s.Serving())
}

// TestARUNSWEEPSUNTILITSCONTEXTISDONE, and the first sweep happens immediately
// rather than one interval later — a node that had to wait would answer no
// connections for its whole first tick.
func TestRunSweepsImmediatelyAndThenOnTheInterval(t *testing.T) {
	d := &deployments{}
	d.set(pgwire())

	s, err := proxy.New(proxy.Config{CloudFqdn: "tau.example.io", Sweep: 10 * time.Millisecond},
		proxy.WithProjects(projects{p: compiled(t, servable())}),
		proxy.WithWorkers(&workers{}),
		proxy.WithDeployments(d),
	)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()

	require.Eventually(t, func() bool { return len(s.Serving()) == 1 }, time.Second, 5*time.Millisecond)

	d.set()
	require.Eventually(t, func() bool { return len(s.Serving()) == 0 }, time.Second, 5*time.Millisecond,
		"and it keeps sweeping")

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return when its context was cancelled")
	}
}

// TestASERVICEWITHNOSOURCE serves only what it was told to by hand, which is
// what a composer with its own listener does.
func TestASweepWithNoDeploymentsDoesNothing(t *testing.T) {
	s, _ := service(t, proxy.Config{CloudFqdn: "tau.example.io"}, servable(), &workers{})
	require.Equal(t, 0, s.Sweep(t.Context()))
}

func TestANilDeploymentSourceIsRefusedAtConstruction(t *testing.T) {
	_, err := proxy.New(proxy.Config{},
		proxy.WithProjects(projects{}),
		proxy.WithWorkers(&workers{}),
		proxy.WithDeployments(nil),
	)
	require.ErrorIs(t, err, proxy.ErrNoDeployments)
}

// TestONELISTENERPERRESOURCE, whichever way it was asked for: two would each
// take half the connections and only one of them would be in DNS.
func TestServingOneProxyTwiceIsRefused(t *testing.T) {
	s, _, _ := sweeping(t, servable(), pgwire())

	first, err := s.Serve(t.Context(), pgwire())
	require.NoError(t, err)

	_, err = s.Serve(t.Context(), pgwire())
	require.ErrorContains(t, err, "already served")

	require.Equal(t, []proxy.Socket{first}, s.Serving())
	require.True(t, listening(t, first.Port), "and the one that was there is untouched")
}
