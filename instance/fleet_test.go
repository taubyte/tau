package instance

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/peer/peertest"
	"github.com/taubyte/tau/pkg/invoke"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	databaseService "github.com/taubyte/tau/services/database"
	registerService "github.com/taubyte/tau/services/register"
	"github.com/taubyte/tau/services/worker"
	workerHosted "github.com/taubyte/tau/services/worker/hosted"
	"github.com/taubyte/tau/services/worker/placement"
)

// Two nodes test the composition: a node finding what it does not run, over real
// gossipsub, streams and clients.

// fleet is n nodes on one mocknet, each told what to run.
func fleet(t *testing.T, configs ...Config) []*Instance {
	t.Helper()

	// Every peer gossips before it is connected: a pubsub stream is opened at start or
	// when identify reports gossip, never retried, and a late identify push is limited
	// per subnet (every mocknet peer shares one).
	peers := make([]peer.Node, len(configs))
	for i := range configs {
		peers[i] = mockPeer(t)
	}
	require.NoError(t, peertest.LinkAll(t))
	for i, a := range peers {
		for _, b := range peers[i+1:] {
			require.NoError(t, a.Host().Connect(t.Context(), libp2ppeer.AddrInfo{ID: b.ID(), Addrs: b.Host().Addrs()}))
		}
	}

	// Every stream is up before a node is built; building nodes would spend the identify
	// push budget.
	stalled := stallSince()
	for i, a := range peers {
		for j, b := range peers {
			if i == j {
				continue
			}
			require.Eventually(t, func() bool {
				return slices.Contains(a.Messaging().ListPeers(""), b.ID())
			}, 30*time.Second, 10*time.Millisecond, "node %d never opened a stream to node %d%s%s", i, j, gossipWith{a.Host()}, stalled)
		}
	}

	out := make([]*Instance, len(configs))
	for i, cfg := range configs {
		cfg = loopbackDNS(cfg)

		n, err := New(peers[i], rootedIn(t, cfg, t.TempDir()), withKVDB(&memFactory{}), withRaft(mockGroups{}))
		require.NoError(t, err)
		t.Cleanup(func() { n.Close() })

		require.NoError(t, n.Start(t.Context()))
		out[i] = n
	}
	return out
}

// sees waits until one node's view has another serving a name.
// finds waits until a node's finder knows another node serving id: how a lookup
// finds a service, which never reads stats.
func finds(t *testing.T, watcher *Instance, id protocol.ID) {
	t.Helper()
	require.Eventually(t, func() bool {
		return len(watcher.finder.Find(id).Known()) > 0
	}, 30*time.Second, 100*time.Millisecond, "nothing serving %s was ever found", id)
}

// sees waits until a node's stats view shows name: what placement chooses from.
func sees(t *testing.T, watcher *Instance, name string) {
	t.Helper()

	stalled := stallSince()
	require.Eventually(t, func() bool {
		return len(watcher.Stats().Route(name, time.Minute)) > 0
	}, 30*time.Second, 100*time.Millisecond, "%s never appeared in the view%s%s%s", name, viewOf{watcher}, gossipWith{watcher.Host()}, stalled)
}

func TestEveryNodeOfALargeFleetHasAStreamToEveryOther(t *testing.T) {
	configs := make([]Config, 16)
	for i := range configs {
		configs[i] = Config{Services: []string{Stats}}
	}
	nodes := fleet(t, configs...)

	for i, from := range nodes {
		peers := from.Messaging().ListPeers("")
		for j, to := range nodes {
			if i != j {
				require.Contains(t, peers, to.Host().ID(), "node %d has no stream to node %d", i, j)
			}
		}
	}
}

func TestAWorkerFindsARegisterOnAnotherNode(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Register, Stats}},
		Config{Services: []string{Worker, Stats}},
	)
	registry, compute := nodes[0], nodes[1]

	_, err := registry.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled(),
	})
	require.NoError(t, err)

	finds(t, compute, registerService.Protocol)

	spec, err := compute.Worker().Resolve(t.Context(), workerTarget())
	require.NoError(t, err)
	require.Equal(t, "oci", spec.Runtime)
	require.Equal(t, "ghcr.io/acme/checkout:1", spec.Artifact, "the image the project's package names")
}

