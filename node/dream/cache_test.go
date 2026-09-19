//go:build dreaming

package dream_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/node"
	nodeDream "github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/invoke"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
)

// A REAL COMPONENT, REACHING REAL STATE, ACROSS REAL NODES.
//
// Every link in this chain is proven somewhere else with its neighbours faked:
// the ABI against a fake store (pkg/invoke/wasm), the grant against a fake tier
// (services/worker), the backing against a fake grant (node). What none of them
// can show is that the four agree — that the label an author wrote reaches the
// rows a kvdb replicated, through a component nobody in this repository wrote
// the bindings for.
//
// So this is the only test in the tree where a wasm guest calls a host function
// that writes to a replicated store. Nothing is faked: the universe's nodes
// build their own wasm tier, their own cache backing and their own kvdb.

// cacherWASM is the same fixture pkg/invoke/wasm runs, addressed here by the
// cid the asset store gives it.
func cacherWASM(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile(filepath.Join("..", "..", "pkg", "invoke", "wasm", "fixtures", "cacher.wasm"))
	require.NoError(t, err, "run `cargo component build --release` in pkg/invoke/wasm/fixtures/cacher")
	return code
}

const (
	cacheProjectID = "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR"
	cacheFuncID    = "QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY"
	cacheID        = "QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB"
)

// withCache is a project whose one function reaches one cache, compiled by
// hand for the reason fixture_test.go gives: what a compiler produces is
// proven in services/worker against the vocabulary's own fixture, and what is
// proven here is the wiring.
//
// The component is addressed by the cid the store handed back, which is what a
// `source: {kind: component}` carries.
func withCache(componentCID string) []byte {
	doc := map[string]any{
		"object": map[string]any{
			"id":   cacheProjectID,
			"name": "shop",
			"function": map[string]any{
				cacheFuncID: map[string]any{
					"name":      "cacher",
					"execution": map[string]any{"call": "handle", "memory": 33554432},
					"source":    map[string]any{"kind": "component", "component": componentCID},
					// The access block, as the compiler writes it: keyed by the
					// label, with the verbs beside it.
					"cache": map[string]any{
						"sessions": map[string]any{"ops": []any{"get", "put", "delete", "list"}},
					},
				},
			},
			"cache": map[string]any{
				cacheID: map[string]any{
					"name": "sessions",
					// crdt, so this cloud serves it: raft is refused
					// by name and gossip is served stronger than asked.
					"consensus": "crdt",
					"size":      1000000,
					"ttl":       float64(time.Hour),
				},
			},
		},
		"indexes": map[string]any{
			"projects/" + cacheProjectID + "/function/cacher/links": []any{
				"branches/main/projects/" + cacheProjectID + "/function/" + cacheFuncID},
			"projects/" + cacheProjectID + "/cache/sessions/links": []any{
				"branches/main/projects/" + cacheProjectID + "/cache/" + cacheID},
		},
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return raw
}

func target() specs.Address {
	return specs.Address{Project: "shop", Environment: "prod", Kind: "function", Name: "cacher"}
}

// TestAGuestWritesAndReadsAReplicatedCache — the whole chain, in one universe.
func TestARealComponentReachesARealCache(t *testing.T) {
	// NO INJECTED TIER. The nodes build their own, which is the point: an
	// injected one would be the test's wasm runtime with the test's cache
	// backing, and what is being proven is the node's.
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	m, err := dream.New(t.Context())
	require.NoError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	require.NoError(t, err)
	require.NoError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			nodeDream.Protocol: {Others: nodeDream.Services(node.Register, node.Worker, node.Stats)},
		},
	}))

	pids, err := u.GetServicePids(nodeDream.Protocol)
	require.NoError(t, err)
	require.NotEmpty(t, pids)

	n := nodeOf(t, pids[0])
	store, ok := nodeDream.Assets(pids[0])
	require.True(t, ok, "the node has an asset store")

	// THE COMPONENT GOES IN BY CID, which is what a resource naming its own
	// component carries — no build service in this universe.
	cid, err := store.Put(t.Context(), bytes.NewReader(cacherWASM(t)))
	require.NoError(t, err)

	_, err = n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: withCache(cid),
	})
	require.NoError(t, err)

	// The grant survived the compile, the publish and the resolve.
	spec, err := n.Worker().Resolve(t.Context(), target())
	require.NoError(t, err)
	require.Equal(t, "wasm", spec.Runtime)
	require.Len(t, spec.Reaches, 1)
	require.Equal(t, "sessions", spec.Reaches[0].Label)
	require.Equal(t, "shop/prod/"+cacheID, spec.Reaches[0].Handle, "the resolved id, not the label")
	require.Empty(t, spec.Reaches[0].Refused)

	// A ROUND TRIP THROUGH THE GUEST: it writes the body into the cache and
	// reads it back, so what comes out came through the host function.
	res, err := n.Worker().Invoke(t.Context(), target(), &invoke.Request{
		Method: "POST", Path: "/roundtrip/sessions/user:1", Body: []byte("alice"),
	})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Equal(t, "alice", string(res.Body))

	// AND THE ROW IS REALLY THERE, read back through a second invocation so
	// the value survived the instance rather than living in it.
	res, err = n.Worker().Invoke(t.Context(), target(), &invoke.Request{
		Method: "POST", Path: "/get/sessions/user:1",
	})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Equal(t, "alice", string(res.Body))

	// A key nobody wrote is a miss and not an outage.
	res, err = n.Worker().Invoke(t.Context(), target(), &invoke.Request{
		Method: "POST", Path: "/get/sessions/user:2",
	})
	require.NoError(t, err)
	require.Equal(t, 404, res.Status)
	require.Equal(t, "not-found", string(res.Body))

	// And a cache the config never granted is refused BY NAME, with the
	// instance still alive afterwards.
	res, err = n.Worker().Invoke(t.Context(), target(), &invoke.Request{
		Method: "POST", Path: "/get/somebody-elses/k",
	})
	require.NoError(t, err)
	require.Equal(t, "no-such-cache:somebody-elses", string(res.Body))
}

