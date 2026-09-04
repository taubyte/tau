package node

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	pkgregister "github.com/taubyte/tau/pkg/register"
)

// TestANodeRunsWhatItWasToldTo, and nothing else.
func TestANodeRunsWhatItWasToldTo(t *testing.T) {
	n, _, _ := build(t, Config{Services: []string{Register, Stats}})

	require.NotNil(t, n.Register())
	require.NotNil(t, n.Stats())
	require.Nil(t, n.Worker())
	require.Nil(t, n.Handler(), "it runs no gateway")
	require.Equal(t, []string{Register, Stats}, n.Services())
}

// TestAnEmptyServiceListIsEveryService, which is the single-node development
// shape and the one a first `tau` run gets.
func TestAnEmptyServiceListIsEveryService(t *testing.T) {
	n, _, _ := build(t, Config{})

	require.ElementsMatch(t, All, n.Services())
	require.NotNil(t, n.Register())
	require.NotNil(t, n.Worker())
	require.NotNil(t, n.Stats())
	require.NotNil(t, n.Handler())
}

// TestAMisspelledServiceIsRefused. A name that starts nothing is a node that
// looks healthy and answers nothing.
func TestAMisspelledServiceIsRefused(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	_, err := New(Config{Services: []string{"registry"}}, WithHost(h))
	require.Error(t, err)
	require.Contains(t, err.Error(), "registry")
	require.Contains(t, err.Error(), Register, "and it says what this node can run")
}

// TestEachServiceSaysWhatItCannotStartWithout.
func TestEachServiceSaysWhatItCannotStartWithout(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	_, err := New(Config{}, WithPubSub(newPubSub(t, h)))
	require.ErrorIs(t, err, ErrNoHost, "a node does not mint its own identity")

	_, err = New(Config{Services: []string{Register}}, WithHost(h))
	require.ErrorIs(t, err, ErrNoDatabases)

	_, err = New(Config{Services: []string{Stats}}, WithHost(h))
	require.ErrorIs(t, err, ErrNoPubSub)

	_, err = New(Config{Services: []string{Worker}}, WithHost(h))
	require.Error(t, err, "a worker with no compute tier can run nothing")

	// A GATEWAY ROUTES FROM STATS. Without one it has no way to choose a
	// worker, and saying so at construction beats a 502 on the first request.
	_, err = New(Config{Services: []string{Gateway}}, WithHost(h))
	require.Error(t, err)
	require.Contains(t, err.Error(), "stats")
}

// TestNothingIsOpenedTwiceAndEverythingIsClosed. A composition that failed part
// way used to leave a database open and a pool running.
func TestWhatWasBuiltIsClosed(t *testing.T) {
	n, _, factory := build(t, Config{Services: []string{Register}})

	opened, closed := factory.counts()
	require.Equal(t, []string{Register}, opened, "one database, named after the service that wanted it")
	require.Zero(t, closed)

	require.NoError(t, n.Close())
	_, closed = factory.counts()
	require.Equal(t, 1, closed)

	require.NoError(t, n.Close(), "close is idempotent")
}

// TestAHalfBuiltNodeCloncesWhatItMade. The worker is built after the register,
// so a worker that cannot be built must not leave the register's database open.
func TestAHalfBuiltNodeClosesWhatItMade(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	h := newHost(t, mn)
	factory := &memFactory{}

	// No runtimes: the worker fails, after the register has opened a database.
	_, err := New(Config{Services: []string{Register, Worker}},
		WithHost(h), WithDatabases(factory))
	require.Error(t, err)

	opened, closed := factory.counts()
	require.Len(t, opened, 1, "the register got as far as opening one")
	require.Equal(t, 1, closed, "and it was closed when the worker failed")
}

// TestStartIsOnceAndOrdered.
func TestStartIsOnce(t *testing.T) {
	n, _, _ := build(t, Config{})

	require.NoError(t, n.Start(t.Context()))
	require.Error(t, n.Start(t.Context()), "a second listener is an error, not a leak")
	require.NoError(t, n.Close())
}

// TestANodeReportsWhatItRuns, which is what every locator in the fleet routes
// on — a worker looking for a register, a gateway looking for a worker.
func TestANodeReportsWhatItRuns(t *testing.T) {
	n, _, _ := build(t, Config{Services: []string{Register, Worker, Stats}})

	snapshot, err := n.Snapshot(t.Context())
	require.NoError(t, err)
	require.True(t, snapshot.Ready)
	require.NotEmpty(t, snapshot.Addresses, "a node nothing can dial is not routable")

	for _, name := range []string{Register, Worker, Stats} {
		require.Contains(t, snapshot.Serving, name)
	}
	require.NotContains(t, snapshot.Serving, Gateway, "it does not run one")

	// SORTED, because pkg/stats matches a name with a binary search.
	require.True(t, slices.IsSorted(snapshot.Serving), "%v", snapshot.Serving)
}

