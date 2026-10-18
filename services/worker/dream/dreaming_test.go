//go:build dreaming

package dream_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/mesh"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/register"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	registerService "github.com/taubyte/tau/services/register"
	registerDream "github.com/taubyte/tau/services/register/dream"
	"github.com/taubyte/tau/services/worker"
	workerDream "github.com/taubyte/tau/services/worker/dream"

	workerv1 "github.com/taubyte/tau/pkg/proto/gen/tau/worker/v1"
	"github.com/taubyte/tau/pkg/proto/gen/tau/worker/v1/workerv1connect"
)

// THIS IS THE WHOLE CHAIN, ON REAL NODES.
//
// A project is compiled by tcc and published into a register running on one
// node. A worker on another node is asked, over a libp2p stream, to invoke a
// resource by name. To answer, it dials the register across the swarm, reads
// the compiled project out of a replicated CRDT, resolves the name through the
// index, and composes a spec from what the author wrote.
//
// Every hop there is the production one. Nothing is faked but the universe and
// the compute tier — and the tier is a deliberate boundary: starting a
// container needs a daemon, so a universe supplies its own tiers and this test
// supplies one that records what it was asked to start. What the tier does with
// a spec is pkg/invoke/container's test, against real backends.
func TestAWorkerResolvesAgainstARegisterAcrossTheSwarm(t *testing.T) {
	recorder := &recordingTier{name: "container"}
	defer workerDream.Runtimes(workerDream.Runtimes(recorder)...)

	m, err := mesh.New(t.Context(), t.Name())
	require.NoError(t, err)
	defer m.Close()

	registerNode, err := registerDream.Start(m)
	require.NoError(t, err)
	workerNode, err := workerDream.Start(m, registerNode.ID())
	require.NoError(t, err)

	registers := []string{registerNode.ID().String()}
	workers := []string{workerNode.ID().String()}

	nodes := nodesByID(m)

	// Publish, over the wire, into the register the worker will have to find.
	publisher, err := registerService.NewClient(nodes[workers[0]].Peer(), decode(t, registers[0]))
	require.NoError(t, err)
	defer publisher.Close()

	_, err = publisher.Publish(t.Context(), register.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled(t),
	})
	require.NoError(t, err)

	// And invoke, over the wire, on the worker.
	client := dialWorker(t, nodes[registers[0]], workers[0])

	require.Eventually(t, func() bool {
		_, err := client.Invoke(t.Context(), connect.NewRequest(&workerv1.InvokeRequest{
			Target: &workerv1.Target{
				Project: "shop", Environment: "prod", Kind: "application", Name: "db",
			},
			Method: "GET", Path: "/",
		}))
		// The register's store is a CRDT, so the publish reaches the worker's
		// register when it reaches it. Anything other than "not published yet"
		// means the lookup ran.
		return err == nil || connect.CodeOf(err) != connect.CodeNotFound
	}, 30*time.Second, 250*time.Millisecond, "the worker never saw the published project")

	started := recorder.specs()
	require.Len(t, started, 1, "the lookup reached the tier exactly once")

	spec := started[0]
	require.Equal(t, "container", spec.Runtime, "read off the kind's own Engines()")
	require.Equal(t, "ghcr.io/acme/postgres:16", spec.Artifact)
	require.Equal(t, int64(2_000_000_000), spec.Memory, "2GB, in bytes, as the author wrote it")
	require.Contains(t, spec.Name, "shop/prod/application/")
	require.NotEmpty(t, spec.Revision)
}

// TestAUniverseWithNoTierSaysSoRatherThanPretending. It is the same chain up to
// the last step, and the last step is the one a process without a container
// daemon cannot take.
func TestAUniverseWithNoTierSaysSo(t *testing.T) {
	defer workerDream.Runtimes(workerDream.Runtimes()...)

	m, err := mesh.New(t.Context(), t.Name())
	require.NoError(t, err)
	defer m.Close()

	registerNode, err := registerDream.Start(m)
	require.NoError(t, err)
	workerNode, err := workerDream.Start(m, registerNode.ID())
	require.NoError(t, err)

	registers := []string{registerNode.ID().String()}
	workers := []string{workerNode.ID().String()}
	nodes := nodesByID(m)

	publisher, err := registerService.NewClient(nodes[workers[0]].Peer(), decode(t, registers[0]))
	require.NoError(t, err)
	defer publisher.Close()

	_, err = publisher.Publish(t.Context(), register.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled(t),
	})
	require.NoError(t, err)

	client := dialWorker(t, nodes[registers[0]], workers[0])

	var last error
	require.Eventually(t, func() bool {
		_, last = client.Invoke(t.Context(), connect.NewRequest(&workerv1.InvokeRequest{
			Target: &workerv1.Target{
				Project: "shop", Environment: "prod", Kind: "application", Name: "db",
			},
		}))
		return last != nil && connect.CodeOf(last) == connect.CodeFailedPrecondition
	}, 30*time.Second, 250*time.Millisecond, "expected the tier to be the thing missing, got %v", last)

	require.Contains(t, last.Error(), "container", "and it names the tier the config asked for")
}

func nodesByID(m *mesh.Mesh) map[string]peer.Node {
	out := map[string]peer.Node{}
	for _, n := range m.Peers() {
		out[n.ID().String()] = n
	}
	return out
}

func decode(t *testing.T, id string) libp2ppeer.ID {
	t.Helper()
	p, err := libp2ppeer.Decode(id)
	require.NoError(t, err)
	return p
}

func dialWorker(t *testing.T, from peer.Node, to string) workerv1connect.WorkerServiceClient {
	t.Helper()
	c, err := rpc.NewClient(from.Peer(), decode(t, to), worker.Protocol)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return workerv1connect.NewWorkerServiceClient(c.HTTP(), c.URL())
}

func compiled(t *testing.T) []byte {
	t.Helper()

	c, err := schema.New(
		schema.WithLocal("../../../pkg/tcc/taubyte/v2/fixtures/basic"),
		schema.WithBranch("main"))
	require.NoError(t, err)

	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err)

	// THE FIXTURE'S `db` DECLARES TWO THINGS THIS CLOUD REFUSES BY NAME — a
	// cloud-wide instance ceiling and a registry credential — and these tests
	// are about the lookup reaching a tier, not about that refusal, which
	// services/worker's unserved_test asserts against the untouched fixture.
	flat := obj.Flat()
	root, _ := flat["object"].(map[string]any)
	applications, _ := root["application"].(map[string]any)
	for _, v := range applications {
		app, _ := v.(map[string]any)
		if app["name"] != "db" {
			continue
		}
		if limits, ok := app["limits"].(map[string]any); ok {
			delete(limits, "max_instances")
		}
		if source, ok := app["source"].(map[string]any); ok {
			delete(source, "image_pull_secret")
		}
	}

	b, err := json.Marshal(flat)
	require.NoError(t, err)
	return b
}

// recordingTier is the compute tier a universe supplies when the point is what
// the lookup produced rather than what running it does.
type recordingTier struct {
	name    string
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
	return &invoke.Response{Status: 200}, nil
}

func (noopInstance) Close() error { return nil }
