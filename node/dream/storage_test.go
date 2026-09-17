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
	nodeDream "github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/invoke"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

// storage_test is an object written by a guest on one node and read by a guest
// on another — which is the whole claim of "content-addressed, served to the
// project's peers", and the one thing neither the ABI's own tests nor the
// node's can make on their own.
//
// The two halves have different plumbing and both have to work: the NAME is a
// row in a replicated kvdb and the BYTES are in the DAG, so this fails if
// either one does not cross.

const bucketID = "QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zo"

// withBucket is a project whose one function reaches one object store.
func withBucket(componentCID string) []byte {
	doc := map[string]any{
		"object": map[string]any{
			"id":   cacheProjectID,
			"name": "shop",
			"function": map[string]any{
				cacheFuncID: map[string]any{
					"name":      "cacher",
					"execution": map[string]any{"call": "handle", "memory": 33554432},
					"source":    map[string]any{"kind": "component", "component": componentCID},
					"storage": map[string]any{
						"receipts": map[string]any{"ops": []any{"get", "head", "put", "delete", "list"}},
					},
				},
			},
			"storage": map[string]any{
				bucketID: map[string]any{
					"name": "receipts",
					// The type block the vocabulary declares, and the FLAT
					// `size` it compiles to — see storageSettings for why the
					// two are spelled differently.
					"type": "object",
					"size": float64(1_000_000),
					"object": map[string]any{
						"versioning": true,
					},
				},
			},
		},
		"indexes": map[string]any{
			"projects/" + cacheProjectID + "/function/cacher/links": []any{
				"branches/main/projects/" + cacheProjectID + "/function/" + cacheFuncID},
			"projects/" + cacheProjectID + "/storage/receipts/links": []any{
				"branches/main/projects/" + cacheProjectID + "/storage/" + bucketID},
		},
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return raw
}

// TestAnObjectWrittenOnOneNodeIsReadableOnAnother.
func TestAnObjectCrossesTheSwarm(t *testing.T) {
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
		Project: "shop", Environment: "prod", Revision: 1, Compiled: withBucket(cid),
	})
	require.NoError(t, err)

	// WRITTEN THROUGH THE FIRST NODE'S GUEST. A put answers the ETAG it stored
	// under, which is S3's contract and is this cloud's content id.
	res, err := first.Worker().Invoke(t.Context(), target(), &invoke.Request{
		Method: "POST", Path: "/obj-put/receipts/2026/01", Body: []byte("a receipt"),
	})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status, string(res.Body))
	etag := string(res.Body)
	require.NotEmpty(t, etag)

	// AND READ THROUGH THE SECOND'S, which means the compiled project reached
	// it, its own tier started the component, its own kvdb had the row and its
	// own DAG had the bytes.
	require.Eventually(t, func() bool {
		res, err := second.Worker().Invoke(t.Context(), target(), &invoke.Request{
			Method: "POST", Path: "/obj-get/receipts/2026/01",
		})
		return err == nil && res.Status == 200 && string(res.Body) == "a receipt"
	}, 60*time.Second, 500*time.Millisecond, "an object never crossed the swarm")

	// AND `head` ANSWERS THERE TOO, off the row rather than the bytes — the
	// record survives the ABI in a universe as well as in a unit test.
	//
	// THE ETAG IS THE ONE THE OTHER NODE ANSWERED WITH, which is the claim the
	// whole content-addressed story rests on: the second node computed nothing
	// and looked nothing up by name — it read the row that replicated, and the
	// row's content id IS what the first node wrote.
	var head string
	require.Eventually(t, func() bool {
		res, err := second.Worker().Invoke(t.Context(), target(), &invoke.Request{
			Method: "POST", Path: "/obj-head/receipts/2026/01",
		})
		if err != nil || res.Status != 200 {
			return false
		}
		head = string(res.Body)
		return bytes.HasPrefix(res.Body, []byte("2026/01:9:"))
	}, 60*time.Second, 500*time.Millisecond, "head never answered on the second node")

	require.Contains(t, head, ":"+etag+":", "the etag did not cross the swarm: %s", head)
}
