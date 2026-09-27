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

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/node"
	nodeDream "github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/invoke"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// workflow_test runs a whole pipeline, compiled by tcc from the vocabulary's
// own fixture rather than assembled here.
//
// THAT IS THE POINT. A workflow LOWERS before anything runs it — one queue per
// step carrying that step's retry budget, one driver per step, a dead-letter
// queue at the end — so a hand-written compiled object would be a fixture of
// what I believe the lowering produces and would keep passing on the day it
// produced something else. What this shows is that what tcc ACTUALLY produces
// runs: the drivers are found at all (they live in a scope that is not a
// namespace), each invokes the function its step named, and the run advances
// from one step's queue to the next.
//
// THE ONE THING SUBSTITUTED IS WHICH ARTIFACT each function runs, which is a
// deployment fact and not a lowering one: the fixture's functions name paths in
// a repository nobody built here, so they are pointed at the component this
// package already ships. That fact is said through the ARTIFACT SEAM rather than
// written into the document — see nodeDream.Built — so the fixture's own sources
// are untouched, the drivers' `source: {kind: builtin}` included, which is the
// thing under test.

// runnableFixture is the vocabulary's project, and the mapping saying which
// component each of its authored functions runs.
//
// THE ENTRYPOINT IS STILL REWRITTEN, and it is the one rewrite that has to be:
// the exported symbol belongs to the ARTIFACT, so pointing a function at a
// different component and keeping the author's `call` names an export that is not
// there. Nothing outside the document can say that.
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

	pointed := map[string]string{}
	var walk func(scope map[string]any)
	walk = func(scope map[string]any) {
		for kind, group := range scope {
			instances, ok := group.(map[string]any)
			if !ok {
				continue
			}
			for id, v := range instances {
				res, ok := v.(map[string]any)
				if !ok {
					continue
				}
				if kind == "function" {
					source, _ := res["source"].(map[string]any)
					// A DRIVER IS LEFT ALONE. Its source is the builtin this
					// whole test is about, and a builtin has no artifact.
					if s, _ := source["kind"].(string); s != "builtin" {
						// A FUNCTION THAT NAMES A COMPONENT RUNS THE
						// COMPONENT'S BUILD, so that is the id the artifact is
						// filed under -- which is what this fixture's `checkout`
						// and `edge_router` do: they name `paylib`.
						if component, _ := source["component"].(string); component != "" {
							pointed[component] = componentCID
						} else {
							pointed[id] = componentCID
						}
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

// TestAWholePipelineRuns: a workload starts it, every driver invokes the
// function its step named, and the run advances to the end.
func TestAWorkflowRunsEndToEnd(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	m, err := dream.New(t.Context())
	require.NoError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	require.NoError(t, err)
	require.NoError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			nodeDream.Protocol: {Others: nodeDream.Services(
				node.Register, node.Worker, node.Stats, node.Primitives)},
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

	compiled, pointed := runnableFixture(t, cid)
	stagedOn(t, pids, "shop", pointed)

	_, err = n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled,
	})
	require.NoError(t, err)

	// THE DEPLOYMENT HAS TO BE HELD BEFORE ANYTHING IS DRAINED, which is the
	// enumeration rule: a node drains the queues of what it is already serving.
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

	// A WORKLOAD STARTS IT, through the host function the vocabulary declares.
	// Nothing here sends to a queue.
	//
	// RETRIED, because a cloud's consensus for queues forms in the background
	// and a send that arrives first is told `unavailable` rather than made to
	// wait out raft's bootstrap.
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

	// AND THE PIPELINE RUNS ITSELF. `reserve` invokes `reserve_stock` and hands
	// on, `settle` waits the 30s its author wrote, `charge` invokes
	// `charge_card` and the run ends with nothing left anywhere.
	//
	// The 30s delay is why this is generous: it is the vocabulary's own number
	// and waiting it out is what proves a step that waits is a queue that holds
	// its work back.
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

	// THE RUN REACHES THE LAST STEP. `reserve` invokes `reserve_stock` and
	// hands on, `settle` waits the 30s its author wrote, `charge` invokes
	// `charge_card` — so this waits out a real delay, which is what shows a
	// step that waits is a queue that holds its work back.
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

	// SETTLE IS OBSERVED BECAUSE IT HOLDS. Its queue carries the 30s delay its
	// author wrote, so an item sits in it far longer than this poll — which is
	// what makes "the run passed through the step that waits" something that
	// can be watched at all.
	require.Contains(t, seen, "checkout_settle", "the run never reached the step that waits")

	// AND `charge` IS NOT ASSERTED THE SAME WAY, deliberately: it drains as soon
	// as it is claimed, so a poll never catches it holding anything and an
	// assertion that it was seen occupied would fail on a run that worked. What
	// shows the run reached it is below — the settle queue emptied and NOTHING
	// WAS BURIED, so the item left settle and no step gave up on it.

	// NOTHING REACHED THE DEAD LETTER, which is where a run stops when a step
	// exhausts its attempts — so every step succeeded rather than going quiet.
	//
	// THIS IS THE ASSERTION THAT WAS VACUOUS, and it is why the wait-only step
	// that buried every run through it shipped: the handle it read named
	// nothing, so an unopened queue and an empty one were the same answer. It
	// resolves the real handle now and asserts the queue was never opened at
	// all — nothing was ever buried, rather than nothing is buried right now.
	ready, claimed, ok := n.QueueDepth(deadLetterOf(t, n))
	require.True(t, ok, "this node cannot read what was buried")
	require.Zero(t, ready+claimed, "a step gave up and the run was buried")
}

// handleOf is the handle one of the workflow's produced queues is opened under.
//
// IT RESOLVES THROUGH THE READ PATH rather than being built here, which is the
// thing that was broken: the queues live at `workflow/<id>/queue/<name>`, and
// nothing could address them while every scope segment was assumed to be a
// namespace.
//
// IT FAILS THE TEST RATHER THAN ANSWERING SOMETHING. An earlier version fell
// back to a handle it assembled itself when a queue had no driver — which the
// dead letter deliberately does not — and that handle named nothing, so
// QueueDepth reported "never opened", the assertion read it as empty, and a
// run that was being buried on every pass looked like a run that finished.
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
