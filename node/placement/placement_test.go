package placement

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/pkg/stats"
	"github.com/taubyte/tau/services/worker"
)

func newHost(t testing.TB, mn mocknet.Mocknet) host.Host {
	t.Helper()
	h, err := mn.GenPeer()
	require.NoError(t, err)
	require.NoError(t, mn.LinkAll())
	return h
}

// table is a router over a fixed set of nodes, so what is tested is the choice
// and not a swarm.
type table struct {
	mu     sync.Mutex
	byName map[string][]stats.Node
	asked  []string
}

func (t *table) Route(name string, _ time.Duration) []stats.Node {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.asked = append(t.asked, name)
	return t.byName[name]
}

func (t *table) questions() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.asked...)
}

// recording is a local worker that answers, or does not.
type recording struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (r *recording) Invoke(context.Context, specs.Address, *invoke.Request) (*invoke.Response, error) {
	r.mu.Lock()
	r.calls++
	err := r.err
	r.mu.Unlock()

	if err != nil {
		return nil, err
	}
	return &invoke.Response{Status: 200, Body: []byte("served")}, nil
}

func (r *recording) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func at() specs.Address {
	return specs.Address{Project: "shop", Environment: "prod", Kind: "function", Name: "checkout"}
}

func newWorkers(t *testing.T, router Router, opts ...Option) (*Workers, host.Host) {
	t.Helper()

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	w, err := New(h, router, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { w.Close() })
	return w, h
}

// TestAWarmNodeIsPreferred. A worker that already holds the compiled project
// serves without fetching one, and the fetch it saves is a whole project over
// the wire.
func TestAWarmNodeIsPreferred(t *testing.T) {
	local := &recording{}

	router := &table{byName: map[string][]stats.Node{}}
	w, h := newWorkers(t, router, WithLocal(local))

	router.byName["worker/shop/prod"] = []stats.Node{{Peer: h.ID().String(), Load: 0.9}}
	router.byName["worker"] = []stats.Node{{Peer: "somebody-else", Load: 0.1}}

	res, err := w.Invoke(t.Context(), at(), &invoke.Request{})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status)
	require.Equal(t, 1, local.count())

	// AND THE WARM QUESTION IS ASKED FIRST. A router that asked the general
	// question first would answer it first, and the warm list would never win.
	require.Equal(t, []string{"worker/shop/prod", "worker"}, router.questions())
}

// TestANodeThatHoldsNothingStillTakesIt. Preferring a warm node is an
// optimisation; requiring one would make a project's first request impossible.
func TestANodeThatHoldsNothingStillTakesIt(t *testing.T) {
	local := &recording{}

	router := &table{byName: map[string][]stats.Node{}}
	w, h := newWorkers(t, router, WithLocal(local))
	router.byName["worker"] = []stats.Node{{Peer: h.ID().String()}}

	_, err := w.Invoke(t.Context(), at(), &invoke.Request{})
	require.NoError(t, err)
	require.Equal(t, 1, local.count())
}

// TestANodeIsNotOfferedTwice. It is in both lists whenever it is warm, and
// trying it again as a "cold" candidate would spend an attempt on a node that
// just refused.
func TestANodeIsNotOfferedTwice(t *testing.T) {
	local := &recording{err: errors.New("no route to peer")}

	router := &table{byName: map[string][]stats.Node{}}
	w, h := newWorkers(t, router, WithLocal(local))

	router.byName["worker/shop/prod"] = []stats.Node{{Peer: h.ID().String()}}
	router.byName["worker"] = []stats.Node{{Peer: h.ID().String()}}

	_, err := w.Invoke(t.Context(), at(), &invoke.Request{})
	require.Error(t, err)
	require.Equal(t, 1, local.count(), "the same node was tried twice")
}

// TestNothingReadyIsSaidPlainly rather than as a timeout.
func TestNothingReadyIsSaidPlainly(t *testing.T) {
	w, _ := newWorkers(t, &table{byName: map[string][]stats.Node{}})

	_, err := w.Invoke(t.Context(), at(), &invoke.Request{})
	require.ErrorIs(t, err, ErrNoWorkers)
}

// TestAFailureToREACHANodeIsRetried and a failure OF the request is not: a 500
// from a workload is that workload's answer, and sending it somewhere else
// would run it twice.
func TestOnlyAnUnreachableNodeIsRetried(t *testing.T) {
	for _, c := range []struct {
		name    string
		err     error
		retried bool
	}{
		{"no route", errors.New("no route to peer"), true},
		{"stream reset", errors.New("stream reset"), true},

		{"the deployment is missing a tier", worker.ErrNoRuntime, false},
		{"missing an artifact", worker.ErrNoArtifact, false},
		{"missing a secret", worker.ErrNoSecrets, false},
		{"a target that names nothing", worker.ErrNoTarget, false},
		{"the tier is not registered", invoke.ErrNoRuntime, false},
		{"the caller gave up", context.Canceled, false},
		{"the budget is spent", context.DeadlineExceeded, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.retried, unreachable(c.err))
		})
	}
}

