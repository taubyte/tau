//go:build dreaming

package dream_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
	nodeDream "github.com/taubyte/tau/instance/dream"
	"github.com/taubyte/tau/pkg/invoke"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

// All of v2 running: a project published through one node's register; a request at
// another node's gateway resolves the name through the fleet, reads the compiled
// project from replicated storage, matches the route and hands the target to a
// worker on a third node. Only the compute tier is faked: it records what it was
// asked to start.

func TestAFleetServesARequestEndToEnd(t *testing.T) {
	recorder := &recordingTier{name: "wasm"}
	defer nodeDream.Runtimes(nodeDream.Runtimes(recorder)...)

	// Three nodes, each running one thing, so none can answer its own question.
	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Services: []string{instance.Register, instance.Discovery},
	})
	require.NoError(t, err)
	defer u.Close()

	pids := u.Pids()
	require.NotEmpty(t, pids)

	registry := nodeOf(t, pids[0])
	require.NotNil(t, registry.Register(), "the first node runs the register")

	stagedOn(t, pids, "shop", map[string]string{checkoutPackageID: anArtifact})

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
	recorder := &recordingTier{name: "wasm"}
	defer nodeDream.Runtimes(nodeDream.Runtimes(recorder)...)

	// Two nodes of one shape both run everything; the gateway on one reaches a worker
	// wherever placement puts it.
	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{Copies: 2})
	require.NoError(t, err)
	defer u.Close()

	pids := u.Pids()
	require.Len(t, pids, 2)

	edge, compute := nodeOf(t, pids[0]), nodeOf(t, pids[1])

	// A universe has no build service; the recording tier never reads the cid.
	stagedOn(t, pids, "shop", map[string]string{checkoutPackageID: anArtifact})

	_, err = edge.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1,
		Compiled: compiled(), Hosts: []string{"shop.example.com"},
	})
	require.NoError(t, err)

	// Both nodes see each other before anything is routed.
	require.Eventually(t, func() bool {
		return len(edge.Discovery().Route(instance.Worker, time.Minute)) > 0 &&
			len(compute.Discovery().Route(instance.Register, time.Minute)) > 0
	}, 30*time.Second, 200*time.Millisecond, "the fleet never saw itself")

	// A REQUEST, THROUGH THE GATEWAY, ALL THE WAY TO A TIER.
	require.Eventually(t, func() bool {
		r := httptest.NewRequest(http.MethodGet, "http://shop.example.com/", nil)
		r.RemoteAddr = "203.0.113.7:51000"

		w := httptest.NewRecorder()
		edge.Handler().ServeHTTP(w, r)

		// The publish reaches the other node's register when the CRDT gets
		// there; until then the name is not found. Anything else means the
		// whole chain ran.
		return w.Code != http.StatusNotFound && len(recorder.specs()) > 0
	}, 30*time.Second, 250*time.Millisecond, "no worker was ever asked to start anything")

	spec := recorder.specs()[0]
	require.Equal(t, "wasm", spec.Runtime, "read off the kind's own Engines()")
	require.Equal(t, int64(33554432), spec.Memory, "and the limit the author wrote")
	require.Contains(t, spec.Name, "shop/prod/function/")
}

func nodeOf(t *testing.T, peerID string) *instance.Instance {
	t.Helper()
	n, ok := nodeDream.Node(peerID)
	require.True(t, ok, "no node on %s", peerID)
	return n
}

// recordingTier records what it was asked to start.
type recordingTier struct {
	name string

	mu      sync.Mutex
	started []invoke.Spec
}

func (r *recordingTier) Name() string { return r.name }

func (r *recordingTier) Start(_ context.Context, spec invoke.Spec) (invoke.Instance, error) {
	r.mu.Lock()
	r.started = append(r.started, spec)
	r.mu.Unlock()
	return noopInstance{}, nil
}

func (r *recordingTier) specs() []invoke.Spec {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]invoke.Spec(nil), r.started...)
}

type noopInstance struct{}

func (noopInstance) Serve(context.Context, *invoke.Request) (*invoke.Response, error) {
	return &invoke.Response{Status: http.StatusOK, Body: []byte("served")}, nil
}

func (noopInstance) Close() error { return nil }
