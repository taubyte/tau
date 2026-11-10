package instance

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	taupeer "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/peer/peertest"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/pkg/stats"
	"github.com/taubyte/tau/services/proxy"
)

// The wiring: a node holding a deployment finds its proxies, listens, and reports the
// name in Serving and the socket in Sockets. Moving bytes is services/proxy's test.

const (
	proxyResourceID = "QmPZv3VQe4s7A18auX3Tjnc3Bxd6cURg94qXCJ2Smbdvqf"
	proxyAppID      = "QmZCstKkwsL212pMaf4xk4V5AbgVfJC2NNSJdu8UZtqSpS"
)

// withProxy is a project whose one proxy forwards to one service.
func withProxy(change func(map[string]any)) []byte {
	res := map[string]any{
		"name":    "pgwire",
		"service": "postgres",
		"host":    map[string]any{"kind": "generated", "name": "db"},
		"forward": map[string]any{"kind": "service", "name": "db", "port": "pg"},
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
			"service": map[string]any{
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
			"projects/" + projectID + "/service/db/links": []any{
				"branches/main/projects/" + projectID + "/service/" + proxyAppID},
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
func proxying(t *testing.T, mn *taupeer.Mocknet, compiled []byte) *Instance {
	t.Helper()

	cfg := Config{Services: []string{Register, Worker, Stats, Proxy}}
	cfg.CloudFqdn = "tau.example.io"

	// A fast sweep, since the deployment is held after Start and the sweep is what is
	// tested; with the default the first pass would race the hold.
	cfg.Sections = map[string]any{Proxy: proxy.Config{Sweep: 20 * time.Millisecond}}

	n, _, _ := makeNode(t, mn, cfg)
	require.NoError(t, n.Start(t.Context()))

	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled,
	})
	require.NoError(t, err)

	// THE DEPLOYMENT HAS TO BE HELD BEFORE ITS PROXIES ARE VISIBLE. The
	// register does not enumerate, so what a node can serve is what it is
	// already serving — this is the touch that makes it so.
	_, err = n.Worker().Resolve(t.Context(),
		specs.Address{Project: "shop", Environment: "prod", Kind: "service", Name: "db"})
	require.NoError(t, err)

	return n
}

func TestANodeListensForItsHeldProxiesAndReportsThem(t *testing.T) {
	mn := peertest.Net(t)
	n := proxying(t, mn, withProxy(nil))

	require.Eventually(t, func() bool { return len(proxyOf(t, n).Serving()) == 1 }, 5*time.Second, 20*time.Millisecond,
		"the sweep did not pick up the held deployment's proxy")

	sock := proxyOf(t, n).Serving()[0]
	require.Equal(t, "postgres", sock.Protocol)
	require.Equal(t, "tcp", sock.Transport)
	require.NotZero(t, sock.Port, "the kernel allocated one")

	snap := reported(t, n, func(row stats.Node) bool { return len(row.Sockets) > 0 })
	require.Len(t, snap.Sockets, 1, "the port is in the report")
	require.Equal(t, sock.Host, snap.Sockets[0].Host)
	require.Equal(t, sock.Port, snap.Sockets[0].Port)
	require.Contains(t, snap.Serving, sock.Host,
		"and so is the name, or a client resolves the machine and never finds it")
}

func TestANodeDoesNotListenForAProxyItCannotServe(t *testing.T) {
	mn := peertest.Net(t)
	n := proxying(t, mn, withProxy(func(r map[string]any) {
		r["tls"] = map[string]any{"mode": "terminate"}
	}))

	require.Never(t, func() bool { return len(proxyOf(t, n).Serving()) > 0 }, time.Second, 50*time.Millisecond)

	snap := reported(t, n, func(stats.Node) bool { return true })
	require.Empty(t, snap.Sockets)
}

func TestAProxyWithoutAWorkerDoesNotStart(t *testing.T) {
	mn := peertest.Net(t)
	n, _, _ := makeNode(t, mn, Config{Services: []string{Register, Stats, Proxy}})
	require.ErrorIs(t, n.Start(t.Context()), proxy.ErrNoWorker)
}

func TestANodeNamingAProxyRunsTheStatsItIsPlacedFrom(t *testing.T) {
	mn := peertest.Net(t)
	n, _, _ := makeNode(t, mn, Config{Services: []string{Register, Worker, Proxy}})
	require.Contains(t, n.Services(), Stats)
}

// proxyOf is the proxy n runs.
func proxyOf(t *testing.T, n *Instance) *proxy.Service {
	t.Helper()
	for _, r := range n.runningNow() {
		if p, ok := r.Service.(*proxy.Service); ok {
			return p
		}
	}
	t.Fatal("the node runs no proxy")
	return nil
}