// TestTheAttemptBudgetIsSpentAndNoMore. A request that has been refused three
// times is not going to be taken by the fourth, and the caller is waiting.
func TestTheAttemptBudgetIsBounded(t *testing.T) {
	local := &recording{err: errors.New("no route to peer")}

	router := &table{byName: map[string][]stats.Node{}}
	w, h := newWorkers(t, router, WithLocal(local), WithAttempts(2))

	// The same peer listed several times: every one is a candidate, and the
	// budget is what stops the loop.
	me := h.ID().String()
	router.byName["worker"] = []stats.Node{{Peer: me}, {Peer: me}, {Peer: me}, {Peer: me}}

	_, err := w.Invoke(t.Context(), at(), &invoke.Request{})
	require.Error(t, err)
	require.Equal(t, 2, local.count())
}

// TestANodeThatIsNotAPeerIsSkipped rather than failing the request: a router
// answering with something that is not a peer id is a broken report, and the
// next candidate may be fine.
func TestANonPeerCandidateIsSkipped(t *testing.T) {
	local := &recording{}

	router := &table{byName: map[string][]stats.Node{}}
	w, h := newWorkers(t, router, WithLocal(local))
	router.byName["worker"] = []stats.Node{{Peer: "not-a-peer-id"}, {Peer: h.ID().String()}}

	_, err := w.Invoke(t.Context(), at(), &invoke.Request{})
	require.NoError(t, err)
	require.Equal(t, 1, local.count())
}

// TestANodeDoesNotFavourItself. WithLocal saves a stream to this node when the
// router picks it; it does not change which node is picked. Favouring itself is
// how one node in a fleet serves everything it is asked and nothing it is not.
func TestANodeDoesNotFavourItself(t *testing.T) {
	local := &recording{}

	router := &table{byName: map[string][]stats.Node{}}
	w, h := newWorkers(t, router, WithLocal(local))

	// A less loaded peer that is not this one comes first, so this node is
	// never reached — and the call fails because that peer is not dialable
	// here, which is the proof it was tried.
	other := newHost(t, mocknet.New())
	router.byName["worker"] = []stats.Node{{Peer: other.ID().String(), Load: 0.1}, {Peer: h.ID().String(), Load: 0.9}}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	_, _ = w.Invoke(ctx, at(), &invoke.Request{})
	require.Equal(t, 1, local.count(), "this node was reached only after the other one failed")
}

func TestWhatItRefuses(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	_, err := New(nil, &table{})
	require.ErrorIs(t, err, ErrNoHost)

	_, err = New(h, nil)
	require.ErrorIs(t, err, ErrNoRouter)

	for _, opt := range []Option{WithLocal(nil), WithMaxAge(0), WithMaxAge(-time.Second), WithAttempts(0)} {
		_, err := New(h, &table{}, opt)
		require.Error(t, err)
	}
}

// TestCloseIsIdempotentAndFinal.
func TestCloseIsIdempotentAndFinal(t *testing.T) {
	router := &table{byName: map[string][]stats.Node{}}
	w, h := newWorkers(t, router)
	router.byName["worker"] = []stats.Node{{Peer: h.ID().String()}}

	require.NoError(t, w.Close())
	require.NoError(t, w.Close())

	_, err := w.Invoke(t.Context(), at(), &invoke.Request{})
	require.Error(t, err)
}

// full is a local worker that offers every world a worker can serve, so what is
// tested below is the PLACEMENT of each verb rather than any one worker.
type full struct {
	recording
	conn net.Conn
}

func (f *full) Transform(context.Context, specs.Address, *invoke.Request) (*invoke.Outcome, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return &invoke.Outcome{Answer: &invoke.Response{Status: 204}}, nil
}

func (f *full) Route(context.Context, specs.Address, *invoke.Request) (*invoke.Routing, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return &invoke.Routing{None: "nowhere"}, nil
}

func (f *full) Direct(context.Context, specs.Address, *invoke.Connection, []byte) (*invoke.Direction, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return &invoke.Direction{Refused: "not this one"}, nil
}

func (f *full) Shape(context.Context, specs.Address, *invoke.Stream) (*invoke.Shaping, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return &invoke.Shaping{Pass: &invoke.Shaped{Out: []byte("shaped")}}, nil
}

func (f *full) Conn(context.Context, worker.Target, string) (net.Conn, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return f.conn, nil
}

