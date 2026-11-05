package instance

import (
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/service"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/stats"
	"github.com/taubyte/tau/services"
)

func TestANodeRunsWhatItWasToldTo(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Discovery}})

	require.NotNil(t, n.Register())
	require.NotNil(t, n.Discovery())
	require.Nil(t, n.Worker())
	require.Nil(t, n.Handler(), "it runs no gateway")
	require.Equal(t, []string{Register, Discovery}, n.Services())
}

func TestAnEmptyServiceListIsEveryService(t *testing.T) {
	n, _, _ := makeNode(t, Config{})

	require.ElementsMatch(t, append(slices.Clone(All), services.Default()...), n.Services(), "and every service tau ships but build")
	require.NotNil(t, n.Register())
	require.NotNil(t, n.Worker())
	require.NotNil(t, n.Discovery())
	require.NotNil(t, n.Handler())
}

func TestAMisspelledServiceIsRefused(t *testing.T) {
	_, err := New(mockPeer(t), rooted(t, Config{Services: []string{"registry"}}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "registry")
	require.Contains(t, err.Error(), Register, "and it says what this node can run")
}

func TestEachServiceSaysWhatItCannotStartWithout(t *testing.T) {
	_, err := New(nil, rooted(t, Config{}))
	require.ErrorIs(t, err, ErrNoPeer, "an instance does not mint its own identity")

	// A GATEWAY ROUTES FROM STATS. Without one it has no way to choose a
	// worker, and saying so at construction beats a 502 on the first request.
	_, err = New(mockPeer(t), rooted(t, Config{Services: []string{Gateway}}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "discovery")
}

func TestWhatWasBuiltIsClosed(t *testing.T) {
	n, _, factory := makeNode(t, Config{Services: []string{Auth}})

	opened, closed := factory.counts()
	require.Equal(t, []string{Auth}, opened, "one database, named after the service that wanted it")
	require.Zero(t, closed)

	require.NoError(t, n.Close())
	_, closed = factory.counts()
	require.Equal(t, 1, closed)

	require.NoError(t, n.Close(), "close is idempotent")
}

func TestAHalfBuiltNodeClosesWhatItMade(t *testing.T) {
	factory := &memFactory{}

	// No discovery: the gateway fails, after auth has opened a database.
	_, err := New(mockPeer(t), rooted(t, Config{Services: []string{Auth, Gateway}}), withKVDB(factory))
	require.Error(t, err)

	opened, closed := factory.counts()
	require.Len(t, opened, 1, "auth got as far as opening one")
	require.Equal(t, 1, closed, "and it was closed when the gateway failed")
}

func TestStartIsOnce(t *testing.T) {
	n, _, _ := makeNode(t, Config{})

	require.NoError(t, n.Start(t.Context()))
	require.Error(t, n.Start(t.Context()), "a second listener is an error, not a leak")
	require.NoError(t, n.Close())
}

func TestANodeReportsWhatItRuns(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Discovery}})
	require.NoError(t, n.Start(t.Context()))

	snapshot := reported(t, n, func(stats.Node) bool { return true })
	require.True(t, snapshot.Ready)
	require.NotEmpty(t, snapshot.Addresses, "a node nothing can dial is not routable")

	for _, name := range []string{Register, Worker, Discovery} {
		require.Contains(t, snapshot.Serving, name)
	}
	require.NotContains(t, snapshot.Serving, Gateway, "it does not run one")

	// SORTED, because pkg/stats matches a name with a binary search.
	require.True(t, slices.IsSorted(snapshot.Serving), "%v", snapshot.Serving)
}

func TestAWorkerAdvertisesWhatItHolds(t *testing.T) {
	n, _, _ := makeNode(t, Config{}, prebuilt())
	require.NoError(t, n.Start(t.Context()))

	// Publish and resolve through this node's own register, which loads the
	// project into the worker's cache.
	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled(),
	})
	require.NoError(t, err)

	_, err = n.Worker().Resolve(t.Context(), workerTarget())
	require.NoError(t, err)

	reported(t, n, func(row stats.Node) bool { return slices.Contains(row.Serving, Worker+"/shop/prod") })
}

func TestALocalRegisterNeedsNoStream(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Discovery}}, prebuilt())
	require.NoError(t, n.Start(t.Context()))

	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled(),
	})
	require.NoError(t, err)

	spec, err := n.Worker().Resolve(t.Context(), workerTarget())
	require.NoError(t, err)
	require.Equal(t, "wasm", spec.Runtime)
	_, dialled := n.registers.At()
	require.False(t, dialled, "the register here is read with no stream")
}

