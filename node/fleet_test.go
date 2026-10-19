package node

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/raft"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	databaseService "github.com/taubyte/tau/services/database"
	"github.com/taubyte/tau/services/worker"
)

// TWO NODES IS WHERE THE COMPOSITION IS ACTUALLY TESTED.
//
// One node proves the wiring; it does not prove the part that matters, which is
// that a node finds what it does not run. A worker with no register has to
// learn from discovery which node has one and dial it — and every hop below is the
// production one: a real gossipsub, a real p2p stream, the real client.

// fleet is n nodes on one mocknet, each told what to run.
func fleet(t *testing.T, configs ...Config) []*Node {
	t.Helper()

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	hosts := make([]host.Host, len(configs))
	for i := range configs {
		h, err := mn.GenPeer()
		require.NoError(t, err)
		hosts[i] = h
	}
	require.NoError(t, mn.LinkAll())
	require.NoError(t, mn.ConnectAllButSelf())

	out := make([]*Node, len(configs))
	for i, cfg := range configs {
		ps, err := pubsub.NewGossipSub(t.Context(), hosts[i])
		require.NoError(t, err)

		// See makeNode: :53 is the operator's default and a test's to override,
		// and a fleet needs a DIFFERENT port per node — which is what :0 gives.
		if (len(cfg.Services) == 0 || cfg.runs(DNS)) && cfg.DNSListen == "" {
			cfg.DNSListen = "127.0.0.1:0"
		}

		// EVERY NODE IN A FLEET CAN RESOLVE WHAT A RESOURCE RUNS, because a
		// fleet's tests are about finding what this node does not run — a
		// register, a vault, a worker — and a workload that cannot be resolved
		// at all fails before the question is reached. The one test here that
		// is ABOUT the build backend builds the real resolver itself, so this
		// does not stand in for it.
		n, err := New(cfg,
			WithHost(hosts[i]),
			WithPubSub(ps),
			WithDatabases(&memFactory{}),
			WithRuntimes(&tier{name: "wasm"}),
			WithRaft(func(string) (raft.Cluster, error) { return raft.NewMockCluster(), nil }),
			WithRoot(t.TempDir()),
			prebuilt(),
		)
		require.NoError(t, err)
		t.Cleanup(func() { n.Close() })

		require.NoError(t, n.Start(t.Context()))
		out[i] = n
	}
	return out
}

// sees waits until one node's view has another serving a name.
func sees(t *testing.T, watcher *Node, name string) {
	t.Helper()

	require.Eventually(t, func() bool {
		return len(watcher.Discovery().Route(name, time.Minute)) > 0
	}, 30*time.Second, 100*time.Millisecond, "%s never appeared in the view", name)
}

// TestAWorkerFindsARegisterItDoesNotRun. The whole point of a composition that
// reports what it runs.
func TestAWorkerFindsARegisterOnAnotherNode(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Register, Discovery}},
		Config{Services: []string{Worker, Discovery}},
	)
	registry, compute := nodes[0], nodes[1]

	_, err := registry.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled(),
	})
	require.NoError(t, err)

	sees(t, compute, Register)

	spec, err := compute.Worker().Resolve(t.Context(), workerTarget())
	require.NoError(t, err)
	require.Equal(t, "wasm", spec.Runtime)
	require.Equal(t, int64(33554432), spec.Memory)
}

// TestTheStreamToARemoteRegisterIsKept, so a read on the request path is not a
// dial on the request path.
func TestTheStreamToARemoteRegisterIsKept(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Register, Discovery}},
		Config{Services: []string{Worker, Discovery}},
	)
	registry, compute := nodes[0], nodes[1]

	_, err := registry.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled(),
	})
	require.NoError(t, err)
	sees(t, compute, Register)

	first, err := compute.registers.Now(t.Context())
	require.NoError(t, err)

	again, err := compute.registers.Now(t.Context())
	require.NoError(t, err)
	require.Same(t, first, again, "a second read opened a second stream")
}

