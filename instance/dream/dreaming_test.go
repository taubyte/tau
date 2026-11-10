//go:build dreaming

package dream_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
	nodeDream "github.com/taubyte/tau/instance/dream"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

// All of v2 running: a project published through one node's register; a request at
// another node's gateway resolves the name through the fleet, reads the compiled
// project from replicated storage, matches the route and hands the target to a
// worker on a third node, which runs what the cloud built.

func TestAFleetServesARequestEndToEnd(t *testing.T) {
	// Three nodes, each running one thing, so none can answer its own question.
	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Services: []string{instance.Register, instance.Stats},
	})
	require.NoError(t, err)
	defer u.Close()

	pids := u.Pids()
	require.NotEmpty(t, pids)

	registry := nodeOf(t, u, pids[0])
	require.NotNil(t, registry.Register(), "the first node runs the register")

	_, err = registry.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1,
		Compiled: compiled(), Hosts: []string{"shop.example.com"},
	})
	require.NoError(t, err)

	// The register answers its own name.
	project, environment, err := registry.Register().Local().ResolveHost(t.Context(), "shop.example.com")
	require.NoError(t, err)
	require.Equal(t, "shop", project)
	require.Equal(t, "prod", environment)
}

func TestAGatewayNodeReachesAWorkerNode(t *testing.T) {
	// Two nodes of one shape both run everything; the gateway on one reaches a worker
	// wherever placement puts it.
	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{Copies: 2, Services: instance.Everything()})
	require.NoError(t, err)
	defer u.Close()

	pids := u.Pids()
	require.Len(t, pids, 2)

	edge, compute := nodeOf(t, u, pids[0]), nodeOf(t, u, pids[1])

	// BUILT AS A PUSH IS, by the cloud's builder.
	publish(t, u, pids, deployed{name: "with/function/wasm", host: "hello.example.com"}, artifacts{"": handlerWASM(t)})

	// Both nodes see each other before anything is routed.
	require.Eventually(t, func() bool {
		return len(edge.Stats().Route(instance.Worker, time.Minute)) > 0 &&
			len(compute.Stats().Route(instance.Register, time.Minute)) > 0
	}, 30*time.Second, 200*time.Millisecond, "the fleet never saw itself")

	// A REQUEST, THROUGH THE GATEWAY, ALL THE WAY TO THE HANDLER. The publish reaches
	// the other node's register when the CRDT gets there; until then the name is not
	// found.
	var w *httptest.ResponseRecorder
	require.Eventually(t, func() bool {
		r := httptest.NewRequest(http.MethodGet, "http://hello.example.com/", nil)
		r.RemoteAddr = "203.0.113.7:51000"
		w = httptest.NewRecorder()
		edge.Handler().ServeHTTP(w, r)
		return w.Code != http.StatusNotFound
	}, 30*time.Second, 250*time.Millisecond, "the name never reached the gateway's node")
	require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
	require.Equal(t, "/", w.Header().Get("x-path"), "the handler built, answering")
}

func nodeOf(t testing.TB, u *nodeDream.Cloud, peerID string) *instance.Instance {
	t.Helper()
	n, ok := u.Node(peerID)
	require.True(t, ok, "no node on %s", peerID)
	return n
}
