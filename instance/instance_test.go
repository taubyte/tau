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
	"github.com/taubyte/tau/pkg/certificate"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/stats"
	"github.com/taubyte/tau/services"
	"github.com/taubyte/tau/services/cm"
	"github.com/taubyte/tau/services/gateway"
	"github.com/taubyte/tau/services/plm/jobs"
	registerService "github.com/taubyte/tau/services/register"
)

func TestANodeRunsWhatItWasToldTo(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Stats}})

	require.NotNil(t, n.Register())
	require.NotNil(t, n.Stats())
	require.Nil(t, n.Worker())
	require.Nil(t, n.Handler(), "it runs no gateway")
	require.Equal(t, []string{Register, Stats}, n.Services())
}

func TestAnEmptyServiceListIsEveryService(t *testing.T) {
	n, _, _ := makeNode(t, Config{})

	require.ElementsMatch(t, services.Default(), n.Services(), "and every service tau ships but build")
	require.NotNil(t, n.Register())
	require.NotNil(t, n.Stats())
	require.Nil(t, n.Worker(), "the worker and the gateway run once the node starts")
	require.Nil(t, n.Handler())
	require.NoError(t, n.Start(t.Context()))
	require.NotNil(t, n.Worker())
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

	// A GATEWAY ROUTES FROM STATS, so a node naming one runs stats beside it.
	n, _, _ := makeNode(t, Config{Services: []string{Gateway}})
	require.Contains(t, n.Services(), Stats)
}

func TestWhatWasBuiltIsClosed(t *testing.T) {
	n, _, factory := makeNode(t, Config{Services: []string{Worker}})
	require.NoError(t, n.Start(t.Context()))

	opened, closed := factory.counts()
	require.Equal(t, []string{jobs.Records}, opened, "one database: plm's records, which the worker holds from the start")
	require.Zero(t, closed)

	require.NoError(t, n.Close())
	_, closed = factory.counts()
	require.Equal(t, 1, closed)

	require.NoError(t, n.Close(), "close is idempotent")
}

func TestAHalfStartedNodeClosesWhatItMade(t *testing.T) {
	// A cloud issuing certificates with no zone: the gateway fails at its start,
	// after the worker has opened a database.
	acme := cm.Config{Certificate: cm.CertificateConfig{ACME: certificate.ACME{Enabled: true}}}
	n, _, factory := makeNode(t, Config{Services: []string{Worker, Gateway}, CM: acme})
	require.ErrorIs(t, n.Start(t.Context()), ErrNoZoneForCertificates)

	opened, _ := factory.counts()
	require.Contains(t, opened, jobs.Records, "the worker got as far as opening one")
	require.NoError(t, n.Close())
	_, closed := factory.counts()
	require.Positive(t, closed, "and it was closed with the node the gateway failed")
}

func TestStartIsOnce(t *testing.T) {
	n, _, _ := makeNode(t, Config{})

	require.NoError(t, n.Start(t.Context()))
	require.Error(t, n.Start(t.Context()), "a second listener is an error, not a leak")
	require.NoError(t, n.Close())
}

func TestANodeReportsWhatItRuns(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}})
	require.NoError(t, n.Start(t.Context()))

	snapshot := reported(t, n, func(stats.Node) bool { return true })
	require.True(t, snapshot.Ready)
	require.NotEmpty(t, snapshot.Addresses, "a node nothing can dial is not routable")

	for _, name := range []string{Register, Worker, Stats} {
		require.Contains(t, snapshot.Serving, name)
	}
	require.NotContains(t, snapshot.Serving, Gateway, "it does not run one")

	// SORTED, because pkg/stats matches a name with a binary search.
	require.True(t, slices.IsSorted(snapshot.Serving), "%v", snapshot.Serving)
}