// TestAValueWrittenOnOneNodeIsReadableOnAnother. A cache is a replicated store
// and nothing has ever proven a workload's own data crosses — every other test
// of replication is about the register.
func TestACacheReplicatesBetweenNodes(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	m, err := dream.New(t.Context())
	require.NoError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	require.NoError(t, err)
	require.NoError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			nodeDream.Protocol: {Others: map[string]int{"copies": 2}},
		},
	}))

	pids, err := u.GetServicePids(nodeDream.Protocol)
	require.NoError(t, err)
	require.Len(t, pids, 2)

	first, second := nodeOf(t, pids[0]), nodeOf(t, pids[1])

	// The component has to be readable from whichever node runs the workload,
	// so it goes into both stores. In a cloud the build service publishes it
	// once and the byte plane moves it; that plane is what services/storage
	// still owes (see pkg/assets' own doc).
	for _, pid := range pids {
		store, ok := nodeDream.Assets(pid)
		require.True(t, ok)
		_, err := store.Put(t.Context(), bytes.NewReader(cacherWASM(t)))
		require.NoError(t, err)
	}

	store, _ := nodeDream.Assets(pids[0])
	cid, err := store.Put(t.Context(), bytes.NewReader(cacherWASM(t)))
	require.NoError(t, err)

	_, err = first.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: withCache(cid),
	})
	require.NoError(t, err)

	// Written on the first node, through its guest.
	res, err := first.Worker().Invoke(t.Context(), target(), &invoke.Request{
		Method: "POST", Path: "/put/sessions/shared", Body: []byte("crosses"),
	})
	require.NoError(t, err)
	require.Equal(t, 204, res.Status, string(res.Body))

	// Read on the second, through ITS guest — which means the compiled project
	// reached it, its own tier started the component, and its own kvdb had the
	// row. Eventually, because a CRDT is eventually consistent and that is
	// exactly what this cache declared.
	require.Eventually(t, func() bool {
		res, err := second.Worker().Invoke(t.Context(), target(), &invoke.Request{
			Method: "POST", Path: "/get/sessions/shared",
		})
		return err == nil && res.Status == 200 && string(res.Body) == "crosses"
	}, 60*time.Second, 500*time.Millisecond, "a workload's own data never crossed the swarm")
}

