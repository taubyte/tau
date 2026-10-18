package node

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"

	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
)

// proxy_test is about the WIRING, not about moving bytes — services/proxy
// carries those over real sockets. What is proved here is that a node holding a
// deployment finds its proxies, listens for them, and says so in the one report
// everything else routes on: the name in Serving so an A record resolves, and
// the socket in Sockets so an SRV record carries the port.

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
			"application": map[string]any{
				proxyAppID: map[string]any{
					"name":   "db",
					"source": map[string]any{"kind": "image", "image": "ghcr.io/acme/postgres:16"},
					"ports":  map[string]any{"pg": map[string]any{"number": 5432}},
				},
			},
			"proxy": map[string]any{proxyResourceID: res},
		},
		"indexes": map[string]any{
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
func proxying(t *testing.T, compiled []byte) *Node {
	t.Helper()

	cfg := Config{Services: []string{Register, Worker, Discovery, Proxy}}
	cfg.Proxy.CloudFqdn = "tau.example.io"

	// SWEEP FAST, BECAUSE THE SWEEP IS WHAT IS BEING TESTED. DefaultSweep is ten
	// seconds and the deployment below is held AFTER Start, so the loop's first
	// pass finds nothing and the second is past any sane deadline.
	//
	// Without this the assertion passed on a race: `go n.proxy.Run(loopCtx)` and
	// the rest of this function are unordered, so the first sweep usually landed
	// after the hold and the test observed a first pass it had no right to. It
	// held until the vocabulary grew by one sync type, which was enough to make
	// the goroutine win — a test that fails when an unrelated package gets
	// slower was never testing what its name says.
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

// TestANODESERVESTHEPROXIESOFWHATITHOLDS, and reports them the two ways a
// client needs: the name resolves to this node, and the port comes back in SRV.
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

// TestAPROXYTHISCLOUDCANNOTSERVE holds no port and appears in no report. The
// reason went to the operator; there is nowhere on a raw socket to put it.
func TestANodeDoesNotListenForAProxyItCannotServe(t *testing.T) {
	n := proxying(t, withProxy(func(r map[string]any) {
		r["tls"] = map[string]any{"mode": "terminate"}
	}))

	require.Never(t, func() bool { return len(n.proxy.Serving()) > 0 }, time.Second, 50*time.Millisecond)

	snap, err := n.Snapshot(t.Context())
	require.NoError(t, err)
	require.Empty(t, snap.Sockets)
}

// TestANODEWITHNOWORKER cannot serve a proxy and says so when it is built,
// rather than starting a sweep that would find nothing forever.
func TestAProxyWithoutAWorkerIsRefusedAtConstruction(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	_, err := New(Config{Services: []string{Register, Discovery, Proxy}},
		WithHost(h), WithPubSub(newPubSub(t, h)), WithDatabases(&memFactory{}))
	require.ErrorContains(t, err, "a worker holds")
}
