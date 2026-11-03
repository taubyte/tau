//go:build dreaming

package dream_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
	nodeDream "github.com/taubyte/tau/instance/dream"
	"github.com/taubyte/tau/pkg/invoke"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

// The `instances:` block end to end: a guest naming one of many, a real validator
// function deciding, and two instances isolated from each other.

const (
	manyCacheID = "QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zi"
	mayOpenFunc = "QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zj"

	// What each of them runs.
	cacherPackageID  = "QmSQWYU5v1UGYk8ABXTyewQFnG7VtCHQtVQ9D2LgBwrG5x"
	mayOpenPackageID = "Qmcg5kkaVm6tkPFBNzRriUtr8XpWnRz5KkxesPSVtEgurk"
)

// withInstances is a project whose function reaches a many-instance cache, plus the
// validator (`implements: instance-validator`, exporting `admit`). Their artifacts
// reach the worker through the staged seam (stagedOn).
func withInstances() []byte {
	doc := map[string]any{
		"object": map[string]any{
			"id":   cacheProjectID,
			"name": "shop",
			"package": map[string]any{
				cacherPackageID:  map[string]any{"name": "cacher_code", "type": "wasm", "source": "services/cacher"},
				mayOpenPackageID: map[string]any{"name": "admitter_code", "type": "wasm", "source": "services/admitter"},
			},
			"function": map[string]any{
				cacheFuncID: map[string]any{
					"name":      "cacher",
					"package":   map[string]any{"name": cacherPackageID},
					"execution": map[string]any{"call": "handle", "memory": 33554432},
					"cache": map[string]any{
						"rooms": map[string]any{"ops": []any{"get", "put"}},
					},
				},
				mayOpenFunc: map[string]any{
					"name":       "may_open",
					"implements": "instance-validator",
					"package":    map[string]any{"name": mayOpenPackageID},
					"execution":  map[string]any{"call": "admit", "memory": 8388608},
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
						"validator": map[string]any{"name": "may_open"},
					},
				},
			},
		},
		"indexes": map[string]any{
			"projects/" + cacheProjectID + "/package/cacher_code/links": []any{
				"branches/main/projects/" + cacheProjectID + "/package/" + cacherPackageID},
			"projects/" + cacheProjectID + "/package/admitter_code/links": []any{
				"branches/main/projects/" + cacheProjectID + "/package/" + mayOpenPackageID},
			"projects/" + cacheProjectID + "/function/cacher/links": []any{
				"branches/main/projects/" + cacheProjectID + "/function/" + cacheFuncID},
			"projects/" + cacheProjectID + "/function/may_open/links": []any{
				"branches/main/projects/" + cacheProjectID + "/function/" + mayOpenFunc},
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

func TestAnInstanceIsAdmittedByARealFunction(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Services: []string{instance.Register, instance.Worker, instance.Discovery},
	})
	require.NoError(t, err)
	defer u.Close()

	pids := u.Pids()
	require.NotEmpty(t, pids)

	n := nodeOf(t, pids[0])
	store, ok := nodeDream.Assets(pids[0])
	require.True(t, ok)

	cid, err := store.Put(t.Context(), bytes.NewReader(cacherWASM(t)))
	require.NoError(t, err)
	admits, err := store.Put(t.Context(), bytes.NewReader(admitterWASM(t)))
	require.NoError(t, err)

	stagedOn(t, pids, "shop", map[string]string{cacherPackageID: cid, mayOpenPackageID: admits})

	_, err = n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: withInstances(),
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

	// THE VALIDATOR'S NO IS THE ANSWER, and it now carries the validator's own
	// words rather than an HTTP status the node had to interpret. The fixture
	// refuses an instance called `denied`; the guest is told it was not admitted
	// rather than that the cache is down.
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

func TestTheInstanceLimitHoldsEndToEnd(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Services: []string{instance.Register, instance.Worker, instance.Discovery},
	})
	require.NoError(t, err)
	defer u.Close()

	pids := u.Pids()
	n := nodeOf(t, pids[0])

	store, ok := nodeDream.Assets(pids[0])
	require.True(t, ok)
	cid, err := store.Put(t.Context(), bytes.NewReader(cacherWASM(t)))
	require.NoError(t, err)
	admits, err := store.Put(t.Context(), bytes.NewReader(admitterWASM(t)))
	require.NoError(t, err)

	stagedOn(t, pids, "shop", map[string]string{cacherPackageID: cid, mayOpenPackageID: admits})

	_, err = n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: withInstances(),
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
