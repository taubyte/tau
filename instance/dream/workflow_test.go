//go:build dreaming

package dream_test

import (
	"strings"
	"testing"
	"time"

	storageService "github.com/taubyte/tau/services/storage"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
	nodeDream "github.com/taubyte/tau/instance/dream"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
)

// A whole pipeline built from projects/with/workflow. A workflow lowers (one queue
// and driver per step, a dead-letter queue at the end), so this shows what tcc
// produces runs. Each function's package is uploaded with a shipped component; the
// drivers' builtin sources are untouched.

func TestAWorkflowRunsEndToEnd(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Services: []string{instance.Register, instance.Worker, instance.Discovery, instance.Primitives, storageService.Descriptor.Name, instance.Runner},
	})
	require.NoError(t, err)
	defer u.Close()

	pids := u.Pids()
	require.NotEmpty(t, pids)

	n := nodeOf(t, pids[0])
	publish(t, u, pids[:1], deployed{project: "shop", name: "with/workflow"}, artifacts{"": cacherWASM(t)})

	// The deployment must be held before anything drains: a node drains the queues of
	// what it serves.
	producer := specs.Address{Project: "shop", Environment: "prod", Kind: "function", Name: "checkout"}
	spec, err := n.Worker().Resolve(t.Context(), producer)
	require.NoError(t, err)

	// The start grant carries where a start lands, off the workflow's own entry
	// record — so this test names no step.
	var start invoke.Grant
	for _, g := range spec.Reaches {
		if g.Kind == "workflow" {
			start = g
		}
	}
	require.NotEmpty(t, start.Settings["entry/start/handle"], "the composer resolved no entry point")

	// A workload starts it through the vocabulary's host function. Retried, since queue
	// consensus forms in the background.
	var last string
	require.Eventually(t, func() bool {
		res, err := n.Worker().Invoke(t.Context(), producer, &invoke.Request{
			Method: "POST", Path: "/start/checkout/order-1", Body: []byte("an order"),
		})
		if err != nil {
			last = err.Error()
			return false
		}
		last = string(res.Body)
		return res.Status == 204
	}, formingBudget, time.Second, "the run could never be started: %s", &lastOf{&last})

	// The pipeline runs itself: reserve invokes reserve_stock, settle waits its 30s,
	// charge invokes charge_card.
	steps := map[string]string{}
	for _, step := range []string{"reserve", "settle", "charge"} {
		steps["checkout_"+step] = handleOf(t, n, "checkout_"+step)
	}

	// A QUEUE NOTHING HAS OPENED HOLDS NOTHING, which is what `ok` reports: the
	// dispatcher opens a step's queue the first time it drains it, so an
	// unopened one is a step no item has reached.
	depth := func(handle string) int {
		ready, claimed, ok := n.QueueDepth(handle)
		if !ok {
			return 0
		}
		return ready + claimed
	}

	// The run reaches the last step, waiting out settle's real 30s delay.
	var seen []string
	require.Eventually(t, func() bool {
		for name, handle := range steps {
			if depth(handle) > 0 {
				seen = append(seen, name)
			}
		}
		return depth(steps["checkout_charge"]) == 0 && len(seen) > 0 &&
			depth(steps["checkout_reserve"]) == 0 && depth(steps["checkout_settle"]) == 0
	}, 180*time.Second, time.Second, "the run never finished; it was seen in %v", &seenSteps{&seen})

	// Settle is observed because its 30s delay holds the item longer than the poll.
	require.Contains(t, seen, "checkout_settle", "the run never reached the step that waits")

	// charge drains as soon as it is claimed, so it is never seen holding anything; the
	// settle queue emptying with nothing buried shows the run reached it.

	// Nothing reached the dead letter: the queue was never opened, so nothing was ever
	// buried.
	ready, claimed, ok := n.QueueDepth(deadLetterOf(t, n))
	require.True(t, ok, "this node cannot read what was buried")
	require.Zero(t, ready+claimed, "a step gave up and the run was buried")
}

// handleOf is the handle a workflow's produced queue opens under, resolved through
// the read path (queues live at `workflow/<id>/queue/<name>`). It fails the test
// rather than assembling a handle, which would name nothing and read as empty.
func handleOf(t *testing.T, n *instance.Instance, step string) string {
	t.Helper()

	spec, err := n.Worker().Resolve(t.Context(), specs.Address{
		Project: "shop", Environment: "prod", Scope: []string{"checkout"},
		Kind: "function", Name: step,
	})
	require.NoError(t, err, "no driver for %s", step)

	for _, g := range spec.Reaches {
		if g.Kind == "sync" && g.Label == step {
			return g.Handle
		}
	}
	t.Fatalf("%s receives from no queue of its own name", step)
	return ""
}

// deadLetterOf is where a run stops when a step gives up. It has NO consumer by
// design, so it is named by the step that routes into it rather than resolved
// as a driver of its own.
func deadLetterOf(t *testing.T, n *instance.Instance) string {
	t.Helper()

	spec, err := n.Worker().Resolve(t.Context(), specs.Address{
		Project: "shop", Environment: "prod", Scope: []string{"checkout"},
		Kind: "function", Name: "checkout_charge",
	})
	require.NoError(t, err)

	for _, g := range spec.Reaches {
		if dead := g.Settings["queue/dead_letter/name"]; g.Kind == "sync" && dead != "" {
			return specs.Handle("shop", "prod", dead)
		}
	}
	t.Fatal("no step routes into a dead letter")
	return ""
}

// seenSteps renders which steps the run was observed in, at the moment an
// assertion gives up rather than at the moment it was written.
type seenSteps struct{ of *[]string }

func (s *seenSteps) String() string {
	shown := map[string]bool{}
	var out []string
	for _, name := range *s.of {
		if !shown[name] {
			shown[name] = true
			out = append(out, name)
		}
	}
	return strings.Join(out, " ")
}