// ── streams ──────────────────────────────────────────────────────────────────

const streamID = "QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zs"

// withStream is a project whose one function publishes to one stream.
func withStream(componentCID string) []byte {
	doc := map[string]any{
		"object": map[string]any{
			"id":   cacheProjectID,
			"name": "shop",
			"function": map[string]any{
				cacheFuncID: map[string]any{
					"name":      "cacher",
					"execution": map[string]any{"call": "handle", "memory": 33554432},
					"source":    map[string]any{"kind": "component", "component": componentCID},
					"stream": map[string]any{
						"orders": map[string]any{"ops": []any{"publish"}},
					},
				},
			},
			"stream": map[string]any{
				// The swarm's own engine, which is the one this cloud holds;
				// kafka and nats are refused by name.
				streamID: map[string]any{"name": "orders", "engine": "distributed"},
			},
		},
		"indexes": map[string]any{
			"projects/" + cacheProjectID + "/function/cacher/links": []any{
				"branches/main/projects/" + cacheProjectID + "/function/" + cacheFuncID},
			"projects/" + cacheProjectID + "/stream/orders/links": []any{
				"branches/main/projects/" + cacheProjectID + "/stream/" + streamID},
		},
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return raw
}

// TestAGuestPublishesAndAnotherNodeReceivesIt — the stream capability across
// real gossipsub, which is the only thing that shows the topic a guest's label
// resolved to is the topic another node is listening on.
func TestAGuestsMessageCrossesTheSwarm(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	m, err := dream.New(t.Context())
	require.NoError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	require.NoError(t, err)
	require.NoError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			nodeDream.Protocol: {Others: map[string]int{"copies": 2}},
		},
	}))

	pids, err := u.GetServicePids(nodeDream.Protocol)
	require.NoError(t, err)
	require.Len(t, pids, 2)

	first, second := nodeOf(t, pids[0]), nodeOf(t, pids[1])

	for _, pid := range pids {
		store, ok := nodeDream.Assets(pid)
		require.True(t, ok)
		_, err := store.Put(t.Context(), bytes.NewReader(cacherWASM(t)))
		require.NoError(t, err)
	}
	store, _ := nodeDream.Assets(pids[0])
	cid, err := store.Put(t.Context(), bytes.NewReader(cacherWASM(t)))
	require.NoError(t, err)

	_, err = first.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: withStream(cid),
	})
	require.NoError(t, err)

	// THE LISTENER IS THE OTHER NODE, and it subscribes to the topic the
	// composer derives rather than to one this test spelled — so a change to
	// how a handle becomes a topic breaks this rather than passing quietly.
	// It resolves on the SECOND node once the register's CRDT reaches it, which
	// is also what proves the compiled project crossed.
	var handle string
	require.Eventually(t, func() bool {
		spec, err := second.Worker().Resolve(t.Context(), target())
		if err != nil {
			return false
		}
		require.Len(t, spec.Reaches, 1)
		require.Empty(t, spec.Reaches[0].Refused)
		handle = spec.Reaches[0].Handle
		return true
	}, 60*time.Second, 500*time.Millisecond, "the project never reached the second node")

	topic, err := second.PubSub().Join(second.StreamTopic(handle))
	require.NoError(t, err)
	defer topic.Close()

	sub, err := topic.Subscribe()
	require.NoError(t, err)
	defer sub.Cancel()

	received := make(chan []byte, 1)
	go func() {
		msg, err := sub.Next(t.Context())
		if err == nil {
			received <- msg.Data
		}
	}()

	// A gossipsub mesh needs both sides joined before a publish reaches
	// anybody, so the publish is retried until it does — which is what the
	// kind's own doc says about delivery.
	require.Eventually(t, func() bool {
		res, err := first.Worker().Invoke(t.Context(), target(), &invoke.Request{
			Method: "POST", Path: "/publish/orders/", Body: []byte("order:1"),
		})
		require.NoError(t, err)
		require.Equal(t, 204, res.Status, string(res.Body))

		select {
		case got := <-received:
			require.Equal(t, "order:1", string(got))
			return true
		default:
			return false
		}
	}, 60*time.Second, 500*time.Millisecond, "a guest's message never crossed the swarm")
}
