//go:build dreaming

package dream_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	nodeDream "github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/invoke"
)

// stream_test is the fan-out kind, end to end and over real gossipsub: a guest
// publishes to a topic and a listener on ANOTHER node gets the bytes.
//
// EVERY OTHER TEST OF THIS PATH FAKES ITS NEIGHBOUR. pkg/invoke/wasm/stream_test
// runs a real guest against a fake Streams and a spec somebody typed;
// node/stream_test runs the real topics with no guest anywhere near them. What
// neither can show is that the topic a guest's LABEL resolved to is the topic
// another node is listening on — and that is the whole of the kind.
//
// AND THE PROJECT IS AUTHORED RATHER THAN TRANSCRIBED, for project_test's
// reason. The object this test used to publish was written by hand, so it was a
// statement of what the compiler was believed to produce and nothing ever
// checked it — and it had drifted the same two ways every other conversion
// found. It omitted `execution/timeout`, which is Required(), so tcc would have
// REFUSED the very project this test claimed to be running; and it omitted
// `implements`, which the vocabulary fills in as `handler`, so the field the
// authorizer path turns on (services/worker/resolve.go:502) was absent from
// every invocation.
//
// THE ENGINE IT GOT RIGHT, and that is worth saying because it is the one field
// a reader would doubt: `engine: distributed` is exactly what an authored
// `distributed:` block compiles to, the key captured out of
// `Path(Either(streamEngines...))`. What a hand-written object cannot have is
// the REST of the kind — `local`, the one axis tau's own engine has; a build; a
// delivery — because nothing hand-written is checked against the vocabulary at
// all. projects/with/stream/distributed is compiled here instead, which is the
// only way a stream's declaration and a stream's behaviour are tested as one
// thing.
//
// AND IT WAS THE LAST KIND IN THIS PACKAGE CARRYING ONE. The hand-built objects
// still in authorizer_test, edge_test and instance_test are a different thing:
// each is a construct cutting across kinds — a guard, a route's edge behaviour,
// an `instances:` block — on a kind that does have a corpus project.

// ── the half this cloud does not serve ───────────────────────────────────────
//
// `stream` HAS TWO VERBS AND ONLY ONE OF THEM RUNS. `publish` is a WriteOp and
// a host function a guest calls; `subscribe` is a DeliveredOp — the platform
// invokes a workload when a message arrives — and NOTHING IN THIS TREE
// DISPATCHES ONE. That is stated here rather than left to be rediscovered,
// because a project declaring `subscribe` reads exactly like a project whose
// subscription works.
//
// Where it is said, and where it is not done:
//
//   - pkg/invoke/wasm/stream.go:19-25 — there is no host function for it and
//     there should not be one; a delivery is not something a guest calls.
//   - node/stream.go:26-32 — "Nothing here dispatches one yet."
//   - services/worker/consume.go:175 — the one enumeration of "what should this
//     node be draining" keeps only `grant.Kind == syncKind` at `type: queue`,
//     so a stream grant declaring `subscribe` never becomes a Consumer at all.
//   - node/dispatch.go:94-97 — and the loop that claims and invokes takes its
//     work from `node.syncs`, which is queues and nothing else.
//   - pkg/invoke/wasm/queue.go:22-29 — WHY, and it is a product decision rather
//     than a missing function: a pubsub message reaches every node running the
//     subscriber, so a dispatcher has to choose between fan-out and
//     exactly-once, and nobody has chosen.
//
// V1 ANSWERED IT AND IS NOT A PRECEDENT.
// services/substrate/components/pubsub/subscribe.go:16-39 looks a message up and
// invokes EVERY workload that matches, on every node holding one — the fan-out
// arm, taken by default rather than decided. It is named here so whoever builds
// the v2 half starts from a decision instead of from that code.
//
// WHAT node/stream.go:108-127 SERVES IS NOT THIS. Its `subscribe(handle)` backs
// `buses` — a BROWSER attaching over the gateway (services/gateway/bus.go:286
// reads the payload straight off it). That is a websocket on the other end of
// the topic, not a workload the platform started.
//
// So the project declares `subscribe` and a `deliver` path onto a cache key, and
// this test asserts everything about it EXCEPT arrival: that the declaration
// survives the compile and reaches the worker as a grant. The day something
// claims a message and invokes, one assertion goes back — read the key.

// TestAGuestsMessageCrossesTheSwarm — the stream capability across real
// gossipsub, which is the only thing that shows the topic a guest's label
// resolved to is the topic another node is listening on.
func TestAGuestsMessageCrossesTheSwarm(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	cloud := deployFleet(t, 2, "with/stream/distributed", "orders.example.com", cacherWASM(t))
	publishing, listening := cloud[0], cloud[1]

	// THE LISTENER IS THE OTHER NODE, and it resolves the handle rather than
	// being told one — so a change to how a compiled stream becomes a grant
	// breaks this rather than passing quietly. It resolving AT ALL is also what
	// proves the compiled project crossed: deployFleet's barrier waited for the
	// register's pointer, and this waits for the worker's read of it.
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
	// THE HANDLE IS ASKED OF THE COMPILED PROJECT rather than restated. A
	// hardcoded id here would be a second statement of what the compiler
	// produced, which is the one thing these projects exist to stop — see idOf.
	require.Equal(t, "shop/prod/"+listening.idOf(t, "stream", "orders"), stream.Handle,
		"the resolved id, not the label")
	require.Empty(t, stream.Refused,
		"the stream named the swarm's own engine, which is the one this cloud drives")

	// BOTH VERBS SURVIVED THE COMPILE, and that is the whole of what can be
	// asserted about the delivered one — see the gap above. `subscribe` reaching
	// the worker as a grant is the config half working; nothing comes and
	// invokes on it.
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

	// A gossipsub mesh needs both sides joined before a publish reaches
	// anybody, so the publish is retried until it does — which is what the
	// kind's own doc says about delivery.
	//
	// NEITHER NAMED BUDGET PRICES THIS ONE, for bus_test's reason: there is no
	// raft anywhere on this path, and what is being waited out is a gossipsub
	// heartbeat and the few of them a mesh takes once both ends have
	// subscribed. The 60s is the cost of REPORTING a mesh that never formed
	// rather than a bound on one that does, and it is the number this assertion
	// has always carried.
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
