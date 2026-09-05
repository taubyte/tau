package node

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/queue"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/worker"
)

// step_test drives the one program a workflow was missing: receive, invoke,
// hand on. Everything around it — the retry budget, the backoff, where an
// exhausted item goes — is the queue's, which the lowering already wired.

// stepConsumer is a driver as the worker resolves one.
func stepConsumer(sends bool) worker.Consumer {
	c := worker.Consumer{
		Target:  worker.Target{Project: "shop", Environment: "prod", Scope: []string{"checkout"}, Kind: "function", Name: "checkout_reserve"},
		Queue:   invoke.Grant{Kind: "queue", Label: "checkout_reserve", Handle: "shop/prod/QmReserve", Ops: []string{"receive"}},
		Builtin: stepBuiltin,
		Invokes: []worker.Target{{Project: "shop", Environment: "prod", Kind: "function", Name: "reserve_stock"}},
	}
	if sends {
		c.Sends = []invoke.Grant{{Kind: "queue", Label: "checkout_charge", Handle: "shop/prod/QmCharge", Ops: []string{"send"}}}
	}
	return c
}

func delivery(id string, body []byte) queue.Delivery {
	return queue.Delivery{ID: id, Data: body, Attempt: 1, Deadline: time.Now().Add(time.Minute)}
}

// withStepFunction is a project holding the author's half of a step: the
// function the driver was granted `invoke` on.
func withStepFunction() []byte {
	return []byte(`{
		"object": {
			"id": "` + projectID + `",
			"name": "shop",
			"function": {
				"QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY": {
					"name": "reserve_stock",
					"execution": {"call": "handle", "memory": 33554432},
					"source": {"kind": "component", "component": "QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVv"}
				}
			}
		},
		"indexes": {
			"projects/` + projectID + `/function/reserve_stock/links": [
				"branches/main/projects/` + projectID + `/function/QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY"]
		}
	}`)
}

// stepping is a node whose worker answers, and the dispatcher over it.
func stepping(t *testing.T, answers *answering) (*Node, *dispatcher) {
	t.Helper()

	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}},
		WithRuntimes(answers))
	require.NoError(t, n.Start(t.Context()))

	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: withStepFunction(),
	})
	require.NoError(t, err)
	<-n.work.forming

	return n, &dispatcher{node: n}
}

// TestASTEPInvokesItsFunctionAndHandsOnWhatCameBack, which is the whole
// contract.
func TestAStepInvokesThenSends(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, d := stepping(t, answers)

	require.NoError(t, d.runStep(t.Context(), stepConsumer(true), delivery("order-1", []byte("an order"))))

	seen := answers.seen()
	require.Len(t, seen, 1)
	require.Equal(t, "an order", string(seen[0].Body), "the item is what the function is given")
	require.Equal(t, []string{"checkout_reserve"}, seen[0].Headers[headerStep])
	require.Equal(t, []string{"1"}, seen[0].Headers[headerAttempts])

	// AND WHAT CAME BACK IS IN THE NEXT STEP'S QUEUE.
	ready, _, ok := n.QueueDepth("shop/prod/QmCharge")
	require.True(t, ok)
	require.Equal(t, 1, ready)
}

// TestTHERUNSIdTravelsWithIt, so a step whose send landed and whose ack did not
// hands the next step the same work rather than a second copy.
func TestTheRunKeepsItsIdAcrossSteps(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, d := stepping(t, answers)

	consumer := stepConsumer(true)
	require.NoError(t, d.runStep(t.Context(), consumer, delivery("order-1", []byte("x"))))
	require.NoError(t, d.runStep(t.Context(), consumer, delivery("order-1", []byte("x"))))

	ready, _, _ := n.QueueDepth("shop/prod/QmCharge")
	require.Equal(t, 1, ready, "a redelivered step queued the same work twice")
}

// TestTHELASTStepHandsOnNothing, which is exactly where a run ends — the
// lowering leaves it no queue to send to and the term does not fill.
func TestTheLastStepEndsTheRun(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	_, d := stepping(t, answers)

	require.NoError(t, d.runStep(t.Context(), stepConsumer(false), delivery("order-1", []byte("x"))))
	require.Len(t, answers.seen(), 1)
}

// TestASTEPThatFailsSettlesNOTHING, so the budget the author wrote for THIS
// step decides whether it is tried again and where it goes when it is not.
func TestAFailedStepIsTheQueuesBusiness(t *testing.T) {
	answers := &answering{name: "wasm", status: 500}
	n, d := stepping(t, answers)

	err := d.runStep(t.Context(), stepConsumer(true), delivery("order-1", []byte("x")))
	require.Error(t, err)
	require.ErrorContains(t, err, "answered 500")

	ready, _, ok := n.QueueDepth("shop/prod/QmCharge")
	require.False(t, ok, "nothing was handed on")
	require.Zero(t, ready)
}

// TestATIERThatCannotStartTheFunctionIsAFailedStep and not a consumed item.
func TestAStepWhoseFunctionCannotRunFails(t *testing.T) {
	answers := &answering{name: "wasm", fail: true}
	_, d := stepping(t, answers)

	require.Error(t, d.runStep(t.Context(), stepConsumer(true), delivery("order-1", []byte("x"))))
}

// TestADRIVERGrantedNothingToInvokeIsACompiledObjectThatDidNotComeFromTcc, and
// says so rather than acking work it never ran.
func TestADriverWithNoTargetFails(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	_, d := stepping(t, answers)

	consumer := stepConsumer(true)
	consumer.Invokes = nil

	require.ErrorIs(t, d.runStep(t.Context(), consumer, delivery("order-1", nil)), ErrNoStepTarget)
}

// TestTHEDISPATCHERRunsABuiltinItselfRatherThanStartingAnInstance. There is no
// artifact for a builtin, so a delivery that went to a tier would ask a store
// for the output of a build that never happened.
func TestTheDispatcherRunsTheStepBuiltin(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, d := stepping(t, answers)

	consumer := stepConsumer(true)
	held, err := n.work.queue(consumer.Queue)
	require.NoError(t, err)
	require.NoError(t, held.Push("order-1", []byte("an order")))

	claimed, err := held.Claim()
	require.NoError(t, err)
	d.deliver(t.Context(), consumer, held, claimed)

	// The step's own function ran, and the driver was never started as a
	// workload: `checkout_reserve` is not a name the tier was asked for.
	seen := answers.seen()
	require.Len(t, seen, 1)
	require.Equal(t, []string{"checkout_reserve"}, seen[0].Headers[headerStep])

	ready, claimedCount := held.Depth()
	require.Zero(t, ready)
	require.Zero(t, claimedCount, "the delivery was acknowledged")
}