// TestAGatewayReachesAWorkerOnAnotherNode — the request path across the swarm,
// with placement choosing the node.
func TestAGatewayReachesAWorkerOnAnotherNode(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Register, Discovery, Gateway}},
		Config{Services: []string{Worker, Discovery}},
	)
	edge, compute := nodes[0], nodes[1]

	_, err := edge.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1,
		Compiled: compiled(), Hosts: []string{"shop.example.com"},
	})
	require.NoError(t, err)

	sees(t, edge, Worker)
	sees(t, compute, Register)

	// The gateway has no worker of its own, so this can only have gone over
	// the swarm. It reaches the tier and stops there, which is what this
	// test's tier does.
	res, err := edge.workers.Invoke(t.Context(), workerTarget(), &invoke.Request{Method: "GET", Path: "/"})
	require.Error(t, err, "the tier starts nothing in this test")
	require.Nil(t, res)
	require.NotContains(t, err.Error(), "no worker is ready", "a worker was found and asked")
}

// TestAChannelDialCrossesToAnotherNode — the point of the kind. The dialing
// node runs no worker at all, so an answer can only have been sought over the
// swarm; what proves it is the error, which is the far node's tier failing
// rather than `no-answer`. `no-answer` is what this would say if the dial had
// never left the machine.
func TestAChannelDialCrossesToAnotherNode(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Register, Discovery, Gateway}},
		Config{Services: []string{Worker, Discovery}},
	)
	edge, compute := nodes[0], nodes[1]

	_, err := edge.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled(),
	})
	require.NoError(t, err)

	sees(t, edge, Worker)
	sees(t, compute, Register)
	require.Nil(t, edge.worker, "the dialing node runs none")

	_, err = (&channels{node: edge}).Dial(t.Context(), channelGrant(), []byte("hello"))
	require.Error(t, err, "the tier starts nothing in this test")
	require.False(t, errors.Is(err, wasmtier.ErrNoAnswer),
		"a worker was found and asked; no-answer would mean none was")
	require.NotContains(t, err.Error(), "no worker is ready")
}

// TestAWarmWorkerIsAdvertisedAndPreferred. A worker that has already loaded an
// environment says so, and placement asks for that name first.
func TestAWarmWorkerIsAdvertised(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Register, Discovery}},
		Config{Services: []string{Worker, Discovery}},
	)
	registry, compute := nodes[0], nodes[1]

	_, err := registry.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled(),
	})
	require.NoError(t, err)
	sees(t, compute, Register)

	_, err = compute.Worker().Resolve(t.Context(), workerTarget())
	require.NoError(t, err)

	// The claim reaches the other node's view on the next report.
	sees(t, registry, worker.Descriptor.Name+"/shop/prod")
}

// TestANodeThatRunsBothUsesNeitherTheSwarmNorAStream.
func TestOneNodeRunningEverythingNeedsNoStream(t *testing.T) {
	nodes := fleet(t, Config{})
	only := nodes[0]

	_, err := only.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled(),
	})
	require.NoError(t, err)

	_, err = only.Worker().Resolve(t.Context(), workerTarget())
	require.NoError(t, err)

	_, dialled := only.registers.At()
	require.False(t, dialled, "it dialled itself")
}

// TestTheSeamsAreHandedThrough: an artifact resolver and a secret resolver
// reach the worker that needs them.
func TestTheSeamsReachTheWorker(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)

	asked := make(chan string, 4)
	n, err := New(Config{Services: []string{Register, Worker}},
		WithHost(h),
		WithDatabases(&memFactory{}),
		WithRuntimes(&tier{name: "wasm"}),
		WithArtifacts(artifactFunc(func(_ context.Context, _, _, kind, id string) (string, error) {
			asked <- "artifact"
			return "registry/built@sha256:" + id, nil
		})),
		WithSecrets(secretFunc(func(_ context.Context, _, _, id string) (string, error) {
			asked <- "secret"
			return "sk_" + id, nil
		})),
	)
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })

	_, err = n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: needsBothSeams(),
	})
	require.NoError(t, err)

	spec, err := n.Worker().Resolve(t.Context(), specs.Address{
		Project: "shop", Environment: "prod", Kind: "function", Name: "built",
	})
	require.NoError(t, err)
	require.Contains(t, spec.Artifact, "registry/built@sha256:")
	require.Contains(t, spec.Env["TOKEN"], "sk_")

	require.Len(t, asked, 2)
}

type artifactFunc func(ctx context.Context, project, environment, kind, resourceID string) (string, error)

