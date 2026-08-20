package schema_test

import (
	"context"
	"testing"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// WHAT A WORKFLOW BECOMES HAS TO BE IN THE OBJECT, or there is nothing to run.
//
// Nothing executes a workflow: what executes is queues and functions. A compiler that
// only reports what a workflow WOULD become — which is what a plan is — produces a
// picture of a program. The lowered view is the program.
func TestLoweredViewHoldsWhatRuns(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)

	authored, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	assert.Assert(t, !hasResource(authored.Flat(), "queue", "checkout_reserve"),
		"the authored project has only the queues somebody wrote")

	lowered, err := schema.Lowered(context.Background(), c)
	assert.NilError(t, err)
	obj, _, err := lowered.Compile(context.Background())
	assert.NilError(t, err)
	flat := obj.Flat()

	for _, name := range []string{"checkout_reserve", "checkout_charge", "checkout_failed"} {
		assert.Assert(t, hasResource(flat, "queue", name), "queue/%s is not in the lowered object", name)
	}
	// the agent's loop and its memory, from the other lowering in the vocabulary
	assert.Assert(t, hasResource(flat, "function", "support_infer"))
	assert.Assert(t, hasResource(flat, "vector", "support_memory"))

	// A DERIVED RESOURCE IS AN ORDINARY RESOURCE. Its fields compiled the way any
	// resource's do — `max-attempts` is a number, and the dead-letter reference resolved
	// to an id like anyone else's.
	q := resource(t, flat, "queue", "checkout_reserve")
	assert.Equal(t, q["max-attempts"], 4)
	dead, _ := q["dead-letter"].(string)
	assert.Assert(t, dead != "" && dead != "queue/checkout_failed",
		"the reference resolved like any other: %q", dead)

	// AND THE ACCESS IT IMPLIES IS ON THE RESOURCE THAT DECLARES IT — the entry an
	// author would have written, on the function, naming the queue it receives from.
	fn := resource(t, flat, "function", "reserve_stock")
	queues, _ := fn["queue"].(map[string]any)
	assert.Assert(t, queues != nil, "the function declares no queue access at all: %v", fn)
	entry, _ := queues["checkout_reserve"].(map[string]any)
	assert.Assert(t, entry != nil, "nothing says this function receives from its step's queue: %v", queues)
	assert.DeepEqual(t, entry["ops"], []string{"receive"})
}

// THE AUTHORED FILES ARE NOT TOUCHED. The view is in memory; a lowering that wrote to
// the repository would be a compiler editing its input.
func TestLoweredViewLeavesTheProjectAlone(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, err = schema.Lowered(context.Background(), c)
	assert.NilError(t, err)

	again, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	assert.Assert(t, !hasResource(again.Flat(), "queue", "checkout_reserve"),
		"compiling the project again found a queue nobody wrote: the view leaked onto disk")
}

func hasResource(flat map[string]any, kind, name string) bool {
	root, ok := flat["object"].(map[string]any)
	if !ok {
		root = flat
	}
	for _, r := range schema.Resources(root) {
		if r.Kind == kind && r.Name == name {
			return true
		}
	}
	return false
}

func resource(t *testing.T, flat map[string]any, kind, name string) map[string]any {
	t.Helper()
	root, ok := flat["object"].(map[string]any)
	if !ok {
		root = flat
	}
	for _, r := range schema.Resources(root) {
		if r.Kind == kind && r.Name == name {
			return r.Fields
		}
	}
	t.Fatalf("no %s/%s", kind, name)
	return nil
}
