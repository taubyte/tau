package worker

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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

func TestAnUnheldDeploymentIsNotEnumerated(t *testing.T) {
	h := newHarness(t)
	require.Empty(t, h.Worker.Consumers(t.Context()))
}

func TestAContainerConsumerCarriesItsDeclaredPath(t *testing.T) {
	h := newHarness(t)
	holding(t, h, "service", "worker_pool")

	got := consumersOf(t, h)["worker_pool"]
	require.Equal(t, "service", got.Target.Kind)
	require.Equal(t, "orders", got.Queue.Label)
	require.Equal(t, "/work", got.Path, "the path the entry declared")
	require.Contains(t, got.Queue.Handle, "shop/prod/", "the resolved id, scoped to the deployment")
	require.Equal(t, 1, got.Concurrency, "the fixture's own limits/concurrency")
	require.Empty(t, got.Queue.Refused)
}

func TestAConsumerTwoNamespacesDownIsFound(t *testing.T) {
	h := newHarness(t)

	// Resolved at the ROOT and found in a namespace: the walk is over the whole
	// compiled object rather than over what happened to be asked for.
	holding(t, h, "function", "checkout")

	got, ok := consumersOf(t, h)["relay"]
	require.True(t, ok, "the walk never reached shop/local")
	require.Equal(t, []string{"shop", "local"}, got.Target.Scope)
	require.Equal(t, "/", got.Path, "a wasm consumer is dispatched and declares no path")
	require.Zero(t, got.Concurrency, "and declared no concurrency either")
}

func TestTwoConsumersOfOneQueueShareItsHandle(t *testing.T) {
	h := newHarness(t)
	holding(t, h, "function", "checkout")

	all := consumersOf(t, h)
	require.Equal(t, all["relay"].Queue.Handle, all["worker_pool"].Queue.Handle)
}

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

	// The retry budget is the step's, which is why each step has its own queue.
	require.Equal(t, "4", all["checkout_reserve"].Queue.Settings["queue/max_attempts"])
	require.Equal(t, "1", all["checkout_charge"].Queue.Settings["queue/max_attempts"])

	// AND A STEP THAT WAITS IS A QUEUE THAT HOLDS ITS WORK BACK.
	require.Equal(t, "30s", all["checkout_settle"].Queue.Settings["queue/delay"])
}

func TestSendingIsNotReceiving(t *testing.T) {
	h := newHarness(t)
	holding(t, h, "function", "checkout")

	_, listed := consumersOf(t, h)["checkout"]
	require.False(t, listed, "checkout reaches a queue and does not receive from it")
}