func (f artifactFunc) Artifact(ctx context.Context, project, environment, kind, resourceID string) (string, error) {
	return f(ctx, project, environment, kind, resourceID)
}

type secretFunc func(ctx context.Context, project, environment, secretID string) (string, error)

func (f secretFunc) Secret(ctx context.Context, project, environment, secretID string) (string, error) {
	return f(ctx, project, environment, secretID)
}

// TestAWorkerResolvesASecretFromItsOwnVault — the composed path, with nothing
// injected. A project whose function names a secret is the ordinary case for
// anything imported from a platform that had environment variables, so this is
// the wiring between "it compiles" and "it runs".
func TestAWorkerResolvesASecretFromItsOwnVault(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Discovery, Secrets}}, prebuilt())
	require.NoError(t, n.Start(t.Context()))

	require.NoError(t, n.Vault().Set(t.Context(), "shop", "prod", secretID, []byte("sk_live")))

	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: needsASecret(),
	})
	require.NoError(t, err)

	spec, err := n.Worker().Resolve(t.Context(), workerTarget())
	require.NoError(t, err)
	require.Equal(t, "sk_live", spec.Env["TOKEN"])
}

// TestAWorkerFindsAVaultOnAnotherNode. A node that runs compute and no vault
// learns from discovery which node holds one and dials it — the same rule as the
// register, over the real gossipsub and the real stream.
func TestAWorkerFindsAVaultOnAnotherNode(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Register, Secrets, Discovery}},
		Config{Services: []string{Worker, Discovery}},
	)
	holder, compute := nodes[0], nodes[1]

	require.NoError(t, holder.Vault().Set(t.Context(), "shop", "prod", secretID, []byte("sk_live")))

	_, err := holder.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: needsASecret(),
	})
	require.NoError(t, err)

	sees(t, compute, Register)
	sees(t, compute, Secrets)

	spec, err := compute.Worker().Resolve(t.Context(), workerTarget())
	require.NoError(t, err)
	require.Equal(t, "sk_live", spec.Env["TOKEN"])
}

// TestAWorkerWithNoVaultAnywhereRefusesRatherThanRunning. A workload started
// without its database password does not fail where the password is missing;
// it fails somewhere else, later, as an application that looks broken.
func TestAWorkerWithNoVaultAnywhereRefusesRatherThanRunning(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Discovery}}, prebuilt())
	require.NoError(t, n.Start(t.Context()))

	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: needsASecret(),
	})
	require.NoError(t, err)

	_, err = n.Worker().Resolve(t.Context(), workerTarget())
	require.ErrorIs(t, err, ErrNoSecrets)
}

// TestABuiltResourceWithNoBuildServiceIsRefusedRatherThanWaited. This runs on
// the request path: a lookup that waits until its context ends is a request
// that hangs until the client gives up, on a cloud where nothing was ever
// going to answer.
func TestABuiltResourceWithNoBuildServiceIsRefused(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Discovery, Secrets}})
	require.NoError(t, n.Start(t.Context()))

	require.NoError(t, n.Vault().Set(t.Context(), "shop", "prod", secretID, []byte("sk_live")))
	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: needsBothSeams(),
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
		require.ErrorIs(t, err, ErrNoBuildBackend)
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

// TestADATABASESERVICEISREACHABLEFROMANOTHERNODE.
//
// It was not, and nothing noticed: the service was built and never started, so
// its listener was never bound. Every caller so far was the node's own Local()
// seam, which is the absence of a hop — so the first peer to dial one got
// "protocols not supported" from a node that was, by every other measure,
// running the database service.
//
// A SWEEP THAT NEVER RAN IS THE OTHER HALF. Start is also what begins
// reconsidering what this node holds, so a composed fleet moved nothing, ever.
func TestADatabaseServiceIsReachableFromAnotherNode(t *testing.T) {
	nodes := fleet(t,
		Config{Services: []string{Register, Discovery, Database}},
		Config{Services: []string{Worker, Discovery}},
	)
	holder, caller := nodes[0], nodes[1]

	_, err := holder.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: withKvDatabase(),
	})
	require.NoError(t, err)

	client, err := databaseService.NewClient(caller.opts.host, holder.opts.host.ID())
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