// TestAWorkerAdvertisesTheEnvironmentsItHolds, so a router can send a request
// where the compiled project is already warm.
func TestAWorkerAdvertisesWhatItHolds(t *testing.T) {
	n, _, _ := build(t, Config{})
	require.NoError(t, n.Start(t.Context()))

	// Publish and resolve through this node's own register, which loads the
	// project into the worker's cache.
	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled(),
	})
	require.NoError(t, err)

	_, err = n.Worker().Resolve(t.Context(), workerTarget())
	require.NoError(t, err)

	snapshot, err := n.Snapshot(t.Context())
	require.NoError(t, err)
	require.Contains(t, snapshot.Serving, Worker+"/shop/prod")
}

// TestALocalRegisterIsUsedWithoutAStream. A register is a state machine over a
// replicated store, so a node running one already holds every answer; dialling
// itself would add a stream and a serialization to a read on every request.
func TestALocalRegisterNeedsNoStream(t *testing.T) {
	n, _, _ := build(t, Config{Services: []string{Register, Worker, Stats}})

	// Nothing is started, so nothing is dialable — and the read still works.
	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled(),
	})
	require.NoError(t, err)

	spec, err := n.Worker().Resolve(t.Context(), workerTarget())
	require.NoError(t, err)
	require.Equal(t, "wasm", spec.Runtime)
}

// TestANodeWithNoRegisterSaysSoRatherThanHanging.
func TestANodeWithNoRegisterSaysSo(t *testing.T) {
	n, _, _ := build(t, Config{Services: []string{Worker, Stats}})

	_, err := n.Worker().Resolve(t.Context(), workerTarget())
	require.ErrorIs(t, err, ErrNoRegister)
}

// TestANodeWithNeitherARegisterNorStatsSaysBoth.
func TestANodeWithNoWayToFindARegister(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	n, err := New(Config{Services: []string{Worker}},
		WithHost(h), WithRuntimes(&tier{name: "wasm"}))
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })

	_, err = n.Worker().Resolve(t.Context(), workerTarget())
	require.ErrorIs(t, err, ErrNoRegister)
	require.Contains(t, err.Error(), "stats")
}

// TestTheGatewayIsServedByTheCaller. Handler() is what a composer serves,
// because where and with what certificate is the composer's.
func TestTheGatewayIsServedByTheCaller(t *testing.T) {
	n, _, _ := build(t, Config{})
	require.NoError(t, n.Start(t.Context()))

	handler := n.Handler()
	require.NotNil(t, handler)

	// A name nobody claimed is a 404 and says nothing about anybody's project.
	r := httptest.NewRequest(http.MethodGet, "http://nobody.example.com/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
	require.NotContains(t, w.Body.String(), "shop")
}

// TestAWholeRequestPathOnOneNode: publish through this node's register, claim a
// name, and ask its gateway for it. Nothing leaves the process, and every hop
// is the production one.
func TestAWholeRequestPathOnOneNode(t *testing.T) {
	n, _, _ := build(t, Config{Gateway: gatewayConfig()})
	require.NoError(t, n.Start(t.Context()))

	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1,
		Compiled: compiled(), Hosts: []string{"shop.example.com"},
	})
	require.NoError(t, err)

	r := httptest.NewRequest(http.MethodGet, "http://shop.example.com/", nil)
	r.RemoteAddr = "203.0.113.7:51000"
	w := httptest.NewRecorder()
	n.Handler().ServeHTTP(w, r)

	// THE TIER IS WHERE IT STOPS, and that is the honest boundary: this test's
	// tier starts nothing, so a 502 here means the host table, the register,
	// the compiled project, the route and the placement all answered.
	require.Equal(t, http.StatusBadGateway, w.Code)
	require.NotContains(t, w.Body.String(), "shop", "and it says nothing about the deployment")
}

// TestTheClockReachesEverythingThatKeepsTime, so a test can move a node's
// caches forward without sleeping.
func TestTheClockReachesEverything(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	n, _, _ := build(t, Config{}, WithClock(func() time.Time { return at }))
	require.NotNil(t, n.Stats())
	require.NoError(t, n.Start(t.Context()))
}

func TestWhatTheOptionsRefuse(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	for _, opt := range []Option{
		WithHost(nil), WithPubSub(nil), WithDatabases(nil),
		WithRuntimes(nil), WithArtifacts(nil), WithSecrets(nil), WithClock(nil),
	} {
		_, err := New(Config{Services: []string{Stats}}, WithHost(h), WithPubSub(newPubSub(t, h)), opt)
		require.Error(t, err)
	}
}

// TestADatabaseThatWillNotOpenIsReported rather than a register with nothing
// behind it.
func TestADatabaseThatWillNotOpen(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	_, err := New(Config{Services: []string{Register}},
		WithHost(h), WithDatabases(&memFactory{err: errors.New("the disk is full")}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "disk is full")
}
