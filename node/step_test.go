package node

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	pkgregister "github.com/taubyte/tau/pkg/register"
	primitivesService "github.com/taubyte/tau/services/primitives"
	"github.com/taubyte/tau/services/worker"
)

// step_test drives the one program a workflow was missing: receive, invoke,
// hand on. Everything around it — the retry budget, the backoff, where an
// exhausted item goes — is the queue's, which the lowering already wired.

// stepConsumer is a driver as the worker resolves one.
func stepConsumer(sends bool) worker.Consumer {
	c := worker.Consumer{
		Target:  worker.Target{Project: "shop", Environment: "prod", Scope: []string{"checkout"}, Kind: "function", Name: "checkout_reserve"},
		Queue:   invoke.Grant{Kind: "sync", Label: "checkout_reserve", Handle: "shop/prod/QmReserve", Ops: []string{"receive"}},
		Builtin: stepBuiltin,
		Invokes: []worker.Target{{Project: "shop", Environment: "prod", Kind: "function", Name: "reserve_stock"}},
	}
	if sends {
		c.Sends = []invoke.Grant{{Kind: "sync", Label: "checkout_charge", Handle: "shop/prod/QmCharge", Ops: []string{"send"}}}
	}
	return c
}

func delivery(id string, body []byte) primitivesService.Delivery {
	return primitivesService.Delivery{ID: id, Data: body, Attempt: 1, Deadline: time.Now().Add(time.Minute)}
}

// withStepFunction is a project holding the author's half of a step: the
// function the driver was granted `invoke` on, and the two queues the lowering
// gave the step — the one it receives from and the one it hands on to.
//
// THE QUEUES ARE DECLARED BECAUSE THE SERVICE READS THEM. A send used to carry
// its own configuration on the grant, so a handle naming nothing at all opened
// a queue with pkg/queue's defaults; the primitives service resolves the
// declaration instead and refuses a resource this deployment does not name. So
// a fixture that skipped them was testing a send nothing could have received.
func withStepFunction() []byte {
	return []byte(`{
		"object": {
			"id": "` + projectID + `",
			"name": "shop",
			"function": {
				"QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY": {
					"name": "reserve_stock",
					"execution": {"call": "handle", "memory": 33554432},
					"source": "services/reserve"
				}
			},
			"sync": {
				"QmReserve": {
					"name": "checkout_reserve", "type": "queue",
					"queue": {"consensus": "raft", "visibility_timeout": 60000000000}
				},
				"QmCharge": {
					"name": "checkout_charge", "type": "queue",
					"queue": {"consensus": "raft", "visibility_timeout": 60000000000}
				}
			}
		},
		"indexes": {
			"projects/` + projectID + `/function/reserve_stock/links": [
				"branches/main/projects/` + projectID + `/function/QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY"],
			"projects/` + projectID + `/sync/checkout_reserve/links": [
				"branches/main/projects/` + projectID + `/sync/QmReserve"],
			"projects/` + projectID + `/sync/checkout_charge/links": [
				"branches/main/projects/` + projectID + `/sync/QmCharge"]
		}
	}`)
}

// stepping is a node whose worker answers, and the dispatcher over it.
func stepping(t *testing.T, answers *answering) (*Node, *dispatcher) {
	t.Helper()

	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Discovery, Primitives}},
		WithRuntimes(answers), prebuilt())
	require.NoError(t, n.Start(t.Context()))

	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: withStepFunction(),
	})
	require.NoError(t, err)
	<-n.Primitives().Formed()

	// THE STEP IS PLACED, so this node has to be in its own view before it can
	// take its own work. Without the wait the test asks placement for a worker
	// while the first discovery report is still in flight, and gets "no worker is
	// ready to take this" — on a machine slow enough to notice, which is a
	// property of the machine and not of the step.
	sees(t, n, Worker)

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

	ready, claimed, _ := n.QueueDepth("shop/prod/QmCharge")
	require.Zero(t, ready+claimed, "nothing was handed on")
}

// TestATIERThatCannotStartTheFunctionIsAFailedStep and not a consumed item.
func TestAStepWhoseFunctionCannotRunFails(t *testing.T) {
	answers := &answering{name: "wasm", fail: true}
	_, d := stepping(t, answers)

	require.Error(t, d.runStep(t.Context(), stepConsumer(true), delivery("order-1", []byte("x"))))
}

// TestASTEPThatOnlyWAITSHandsTheItemOnUnchanged. The vocabulary's own fixture
// has one — `- name: settle / wait: 30s` — and the lowering gives it a queue
// whose `delay` IS the pause and a driver with no call to grant. By the time
// this runs the wait has already been served, so the driver's only job is to
// pass the item along.
//
// Refusing here instead sent every run through such a step to the dead letter.
func TestAWaitOnlyStepHandsTheItemOn(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, d := stepping(t, answers)

	consumer := stepConsumer(true)
	consumer.Invokes = nil

	require.NoError(t, d.runStep(t.Context(), consumer, delivery("order-1", []byte("carried"))))
	require.Empty(t, answers.seen(), "a step that waits invokes nothing")

	// AND WHAT IT CARRIES IS UNCHANGED: the run's state is the message.
	claimed, err := n.syncs.Claim(t.Context(), consumer.Sends[0], 1)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, "carried", string(claimed[0].Data))
}

// TestADRIVERGrantedNEITHERIsACompiledObjectThatDidNotComeFromTcc — it is
// neither a step that calls nor a step that waits — and says so rather than
// acking work it never ran.
func TestADriverWithNoTargetAndNoNextFails(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	_, d := stepping(t, answers)

	consumer := stepConsumer(false)
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
	require.NoError(t, n.syncs.Send(t.Context(), consumer.Queue, "order-1", []byte("an order")))

	claimed, err := n.syncs.Claim(t.Context(), consumer.Queue, 1)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	d.deliver(t.Context(), consumer, n.syncs, claimed[0])

	// The step's own function ran, and the driver was never started as a
	// workload: `checkout_reserve` is not a name the tier was asked for.
	seen := answers.seen()
	require.Len(t, seen, 1)
	require.Equal(t, []string{"checkout_reserve"}, seen[0].Headers[headerStep])

	depth, err := n.syncs.Depth(t.Context(), consumer.Queue)
	require.NoError(t, err)
	require.Zero(t, depth.Ready)
	require.Zero(t, depth.Claimed, "the delivery was acknowledged")
}
