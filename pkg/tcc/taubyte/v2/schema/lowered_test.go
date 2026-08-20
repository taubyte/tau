package schema_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/afero"
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

// ONE RESOURCE, LOWERED. A console opens a workflow and shows what it becomes; a CLI
// prints it before applying anything. Both ask about one resource, so that is the unit —
// and it is the same computation the whole-project view applies, not a second one that
// looks like it.
func TestEmissionOfOneResource(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	e, ok := schema.EmissionOf(obj.Flat(), "workflow", "checkout", "")
	assert.Assert(t, ok, "a workflow that lowers has an emission")

	docs := map[string]string{}
	for _, d := range e.Documents {
		docs[d.Path] = d.Body
	}
	assert.Equal(t, len(docs), 3, "one queue per step, and the one they fail into: %v", keys(docs))
	assert.Assert(t, strings.Contains(docs["/queue/checkout_reserve.yaml"], "max-attempts: 4"))

	// ...and what it puts on documents somebody else wrote, which is the half a diff has
	// to show: the function that runs a step gains the entry saying it receives.
	entries := map[string]string{}
	for _, x := range e.Entries {
		entries[x.Path+" "+x.Kind+"/"+x.Name] = x.Op
	}
	assert.Equal(t, entries["/function/reserve_stock.yaml queue/checkout_reserve"], "receive")
	assert.Equal(t, entries["/function/reserve_stock.yaml queue/checkout_charge"], "send")
	assert.Equal(t, entries["/function/charge_card.yaml queue/checkout_charge"], "receive")

	// A resource that lowers into nothing has no emission rather than an empty one.
	_, ok = schema.EmissionOf(obj.Flat(), "function", "checkout", "")
	assert.Assert(t, !ok)
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// A LOWERABLE RESOURCE IN A NAMESPACE puts its documents in that namespace, and the
// entries it places carry the way back. Scope arithmetic is where a path builder goes
// wrong, and it goes wrong silently: a document written one directory up is a resource
// in the wrong namespace, which compiles.
func TestEmissionInsideANamespace(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/namespace/shop/workflow/ship.yaml", []byte(
		"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zq\n"+
			"description: pack it, then hand it over\n"+
			"steps:\n"+
			"    - name: pack\n"+
			"      run: notify\n"+
			"      attempts: 2\n"), 0o644))

	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	e, ok := schema.EmissionOf(obj.Flat(), "workflow", "ship", "shop")
	assert.Assert(t, ok)
	assert.Equal(t, e.Documents[0].Path, "/namespace/shop/queue/ship_failed.yaml")
	assert.Equal(t, e.Documents[1].Path, "/namespace/shop/queue/ship_pack.yaml")
	// `notify` lives in shop too, so the entry lands there and says nothing about
	// namespace: the queue is in the same place.
	assert.Equal(t, e.Entries[0].Path, "/namespace/shop/function/notify.yaml")
	assert.Equal(t, e.Entries[0].Namespace, "")

	// ...and it all compiles, in the right namespace.
	lowered, err := schema.Lowered(context.Background(), c)
	assert.NilError(t, err)
	obj, _, err = lowered.Compile(context.Background())
	assert.NilError(t, err)
	for _, r := range schema.Resources(rootOf(obj.Flat())) {
		if r.Kind == "queue" && r.Name == "ship_pack" {
			assert.Equal(t, r.Scope, "shop")
			return
		}
	}
	t.Fatal("queue/ship_pack is not in the lowered object")
}

func rootOf(flat map[string]any) map[string]any {
	if root, ok := flat["object"].(map[string]any); ok {
		return root
	}
	return flat
}

// THROUGH THE SESSION, which is how a CLI and a console ask everything else. The address
// is the ordinary one — the same []string that Get and ValidateResource take — so a
// caller that can validate a resource can lower it.
func TestSessionLowersOneResource(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	s, err := schema.AdoptSession(fs)
	assert.NilError(t, err)

	e, err := s.Lower(context.Background(), []string{"workflow", "checkout"}, schema.CompileOptions{Branch: "main"})
	assert.NilError(t, err)
	assert.Equal(t, len(e.Documents), 3)
	assert.Equal(t, e.Of.Kind+"/"+e.Of.Name, "workflow/checkout")

	// A resource that becomes nothing answers "nothing" rather than failing: most
	// resources do not lower, and asking is not a mistake.
	none, err := s.Lower(context.Background(), []string{"function", "checkout"}, schema.CompileOptions{Branch: "main"})
	assert.NilError(t, err)
	assert.Equal(t, len(none.Documents), 0)

	// ...and the whole project, lowered, is the object a runtime is handed.
	obj, err := s.Lowered(context.Background(), schema.CompileOptions{Branch: "main"})
	assert.NilError(t, err)
	assert.Assert(t, hasResource(obj.Flat(), "queue", "checkout_reserve"))
}
