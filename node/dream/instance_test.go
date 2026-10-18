//go:build dreaming

package dream_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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

const (
	manyCacheID = "QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zi"
	mayOpenFunc = "QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zj"
)

// withInstances is a project whose one function reaches a cache that holds
// many, and is also the validator that admits them.
//
// TWO COMPONENTS, TWO WORLDS. The guest is a handler and the validator declares
// `implements: instance-validator` and exports `admit(opening) -> verdict`.
//
// WHICH COMPONENT EACH ONE RUNS IS NOT IN HERE. Both name a path in a repository
// the way an author writes it, and the artifacts they were staged with reach the
// worker through the seam a cloud's build service fills — see stagedOn.
//
// It used to be ONE component in both roles, because that world was named by the
// vocabulary and built by nothing — a validator had to BE a handler, told which
// instance by a header and answering with an HTTP status. This test said so in
// this comment. The world exists now, so the fixture's own shape is the one the
// config describes.
func withInstances() []byte {
	doc := map[string]any{
		"object": map[string]any{
			"id":   cacheProjectID,
			"name": "shop",
			"function": map[string]any{
				cacheFuncID: map[string]any{
					"name":      "cacher",
					"execution": map[string]any{"call": "handle", "memory": 33554432},
					"source":    "services/cacher",
					"cache": map[string]any{
						"rooms": map[string]any{"ops": []any{"get", "put"}},
					},
				},
				mayOpenFunc: map[string]any{
					"name":       "may_open",
					"implements": "instance-validator",
					"execution":  map[string]any{"call": "admit", "memory": 8388608},
					"source":     "services/admitter",
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

// TestAGuestOpensOneOfManyAndAFunctionDecides.
func TestAnInstanceIsAdmittedByARealFunction(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Services: []string{node.Register, node.Worker, node.Stats},
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

	stagedOn(t, pids, "shop", map[string]string{cacheFuncID: cid, mayOpenFunc: admits})

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

// TestTHECEILINGHoldsAgainstARealValidator. `limit: 3` and a fourth room.
func TestTheInstanceLimitHoldsEndToEnd(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Services: []string{node.Register, node.Worker, node.Stats},
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

	stagedOn(t, pids, "shop", map[string]string{cacheFuncID: cid, mayOpenFunc: admits})

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