// TestEveryVerbIsPlacedTheSameWay. A transformer, a gateway router, a proxy
// router and a proxy transformer are all workloads of the same deployment, so
// the node that is warm for the project is warm for them too — and each one
// goes through the same choice rather than a second one beside it.
func TestEveryVerbIsPlacedTheSameWay(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*Workers) error
	}{
		{"transform", func(w *Workers) error {
			out, err := w.Transform(t.Context(), at(), &invoke.Request{})
			if err == nil && out.Answer.Status != 204 {
				return errors.New("not the transformer's answer")
			}
			return err
		}},
		{"route", func(w *Workers) error {
			out, err := w.Route(t.Context(), at(), &invoke.Request{})
			if err == nil && out.None != "nowhere" {
				return errors.New("not the router's answer")
			}
			return err
		}},
		{"direct", func(w *Workers) error {
			out, err := w.Direct(t.Context(), at(), &invoke.Connection{Source: "10.0.0.1:1"}, []byte("PG"))
			if err == nil && out.Refused != "not this one" {
				return errors.New("not the proxy router's answer")
			}
			return err
		}},
		{"shape", func(w *Workers) error {
			out, err := w.Shape(t.Context(), at(), &invoke.Stream{Way: invoke.Inbound})
			if err == nil && string(out.Pass.Out) != "shaped" {
				return errors.New("not the proxy transformer's answer")
			}
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			local := &full{}
			router := &table{byName: map[string][]stats.Node{}}
			w, h := newWorkers(t, router, WithLocal(local))
			router.byName["worker/shop/prod"] = []stats.Node{{Peer: h.ID().String()}}

			require.NoError(t, tc.call(w))
			require.Equal(t, 1, local.count())

			// THE WARM QUESTION FIRST, for every verb and not just Invoke.
			require.Equal(t, "worker/shop/prod", router.questions()[0])
		})
	}
}

// TestAWorkerThatServesNoSuchWorldSaysSo. A worker is not required to offer
// every world, so the refusal names which one it does not run rather than
// looking like a node that could not be reached.
func TestAWorkerThatServesNoSuchWorldSaysSo(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*Workers) error
		says string
	}{
		{"transformer", func(w *Workers) error {
			_, err := w.Transform(t.Context(), at(), &invoke.Request{})
			return err
		}, "runs no transformer"},
		{"router", func(w *Workers) error {
			_, err := w.Route(t.Context(), at(), &invoke.Request{})
			return err
		}, "runs no router"},
		{"proxy router", func(w *Workers) error {
			_, err := w.Direct(t.Context(), at(), &invoke.Connection{}, nil)
			return err
		}, "runs no proxy router"},
		{"proxy transformer", func(w *Workers) error {
			_, err := w.Shape(t.Context(), at(), &invoke.Stream{})
			return err
		}, "runs no proxy transformer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A bare Invoker: it answers requests and nothing else.
			local := &recording{}
			router := &table{byName: map[string][]stats.Node{}}
			w, h := newWorkers(t, router, WithLocal(local))
			router.byName["worker"] = []stats.Node{{Peer: h.ID().String()}}

			err := tc.call(w)
			require.ErrorContains(t, err, tc.says)
			require.Equal(t, 0, local.count())
		})
	}
}

// TestAConnectionToOurOwnWorkloadIsNotAStreamToOurselves.
func TestAConnectionToOurOwnWorkloadIsNotAStreamToOurselves(t *testing.T) {
	here, there := net.Pipe()
	t.Cleanup(func() { here.Close(); there.Close() })

	local := &full{conn: here}
	router := &table{byName: map[string][]stats.Node{}}
	w, h := newWorkers(t, router, WithLocal(local))
	router.byName["worker/shop/prod"] = []stats.Node{{Peer: h.ID().String()}}

	conn, err := w.Conn(t.Context(), at(), "http")
	require.NoError(t, err)
	require.Equal(t, here, conn)
	require.Equal(t, 1, local.count())
}

// TestAConnectionHasNowhereToGoWhenNoNodeIsReady. The refusal is the same one
// an invocation gets, because the choice that produced it is the same choice.
func TestAConnectionHasNowhereToGoWhenNoNodeIsReady(t *testing.T) {
	w, _ := newWorkers(t, &table{byName: map[string][]stats.Node{}}, WithLocal(&full{}))

	_, err := w.Conn(t.Context(), at(), "http")
	require.ErrorIs(t, err, ErrNoWorkers)
}

// TestAPeerThatIsNotOneIsNotTried. A discovery row carrying a string that does not
// decode is not a node that could not be reached — nothing was dialed — so it
// is reported rather than retried.
func TestAPeerThatIsNotOneIsNotTried(t *testing.T) {
	router := &table{byName: map[string][]stats.Node{
		"worker": {{Peer: "not-a-peer-id"}},
	}}
	w, _ := newWorkers(t, router, WithLocal(&full{}))

	_, err := w.Conn(t.Context(), at(), "http")
	require.ErrorContains(t, err, "is not a peer")
}

// TestAReportIsAlwaysOlderThanNothing.
func TestAReportIsAlwaysOlderThanNothing(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	_, err := New(newHost(t, mn), &table{}, WithMaxAge(0))
	require.ErrorContains(t, err, "a report is always older than nothing")
}