func TestAWorkerAdvertisesWhatItHolds(t *testing.T) {
	n, _, _ := makeNode(t, Config{})
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
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}})
	require.NoError(t, n.Start(t.Context()))

	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled(),
	})
	require.NoError(t, err)

	spec, err := n.Worker().Resolve(t.Context(), workerTarget())
	require.NoError(t, err)
	// A HOST CANNOT DIAL ITSELF, so a resolve that answers was read from the register
	// beside the worker, with no stream.
	require.Equal(t, "oci", spec.Runtime)
}

func TestANodeWithNoRegisterSaysSo(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker, Stats}})
	require.NoError(t, n.Start(t.Context()))

	_, err := n.Worker().Resolve(t.Context(), workerTarget())
	require.ErrorIs(t, err, service.ErrNoneRuns)
	require.ErrorContains(t, err, Register)
}

func TestANodeThatFindsNoRegister(t *testing.T) {
	n, err := New(mockPeer(t), rooted(t, Config{Services: []string{Worker}}))
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })
	require.NoError(t, n.Start(t.Context()))

	_, err = n.Worker().Resolve(t.Context(), workerTarget())
	require.ErrorIs(t, err, service.ErrNoneRuns)
	require.Contains(t, err.Error(), string(registerService.Protocol))
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
	n, _, _ := makeNode(t, Config{})
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

func TestADatabaseThatWillNotOpen(t *testing.T) {
	n, err := New(mockPeer(t), rooted(t, Config{Services: []string{Worker}}), withKVDB(&memFactory{err: errors.New("the disk is full")}))
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })
	require.ErrorContains(t, n.Start(t.Context()), "disk is full")
}

func TestTheAccessorsAreTheServicesItRuns(t *testing.T) {
	bare, _, _ := makeNode(t, Config{Services: []string{Register}})
	require.Nil(t, bare.Secrets())
	require.Nil(t, bare.Database())
	require.Nil(t, bare.TLSConfig(), "no gateway, nothing to answer a handshake with")
	_, _, ok := bare.QueueDepth("shop/prod/QmAnything")
	require.False(t, ok, "a node with no sync backing knows no queue")
	require.Same(t, bare.Messaging(), bare.PubSub())

	full, _, _ := makeNode(t, Config{Services: []string{Register, Stats, Secrets, Database, Gateway}})
	require.NoError(t, full.Start(t.Context()))
	require.Same(t, full.secrets, full.Secrets())
	require.Same(t, full.database, full.Database())
	tlsConfig := full.TLSConfig()
	require.NotNil(t, tlsConfig)
	require.NotNil(t, tlsConfig.GetCertificate)
	require.Equal(t, uint16(tls.VersionTLS12), tlsConfig.MinVersion)
}

func TestANodeWithNoRegisterReadsNoneOfItsAnswers(t *testing.T) {
	ctx := t.Context()
	without, _, _ := makeNode(t, Config{Services: []string{Stats}})
	require.NoError(t, without.Start(ctx))
	s := registerService.ViewsOn(without.HostOf(""))
	t.Cleanup(func() { s.Close() })
	_, err := s.Object(ctx, pkgregister.ObjectID("x"))
	require.ErrorIs(t, err, service.ErrNoneRuns)
	_, _, err = s.ResolveHost(ctx, "shop.example.com")
	require.ErrorIs(t, err, service.ErrNoneRuns)
}

func TestAGatewayServedOverTLSSaysSoInItsSection(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Stats, Gateway}, Sections: map[string]any{Gateway: gateway.Config{TLS: true}}})
	require.NoError(t, n.Start(t.Context()))
	require.NotNil(t, n.TLSConfig())
}

// reported is this node's own row in its stats view once it satisfies ok:
// what the rest of the fleet routes on, as it arrived.
func reported(t *testing.T, n *Instance, ok func(stats.Node) bool) stats.Node {
	t.Helper()
	var row stats.Node
	require.Eventually(t, func() bool {
		var held bool
		row, held = n.Stats().Get(n.Host().ID().String())
		return held && ok(row)
	}, 20*time.Second, 20*time.Millisecond)
	return row
}
