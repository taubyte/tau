//go:build dreaming

package dream_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node"
	nodeDream "github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/invoke"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// A whole pipeline compiled by tcc from the vocabulary's fixture. A workflow lowers
// (one queue and driver per step, a dead-letter queue at the end), so this shows
// what tcc produces runs. Only each function's artifact is substituted, through the
// artifact seam; the drivers' builtin sources are untouched.

// runnableFixture is the vocabulary's project and the mapping of each authored
// function to its component. The entrypoint is rewritten: the export belongs to the
// artifact, so a different component needs a different `call`.
func runnableFixture(t *testing.T, componentCID string) ([]byte, map[string]string) {
	t.Helper()

	c, err := schema.New(
		schema.WithLocal("../../pkg/tcc/taubyte/v2/fixtures/basic"),
		schema.WithBranch("main"))
	require.NoError(t, err)

	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err)

	flat := obj.Flat()
	root, ok := flat["object"].(map[string]any)
	require.True(t, ok)

	// Every package by id, wherever its scope: a lowered workflow's drivers' package is
	// in the workflow's own.
	packages := map[string]map[string]any{}
	var collect func(scope map[string]any)
	collect = func(scope map[string]any) {
		for kind, group := range scope {
			instances, ok := group.(map[string]any)
			if !ok {
				continue
			}
			for id, v := range instances {
				if res, ok := v.(map[string]any); ok {
					if kind == "package" {
						packages[id] = res
					}
					collect(res)
				}
			}
		}
	}
	collect(root)

	pointed := map[string]string{}
	var walk func(scope map[string]any)
	walk = func(scope map[string]any) {
		for kind, group := range scope {
			instances, ok := group.(map[string]any)
			if !ok {
				continue
			}
			for _, v := range instances {
				res, ok := v.(map[string]any)
				if !ok {
					continue
				}
				// The workflow's run ceiling and the steps' selectors are removed: nothing counts
				// runs, so the worker would refuse the start (unservableWorkflow).
				if kind == "workflow" {
					delete(res, "concurrency")
					steps, _ := res["steps"].([]any)
					for _, s := range steps {
						step, _ := s.(map[string]any)
						if call, ok := step["call"].(map[string]any); ok {
							delete(call, "input")
							delete(call, "result")
						}
					}
				}
				if kind == "function" {
					ref, _ := res["package"].(map[string]any)
					pkgID, _ := ref["name"].(string)
					source, _ := packages[pkgID]["source"].(map[string]any)
					// A driver runs a builtin package, which has no artifact; an image has its own.
					if s, _ := source["kind"].(string); s != "builtin" && s != "image" {
						// Every function runs this one build, filed under its package's id (checkout
						// and edge_router both run paylib).
						pointed[pkgID] = componentCID
						if exec, ok := res["execution"].(map[string]any); ok {
							exec["call"] = "handle"
						}
					}
				}
				walk(res)
			}
		}
	}
	walk(root)
	require.NotZero(t, len(pointed))

	raw, err := json.Marshal(flat)
	require.NoError(t, err)
	return raw, pointed
}

func TestAWorkflowRunsEndToEnd(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Services: []string{node.Register, node.Worker, node.Discovery, node.Primitives},
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

	compiled, pointed := runnableFixture(t, cid)
	stagedOn(t, pids, "shop", pointed)

	_, err = n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled,
	})
	require.NoError(t, err)

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
func handleOf(t *testing.T, n *node.Node, step string) string {
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
func deadLetterOf(t *testing.T, n *node.Node) string {
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