func TestANodeWithNoRegisterSaysSo(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker, Discovery}})

	_, err := n.Worker().Resolve(t.Context(), workerTarget())
	require.ErrorIs(t, err, service.ErrNoneRuns)
	require.ErrorContains(t, err, Register)
}

func TestANodeWithNoWayToFindARegister(t *testing.T) {
	n, err := New(mockPeer(t), rooted(t, Config{Services: []string{Worker}}), WithRuntimes(&tier{name: "wasm"}))
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })

	_, err = n.Worker().Resolve(t.Context(), workerTarget())
	require.ErrorIs(t, err, service.ErrNoneRuns)
	require.Contains(t, err.Error(), "no directory")
}

func TestTheGatewayIsServedByTheCaller(t *testing.T) {
	n, _, _ := makeNode(t, Config{})
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

func TestAWholeRequestPathOnOneNode(t *testing.T) {
	n, _, _ := makeNode(t, Config{Gateway: gatewayConfig()}, prebuilt())
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

func TestTheClockReachesEverything(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	n, _, _ := makeNode(t, Config{}, WithClock(func() time.Time { return at }))
	require.NotNil(t, n.Discovery())
	require.NoError(t, n.Start(t.Context()))
}

func TestWhatTheOptionsRefuse(t *testing.T) {
	for _, opt := range []Option{
		WithRuntimes(nil), WithSecrets(nil), WithClock(nil),
	} {
		_, err := New(mockPeer(t), rooted(t, Config{Services: []string{Discovery}}), opt)
		require.Error(t, err)
	}
}

func TestADatabaseThatWillNotOpen(t *testing.T) {
	_, err := New(mockPeer(t), rooted(t, Config{Services: []string{Auth}}), withKVDB(&memFactory{err: errors.New("the disk is full")}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "disk is full")
}

func TestTheAccessorsAreTheServicesItRuns(t *testing.T) {
	bare, _, _ := makeNode(t, Config{Services: []string{Register}})
	require.Nil(t, bare.Secrets())
	require.Nil(t, bare.Database())
	require.Nil(t, bare.TLSConfig(), "no gateway, nothing to answer a handshake with")
	_, _, ok := bare.QueueDepth("shop/prod/QmAnything")
	require.False(t, ok, "a node with no sync backing knows no queue")
	require.Same(t, bare.Messaging(), bare.PubSub())

	full, _, _ := makeNode(t, Config{Services: []string{Register, Discovery, Secrets, Database, Gateway}, Gateway: gatewayConfig()})
	require.Same(t, full.secrets, full.Secrets())
	require.Same(t, full.database, full.Database())
	tlsConfig := full.TLSConfig()
	require.NotNil(t, tlsConfig)
	require.NotNil(t, tlsConfig.GetCertificate)
	require.Equal(t, uint16(tls.VersionTLS12), tlsConfig.MinVersion)
}

func TestTheFleetIsReadFromTheLocalRegister(t *testing.T) {
	ctx := t.Context()
	with, _, _ := makeNode(t, Config{Services: []string{Register}})
	require.NoError(t, with.Start(ctx))
	_, err := with.views.ResolveFleet(ctx)
	require.ErrorIs(t, err, pkgregister.ErrNotPublished, "nothing was published, and the register said so")

	without, _, _ := makeNode(t, Config{Services: []string{Discovery}})
	require.NoError(t, without.Start(ctx))
	s := without.views
	_, err = s.ResolveFleet(ctx)
	require.ErrorIs(t, err, service.ErrNoneRuns)
	_, err = s.Object(ctx, pkgregister.ObjectID("x"))
	require.ErrorIs(t, err, service.ErrNoneRuns)
	_, _, err = s.ResolveHost(ctx, "shop.example.com")
	require.ErrorIs(t, err, service.ErrNoneRuns)
}

func TestWithTLSReachesTheGateway(t *testing.T) {
	var o options
	require.NoError(t, WithTLS()(&o))
	require.True(t, o.terminates)

	n, _, _ := makeNode(t, Config{Services: []string{Register, Discovery, Gateway}, Gateway: gatewayConfig()}, WithTLS())
	require.NotNil(t, n.TLSConfig())
}

// reported is this node's own row in its discovery's view once it satisfies ok:
// what the rest of the fleet routes on, as it arrived.
func reported(t *testing.T, n *Instance, ok func(stats.Node) bool) stats.Node {
	t.Helper()
	var row stats.Node
	require.Eventually(t, func() bool {
		var held bool
		row, held = n.Discovery().Get(n.Host().ID().String())
		return held && ok(row)
	}, 20*time.Second, 20*time.Millisecond)
	return row
}
