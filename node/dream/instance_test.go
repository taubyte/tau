//go:build dreaming

package dream_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/node"
	nodeDream "github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/invoke"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

// instance_test is the vocabulary's `instances:` block end to end: a guest
// naming one of many, a REAL validator function deciding, and two instances
// that cannot see each other.
//
// WHAT ONLY A UNIVERSE CAN SHOW is that the validator is a workload. Every
// other test of this path fakes the thing that decides — and what the config
// declares is a function, invoked through the worker, whose answer is the
// admission. Nothing short of a real component running it proves the two halves
// meet.

const manyCacheID = "QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zi"

// withInstances is a project whose one function reaches a cache that holds
// many, and is also the validator that admits them.
//
// ONE COMPONENT IN BOTH ROLES, because the fixture answers a request carrying
// an instance header as a decision and anything else as a cache operation. That
// is the shape a validator has today: `implements: instance-validator` names a
// host world nothing builds, so a validator IS a handler.
func withInstances(componentCID string) []byte {
	doc := map[string]any{
		"object": map[string]any{
			"id":   cacheProjectID,
			"name": "shop",
			"function": map[string]any{
				cacheFuncID: map[string]any{
					"name":      "cacher",
					"execution": map[string]any{"call": "handle", "memory": 33554432},
					"source":    map[string]any{"kind": "component", "component": componentCID},
					"cache": map[string]any{
						"rooms": map[string]any{"ops": []any{"get", "put"}},
					},
				},
			},
			"cache": map[string]any{
				manyCacheID: map[string]any{
					"name": "rooms", "consensus": "crdt",
					"size": 1000000, "ttl": float64(time.Hour),
					// MANY INSTANCES, ADMITTED BY A FUNCTION AND BOUNDED.
					"instances": map[string]any{
						"admission": "validator",
						"limit":     float64(3),
						"validator": map[string]any{"name": "cacher"},
					},
				},
			},
		},
		"indexes": map[string]any{
			"projects/" + cacheProjectID + "/function/cacher/links": []any{
				"branches/main/projects/" + cacheProjectID + "/function/" + cacheFuncID},
			"projects/" + cacheProjectID + "/cache/rooms/links": []any{
				"branches/main/projects/" + cacheProjectID + "/cache/" + manyCacheID},
		},
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return raw
}

// TestAGuestOpensOneOfManyAndAFunctionDecides.
func TestAnInstanceIsAdmittedByARealFunction(t *testing.T) {
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
	require.True(t, ok)

	cid, err := store.Put(t.Context(), bytes.NewReader(cacherWASM(t)))
	require.NoError(t, err)

	_, err = n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: withInstances(cid),
	})
	require.NoError(t, err)

	// ONE INSTANCE, ADMITTED AND WRITTEN TO.
	res, err := n.Worker().Invoke(t.Context(), target(), &invoke.Request{
		Method: "POST", Path: "/put/rooms~general/greeting", Body: []byte("hello"),
	})
	require.NoError(t, err)
	require.Equal(t, 204, res.Status, string(res.Body))

	res, err = n.Worker().Invoke(t.Context(), target(), &invoke.Request{
		Method: "POST", Path: "/get/rooms~general/greeting",
	})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Equal(t, "hello", string(res.Body))

	// AND A SECOND INSTANCE IS A SECOND STORE. The handle carries the instance,
	// so the same key in another room is a miss rather than somebody else's
	// value.
	res, err = n.Worker().Invoke(t.Context(), target(), &invoke.Request{
		Method: "POST", Path: "/get/rooms~lobby/greeting",
	})
	require.NoError(t, err)
	require.Equal(t, 404, res.Status)
	require.Equal(t, "not-found", string(res.Body))

	// THE VALIDATOR'S NO IS THE ANSWER. The fixture refuses an instance called
	// `denied`, which is a real component returning a real status through the
	// worker — and the guest is told it was not admitted rather than that the
	// cache is down.
	res, err = n.Worker().Invoke(t.Context(), target(), &invoke.Request{
		Method: "POST", Path: "/get/rooms~denied/k",
	})
	require.NoError(t, err)
	require.Equal(t, 404, res.Status)
	require.Contains(t, string(res.Body), "denied:")
	require.Contains(t, string(res.Body), "not admitted")

	// AND ADDRESSING THE STORE ITSELF IS REFUSED, because serving it would be
	// one store where the config says many, admitted by nobody.
	res, err = n.Worker().Invoke(t.Context(), target(), &invoke.Request{
		Method: "POST", Path: "/get/rooms/k",
	})
	require.NoError(t, err)
	require.Contains(t, string(res.Body), "holds many instances")
}

// TestTHECEILINGHoldsAgainstARealValidator. `limit: 3` and a fourth room.
func TestTheInstanceLimitHoldsEndToEnd(t *testing.T) {
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
	n := nodeOf(t, pids[0])

	store, ok := nodeDream.Assets(pids[0])
	require.True(t, ok)
	cid, err := store.Put(t.Context(), bytes.NewReader(cacherWASM(t)))
	require.NoError(t, err)

	_, err = n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: withInstances(cid),
	})
	require.NoError(t, err)

	for _, room := range []string{"one", "two", "three"} {
		res, err := n.Worker().Invoke(t.Context(), target(), &invoke.Request{
			Method: "POST", Path: "/put/rooms~" + room + "/k", Body: []byte("x"),
		})
		require.NoError(t, err)
		require.Equal(t, 204, res.Status, "%s: %s", room, res.Body)
	}

	res, err := n.Worker().Invoke(t.Context(), target(), &invoke.Request{
		Method: "POST", Path: "/put/rooms~four/k", Body: []byte("x"),
	})
	require.NoError(t, err)
	require.Equal(t, 403, res.Status)
	require.Contains(t, string(res.Body), "limit is 3")

	// AND A ROOM ALREADY ADMITTED STILL SERVES. A full store is not a broken
	// one.
	res, err = n.Worker().Invoke(t.Context(), target(), &invoke.Request{
		Method: "POST", Path: "/get/rooms~one/k",
	})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status, string(res.Body))
}
