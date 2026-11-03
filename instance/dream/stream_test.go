//go:build dreaming

package dream_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	nodeDream "github.com/taubyte/tau/instance/dream"
	"github.com/taubyte/tau/pkg/invoke"
)

// Fan-out over real gossipsub: a guest publishes to a topic and a listener on
// another node gets the bytes, showing a guest's label resolves to the topic another
// node listens on.

// `subscribe` is a DeliveredOp and nothing in this tree dispatches one
// (services/worker/consume.go keeps only queues; instance/dispatch.go drains only
// node.syncs). Choosing between fan-out and exactly-once is an open product decision
// (pkg/invoke/wasm/queue.go). instance/stream.go's subscribe backs browser buses, not
// workloads. So this asserts the declaration reaches the worker as a grant, not
// arrival; when delivery exists, assert the key.

func TestAGuestsMessageCrossesTheSwarm(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	cloud := deployFleet(t, 2, "with/stream/distributed", "orders.example.com", cacherWASM(t))
	publishing, listening := cloud[0], cloud[1]

	// The listener is the other node, resolving the handle itself, which also proves the
	// compiled project crossed.
	var stream invoke.Grant
	require.Eventually(t, func() bool {
		spec, err := listening.node.Worker().Resolve(t.Context(), target())
		if err != nil {
			return false
		}
		for _, g := range spec.Reaches {
			if g.Kind == "stream" {
				stream = g
			}
		}
		return stream.Handle != ""
	}, 60*time.Second, 500*time.Millisecond, "the project never reached the second node")

	require.Equal(t, "orders", stream.Label)
	// Asked of the compiled project rather than restated (see idOf).
	require.Equal(t, "shop/prod/"+listening.idOf(t, "stream", "orders"), stream.Handle,
		"the resolved id, not the label")
	require.Empty(t, stream.Refused,
		"the stream named the swarm's own engine, which is the one this cloud drives")

	// Both verbs survived the compile; nothing invokes on `subscribe` yet.
	require.True(t, stream.Allows("publish"), "the verb the guest calls")
	require.True(t, stream.Allows("subscribe"),
		"the verb the platform would deliver on, declared and carried but never dispatched")

	topic, err := listening.node.PubSub().Join(listening.node.StreamTopic(stream.Handle))
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

	// A gossipsub mesh needs both sides joined before a publish reaches anybody, so the
	// publish is retried. No raft here; 60s is headroom for reporting a mesh that never
	// formed.
	require.Eventually(t, func() bool {
		res, err := publishing.onCarrying(t.Context(), target(),
			http.MethodPost, "/publish/orders/", "order:1")
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
