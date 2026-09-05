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

// queue_test is background work, end to end and with nothing faked: a guest
// pushes an item, the platform claims it off a raft-replicated queue with no
// prompting, and the same guest is invoked again with the payload.
//
// EVERY OTHER TEST OF THIS PATH FAKES ITS NEIGHBOUR — the dispatcher's fakes a
// tier, the tier's fakes a queue, the queue's fakes raft. What none of them can
// show is that a workload declaring `receive` in a compiled project is a
// workload something actually comes and invokes.

const (
	jobsID     = "QmezXKNabfuAJze6dJ8d45PW14eJBM9JAomTC8uT3Jqn48"
	jobsDeadID = "QmQ5XpS9mZCWE5Dc4TkBaGLSjk2rA9qj7BtGauhkcQj2tz"
)

// withQueue is a project whose one function both sends to a queue and receives
// from it, and writes what it receives into a cache.
//
// THE DELIVERY PATH IS WHAT MAKES THE ARRIVAL VISIBLE. The fixture component
// routes on its path, so `/put/sessions/done` means "write the body under
// `done`" — and a test that can read that key back afterwards has proof the
// item reached the guest with its payload intact.
func withQueue(componentCID string) []byte {
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
						"sessions": map[string]any{"ops": []any{"get", "put"}},
					},
					"queue": map[string]any{
						"jobs": map[string]any{
							"ops":     []any{"send", "receive"},
							"deliver": map[string]any{"path": "/put/sessions/done"},
						},
					},
				},
			},
			"cache": map[string]any{
				cacheID: map[string]any{
					"name": "sessions", "consensus": "crdt",
					"size": 1000000, "ttl": float64(time.Hour),
				},
			},
			"queue": map[string]any{
				jobsID: map[string]any{
					"name":               "jobs",
					"visibility_timeout": float64(30 * time.Second),
					"max_attempts":       float64(3),
					"dead_letter":        map[string]any{"name": jobsDeadID},
				},
				jobsDeadID: map[string]any{"name": "jobs_dead"},
			},
		},
		"indexes": map[string]any{
			"projects/" + cacheProjectID + "/function/cacher/links": []any{
				"branches/main/projects/" + cacheProjectID + "/function/" + cacheFuncID},
			"projects/" + cacheProjectID + "/cache/sessions/links": []any{
				"branches/main/projects/" + cacheProjectID + "/cache/" + cacheID},
			"projects/" + cacheProjectID + "/queue/jobs/links": []any{
				"branches/main/projects/" + cacheProjectID + "/queue/" + jobsID},
			"projects/" + cacheProjectID + "/queue/jobs_dead/links": []any{
				"branches/main/projects/" + cacheProjectID + "/queue/" + jobsDeadID},
		},
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return raw
}

// lastOf renders whatever the last attempt said, at the moment the assertion
// gives up rather than at the moment it was written.
type lastOf struct{ s *string }

func (l *lastOf) String() string { return *l.s }

// TestAGuestPushesAndThePlatformComesBackWithIt — the whole claim of the
// delivered half of a queue.
func TestAnItemSentByAGuestIsDeliveredToOne(t *testing.T) {
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
		Project: "shop", Environment: "prod", Revision: 1, Compiled: withQueue(cid),
	})
	require.NoError(t, err)

	// THE GUEST IS THE PRODUCER, which is the ordinary shape: a request comes
	// in, the work is queued, and the request answers immediately.
	//
	// RETRIED, because a cloud's consensus for queues forms in the background
	// and a send that arrives first is told `unavailable` rather than made to
	// wait out raft's bootstrap. That refusal IS the contract — the arm exists
	// so a caller can retry — so a test that only ever sent once would be
	// testing a cloud that had already been running.
	var last string
	require.Eventually(t, func() bool {
		res, err := n.Worker().Invoke(t.Context(), target(), &invoke.Request{
			Method: "POST", Path: "/send/jobs/order-1", Body: []byte("ship it"),
		})
		if err != nil {
			last = err.Error()
			return false
		}
		last = string(res.Body)
		return res.Status == 204
	}, 90*time.Second, time.Second, "the guest could never queue anything: %s", &lastOf{&last})

	// AND NOBODY ASKS FOR IT BACK. The node's own claim loop finds the item,
	// invokes the workload that declared `receive`, and the delivery lands on
	// the path the entry declared — which for this component means the payload
	// is written into the cache under `done`.
	require.Eventually(t, func() bool {
		res, err := n.Worker().Invoke(t.Context(), target(), &invoke.Request{
			Method: "POST", Path: "/get/sessions/done",
		})
		return err == nil && res.Status == 200 && string(res.Body) == "ship it"
	}, 60*time.Second, 500*time.Millisecond, "nothing ever delivered the item")

	// AND IT IS ACKNOWLEDGED, so the queue is empty rather than holding an item
	// that will be handed round again.
	require.Eventually(t, func() bool {
		ready, claimed, ok := n.QueueDepth("shop/prod/" + jobsID)
		return ok && ready == 0 && claimed == 0
	}, 60*time.Second, 500*time.Millisecond, "the item was delivered and never settled")
}
