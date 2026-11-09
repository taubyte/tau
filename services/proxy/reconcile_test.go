package proxy

import (
	"context"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// deployments is what this node holds, and it changes between sweeps the way a
// deploy makes it change.
type deployments struct {
	mu   sync.Mutex
	held []Bind
}

func (d *deployments) Proxies(context.Context) []Bind {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Bind(nil), d.held...)
}

func (d *deployments) set(b ...Bind) {
	d.mu.Lock()
	d.held = b
	d.mu.Unlock()
}

func sweeping(t *testing.T, res map[string]any, held ...Bind) (*Service, *deployments, *warnings) {
	t.Helper()

	d := &deployments{}
	d.set(held...)

	said := &warnings{}
	s, err := build(Config{CloudFqdn: "tau.example.io"},
		withProjects(projects{p: compiled(t, res)}),
		withWorkers(&workers{}),
		withDeployments(d),
		withWarn(said.add),
	)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s, d, said
}

func pgwire() Bind {
	return Bind{Project: "shop", Environment: "prod", Name: "pgwire"}
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

func TestASweepServesWhatIsHeldExactlyOnce(t *testing.T) {
	s, _, _ := sweeping(t, servable(), pgwire())

	require.Equal(t, 1, s.Sweep(t.Context()), "the first sweep starts it")
	require.Len(t, s.Serving(), 1)

	require.Equal(t, 0, s.Sweep(t.Context()), "and the second starts nothing")
	require.Len(t, s.Serving(), 1)
}

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

func TestAProxyDeployedBetweenSweepsIsPickedUp(t *testing.T) {
	s, d, _ := sweeping(t, servable())

	require.Equal(t, 0, s.Sweep(t.Context()))
	require.Empty(t, s.Serving())

	d.set(pgwire())
	require.Equal(t, 1, s.Sweep(t.Context()))
	require.Len(t, s.Serving(), 1)
}

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

func TestRunSweepsImmediatelyAndThenOnTheInterval(t *testing.T) {
	d := &deployments{}
	d.set(pgwire())

	s, err := build(Config{CloudFqdn: "tau.example.io", Sweep: 10 * time.Millisecond},
		withProjects(projects{p: compiled(t, servable())}),
		withWorkers(&workers{}),
		withDeployments(d),
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

func TestASweepWithNoDeploymentsDoesNothing(t *testing.T) {
	s, _ := served(t, Config{CloudFqdn: "tau.example.io"}, servable(), &workers{})
	require.Equal(t, 0, s.Sweep(t.Context()))
}

func TestANilDeploymentSourceIsRefusedAtConstruction(t *testing.T) {
	_, err := build(Config{},
		withProjects(projects{}),
		withWorkers(&workers{}),
		withDeployments(nil),
	)
	require.ErrorIs(t, err, ErrNoDeployments)
}

func TestServingOneProxyTwiceIsRefused(t *testing.T) {
	s, _, _ := sweeping(t, servable(), pgwire())

	first, err := s.Serve(t.Context(), pgwire())
	require.NoError(t, err)

	_, err = s.Serve(t.Context(), pgwire())
	require.ErrorContains(t, err, "already served")

	require.Equal(t, []Socket{first}, s.Serving())
	require.True(t, listening(t, first.Port), "and the one that was there is untouched")
}