func TestAGatewayReachesAWorkerOnAnotherNode(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Register, Stats, Gateway}},
		Config{Services: []string{Worker, Stats}},
	)
	edge, compute := nodes[0], nodes[1]

	_, err := edge.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1,
		Compiled: compiled(), Hosts: []string{"shop.example.com"},
	})
	require.NoError(t, err)

	sees(t, edge, Worker)
	finds(t, compute, registerService.Protocol)

	// The gateway's node has no worker of its own, so this can only have gone over
	// the swarm. It reaches the tier and stops there, which is what this test's tier
	// does.
	workers, err := placement.On(edge.HostOf(Gateway))
	require.NoError(t, err)
	t.Cleanup(func() { workers.Close() })
	res, err := workers.Invoke(t.Context(), workerTarget(), &invoke.Request{Method: "GET", Path: "/"})
	require.Error(t, err, "the tier starts nothing in this test")
	require.Nil(t, res)
	require.NotContains(t, err.Error(), "no worker is ready", "a worker was found and asked")
}

func TestAWarmWorkerIsAdvertised(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Register, Stats}},
		Config{Services: []string{Worker, Stats}},
	)
	registry, compute := nodes[0], nodes[1]

	_, err := registry.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled(),
	})
	require.NoError(t, err)
	finds(t, compute, registerService.Protocol)

	_, err = compute.Worker().Resolve(t.Context(), workerTarget())
	require.NoError(t, err)

	// The claim reaches the other node's view on the next report.
	sees(t, registry, worker.Descriptor.Name+"/shop/prod")
}

func TestOneNodeRunningEverythingNeedsNoStream(t *testing.T) {
	nodes := fleet(t, Config{})
	only := nodes[0]

	_, err := only.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled(),
	})
	require.NoError(t, err)

	// A HOST CANNOT DIAL ITSELF, so a resolve that answers was answered by the register
	// beside the worker, with no stream.
	_, err = only.Worker().Resolve(t.Context(), workerTarget())
	require.NoError(t, err)
}

func TestABuiltResourceNothingHasBuiltIsRefused(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats, Secrets}})
	require.NoError(t, n.Start(t.Context()))

	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: builtByThePlatform(),
	})
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, err := n.Worker().Resolve(t.Context(), specs.Address{
			Project: "shop", Environment: "prod", Kind: "function", Name: "built",
		})
		done <- err
	}()

	select {
	case err := <-done:
		require.ErrorIs(t, err, workerHosted.ErrNotBuilt)
	case <-time.After(10 * time.Second):
		t.Fatal("it waited instead of refusing")
	}
}

// kvDatabaseID is a kv database one deployment declares.
const kvDatabaseID = "QmVLDAzEjEbfBPuoyLPWnPMhKp8ceDXwGdmMKY5W3nZWfG"

// withKvDatabase is a compiled project holding one, so the service reads a real
// declaration rather than being told what it is.
func withKvDatabase() []byte {
	doc := map[string]any{
		"object": map[string]any{
			"id": "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR", "name": "shop",
			"database": map[string]any{
				kvDatabaseID: map[string]any{
					"name": "sessions", "type": "kv",
					"kv": map[string]any{"engine": "distributed", "consensus": "crdt"},
				},
			},
		},
		"indexes": map[string]any{
			"projects/QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR/database/sessions/links": []any{
				"branches/main/projects/QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR/database/" + kvDatabaseID},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return raw
}

func TestADatabaseServiceIsReachableFromAnotherNode(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Register, Stats, Database}},
		Config{Services: []string{Worker, Stats}},
	)
	holder, caller := nodes[0], nodes[1]

	_, err := holder.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: withKvDatabase(),
	})
	require.NoError(t, err)

	client, err := databaseService.NewClient(caller.Host(), holder.Host().ID())
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	kv, err := client.OpenKV(t.Context(), invoke.Grant{Handle: "shop/prod/" + kvDatabaseID})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return kv.Put(t.Context(), "session:1", []byte("alice")) == nil
	}, 30*time.Second, 100*time.Millisecond, "the database service was never reachable over the wire")

	value, err := kv.Get(t.Context(), "session:1")
	require.NoError(t, err)
	require.Equal(t, []byte("alice"), value)
}
