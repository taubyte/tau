package instance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"

	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
)

// The wiring: a node holding a deployment finds its proxies, listens, and reports the
// name in Serving and the socket in Sockets. Moving bytes is services/proxy's test.

const (
	proxyResourceID = "QmPZv3VQe4s7A18auX3Tjnc3Bxd6cURg94qXCJ2Smbdvqf"
	proxyAppID      = "QmZCstKkwsL212pMaf4xk4V5AbgVfJC2NNSJdu8UZtqSpS"
)

// withProxy is a project whose one proxy forwards to one application.
func withProxy(change func(map[string]any)) []byte {
	res := map[string]any{
		"name":    "pgwire",
		"service": "postgres",
		"host":    map[string]any{"kind": "generated", "name": "db"},
		"forward": map[string]any{"kind": "application", "name": "db", "port": "pg"},
	}
	if change != nil {
		change(res)
	}

	doc := map[string]any{
		"object": map[string]any{
			"id":   projectID,
			"name": "shop",
			"package": map[string]any{
				imagePackageID: map[string]any{"name": "postgres", "type": "oci",
					"source": map[string]any{"kind": "image", "image": "ghcr.io/acme/postgres:16"}},
			},
			"application": map[string]any{
				proxyAppID: map[string]any{
					"name":    "db",
					"package": map[string]any{"name": imagePackageID},
					"ports":   map[string]any{"pg": map[string]any{"number": 5432}},
				},
			},
			"proxy": map[string]any{proxyResourceID: res},
		},
		"indexes": map[string]any{
			"projects/" + projectID + "/package/postgres/links": []any{
				"branches/main/projects/" + projectID + "/package/" + imagePackageID},
			"projects/" + projectID + "/application/db/links": []any{
				"branches/main/projects/" + projectID + "/application/" + proxyAppID},
			"projects/" + projectID + "/proxy/pgwire/links": []any{
				"branches/main/projects/" + projectID + "/proxy/" + proxyResourceID},
		},
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return raw
}

// proxying is a node that runs a proxy, holding a deployment that declares one.
func proxying(t *testing.T, compiled []byte) *Instance {
	t.Helper()

	cfg := Config{Services: []string{Register, Worker, Discovery, Proxy}}
	cfg.Proxy.CloudFqdn = "tau.example.io"

	// A fast sweep, since the deployment is held after Start and the sweep is what is
	// tested; with the default the first pass would race the hold.
	cfg.Proxy.Sweep = 20 * time.Millisecond

	n, _, _ := makeNode(t, cfg)
	require.NoError(t, n.Start(t.Context()))

	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled,
	})
	require.NoError(t, err)

	// THE DEPLOYMENT HAS TO BE HELD BEFORE ITS PROXIES ARE VISIBLE. The
	// register does not enumerate, so what a node can serve is what it is
	// already serving — this is the touch that makes it so.
	_, err = n.Worker().Resolve(t.Context(),
		specs.Address{Project: "shop", Environment: "prod", Kind: "application", Name: "db"})
	require.NoError(t, err)

	return n
}

func TestANodeListensForItsHeldProxiesAndReportsThem(t *testing.T) {
	n := proxying(t, withProxy(nil))

	require.Eventually(t, func() bool { return len(n.proxy.Serving()) == 1 }, 5*time.Second, 20*time.Millisecond,
		"the sweep did not pick up the held deployment's proxy")

	sock := n.proxy.Serving()[0]
	require.Equal(t, "postgres", sock.Protocol)
	require.Equal(t, "tcp", sock.Transport)
	require.NotZero(t, sock.Port, "the kernel allocated one")

	snap, err := n.Snapshot(t.Context())
	require.NoError(t, err)

	require.Len(t, snap.Sockets, 1, "the port is in the report")
	require.Equal(t, sock.Host, snap.Sockets[0].Host)
	require.Equal(t, sock.Port, snap.Sockets[0].Port)
	require.Contains(t, snap.Serving, sock.Host,
		"and so is the name, or a client resolves the machine and never finds it")
}

func TestANodeDoesNotListenForAProxyItCannotServe(t *testing.T) {
	n := proxying(t, withProxy(func(r map[string]any) {
		r["tls"] = map[string]any{"mode": "terminate"}
	}))

	require.Never(t, func() bool { return len(n.proxy.Serving()) > 0 }, time.Second, 50*time.Millisecond)

	snap, err := n.Snapshot(t.Context())
	require.NoError(t, err)
	require.Empty(t, snap.Sockets)
}

func TestAProxyWithoutAWorkerIsRefusedAtConstruction(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	_, err := New(Config{Services: []string{Register, Discovery, Proxy}},
		WithHost(h), WithPubSub(newPubSub(t, h)), WithDatabases(&memFactory{}))
	require.ErrorContains(t, err, "a worker holds")
}

func TestAProxyWithoutPlacementIsRefusedAtConstruction(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	_, err := New(Config{Services: []string{Register, Worker, Proxy}},
		WithHost(h), WithPubSub(newPubSub(t, h)), WithDatabases(&memFactory{}),
		WithRuntimes(&tier{name: "oci"}))
	require.ErrorContains(t, err, "through placement")
}

func TestANodeWithNoWorkerHoldsNoProxy(t *testing.T) {
	require.Nil(t, (&proxies{node: &Instance{}}).Proxies(t.Context()))
}

func TestAProxyTakesTheInjectedResolver(t *testing.T) {
	injected := secretFor(func(context.Context, string, string, string) (string, error) { return "", nil })
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Discovery, Proxy}}, WithSecrets(injected))
	require.NotNil(t, n.proxy)
}
