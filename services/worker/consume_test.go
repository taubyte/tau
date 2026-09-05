package worker

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// consume_test is the enumeration, against the vocabulary's own fixture: what a
// node should be draining is read off the compiled projects it already holds,
// and every part of that sentence is somewhere a walk can go wrong.

// consumersOf is the list keyed by the workload's name.
func consumersOf(t *testing.T, h *harness) map[string]Consumer {
	t.Helper()

	out := map[string]Consumer{}
	for _, c := range h.Worker.Consumers(t.Context()) {
		out[c.Target.Name] = c
	}
	return out
}

// holding resolves one resource so the node holds the deployment, which is what
// makes it enumerable at all.
func holding(t *testing.T, h *harness, kind, name string) {
	t.Helper()
	_, err := h.Worker.Resolve(t.Context(), Target{
		Project: "shop", Environment: "prod", Kind: kind, Name: name,
	})
	require.NoError(t, err)
}

// TestNOTHINGIsDrainedUntilSomethingHoldsIt. The register deliberately does not
// enumerate, so what a node drains is what it is already serving.
func TestAnUnheldDeploymentIsNotEnumerated(t *testing.T) {
	h := newHarness(t)
	require.Empty(t, h.Worker.Consumers(t.Context()))
}

// TestTheFIXTURESConsumerIsFound. `application/worker_pool` declares
// `queue: orders: {ops: [receive], deliver: {port: work, path: /work}}`, which
// is the whole shape: a container that is pushed to on a path it chose.
func TestAContainerConsumerCarriesItsDeclaredPath(t *testing.T) {
	h := newHarness(t)
	holding(t, h, "application", "worker_pool")

	got := consumersOf(t, h)["worker_pool"]
	require.Equal(t, "application", got.Target.Kind)
	require.Equal(t, "orders", got.Queue.Label)
	require.Equal(t, "/work", got.Path, "the path the entry declared")
	require.Contains(t, got.Queue.Handle, "shop/prod/", "the resolved id, scoped to the deployment")
	require.Equal(t, 1, got.Concurrency, "the fixture's own limits/concurrency")
	require.Empty(t, got.Queue.Refused)
}

// TestTHEWALKDescendsIntoNamespaces. `shop/eu/relay` receives from the
// project-wide queue and is two levels down; a walk that stopped at the top
// would drain nothing for it and nothing would say so.
func TestAConsumerTwoNamespacesDownIsFound(t *testing.T) {
	h := newHarness(t)

	// Resolved at the ROOT and found in a namespace: the walk is over the whole
	// compiled object rather than over what happened to be asked for.
	holding(t, h, "function", "checkout")

	got, ok := consumersOf(t, h)["relay"]
	require.True(t, ok, "the walk never reached shop/eu")
	require.Equal(t, []string{"shop", "eu"}, got.Target.Scope)
	require.Equal(t, "/", got.Path, "a wasm consumer is dispatched and declares no path")
	require.Zero(t, got.Concurrency, "and declared no concurrency either")
}

// TestTwoWorkloadsMayRECEIVEFromOneQueue, which is a pool of consumers and is
// what `limits/max_instances` on the container half already implies. The two
// resolve to the SAME handle, so they are draining one queue rather than two.
func TestTwoConsumersOfOneQueueShareItsHandle(t *testing.T) {
	h := newHarness(t)
	holding(t, h, "function", "checkout")

	all := consumersOf(t, h)
	require.Equal(t, all["relay"].Queue.Handle, all["worker_pool"].Queue.Handle)
}

// TestAWORKFLOWSStepsAreConsumersToo, and they are the reason the walk descends
// into a scope that is not a namespace.
//
// A workflow LOWERS to one queue and one driver per step, indexed under the
// workflow itself — `workflow/<id>/queue/checkout_reserve`. Nothing found them
// until the read path stopped assuming every scope segment was a `namespace`,
// so a workflow compiled, deployed, and had every step's queue drained by
// nobody.
func TestAWorkflowsStepDriversAreFound(t *testing.T) {
	h := newHarness(t)
	holding(t, h, "function", "checkout")

	all := consumersOf(t, h)
	for _, step := range []string{"checkout_reserve", "checkout_charge", "checkout_settle"} {
		got, ok := all[step]
		require.True(t, ok, "%s was not found: %v", step, all)
		require.Equal(t, []string{"checkout"}, got.Target.Scope, "it lives inside the workflow")
		require.Equal(t, step, got.Queue.Label, "and receives from the queue of its own name")
	}

	// THE RETRY BUDGET IS THE STEP'S, which is the whole reason the lowering
	// gives each one its own queue rather than sharing.
	require.Equal(t, "4", all["checkout_reserve"].Queue.Settings["max_attempts"])
	require.Equal(t, "1", all["checkout_charge"].Queue.Settings["max_attempts"])

	// AND A STEP THAT WAITS IS A QUEUE THAT HOLDS ITS WORK BACK.
	require.Equal(t, "30s", all["checkout_settle"].Queue.Settings["delay"])
}

// TestAWorkloadThatOnlySENDSIsNotAConsumer. `send` is a host function the guest
// calls; only `receive` is a delivery, and a dispatcher that drained for a
// producer would invoke it with its own work.
func TestSendingIsNotReceiving(t *testing.T) {
	h := newHarness(t)
	holding(t, h, "function", "checkout")

	_, listed := consumersOf(t, h)["checkout"]
	require.False(t, listed, "checkout reaches a queue and does not receive from it")
}
