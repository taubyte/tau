package schema_test

import (
	"context"
	"sort"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/interp"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// COMPILING IS LOWERING. Nothing executes a workflow: what executes is the queues and
// functions it becomes, so an object without them is an object missing what it needs.
// There is no second call to remember.
func TestCompileHoldsWhatRuns(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)

	authored, _, err := c.Authored(context.Background())
	assert.NilError(t, err)
	assert.Assert(t, !hasResource(authored.Flat(), "queue", "checkout_reserve"),
		"what was WRITTEN has only the queues somebody wrote")

	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	flat := obj.Flat()

	for _, name := range []string{"checkout_reserve", "checkout_charge", "checkout_failed"} {
		assert.Assert(t, hasResource(flat, "queue", name), "queue/%s is not in the object", name)
	}
	// the agent's loop and its memory, from the other lowering in the vocabulary
	assert.Assert(t, hasResource(flat, "function", "support_infer"))
	assert.Assert(t, hasResource(flat, "vector", "support_memory"))

	// A PRODUCED RESOURCE IS AN ORDINARY RESOURCE. It went through the same pipeline, so
	// `max-attempts` is a number and its dead-letter reference resolved to an id like
	// anyone else's — which is the whole claim: this is config, not a picture of it.
	q := resource(t, flat, "queue", "checkout_reserve")
	assert.Equal(t, q["max-attempts"], 4)
	dead, _ := q["dead-letter"].(string)
	assert.Assert(t, dead != "" && dead != "queue/checkout_failed",
		"the reference resolved like any other: %q", dead)

	// AND THE ACCESS IT IMPLIES IS ON THE RESOURCE THAT DECLARES IT — the entry an author
	// would have written, on the function, naming the queue it receives from.
	fn := resource(t, flat, "function", "reserve_stock")
	queues, _ := fn["queue"].(map[string]any)
	assert.Assert(t, queues != nil, "the function declares no queue access at all: %v", fn)
	entry, _ := queues["checkout_reserve"].(map[string]any)
	assert.Assert(t, entry != nil, "nothing says this function receives from its step's queue: %v", queues)
	assert.DeepEqual(t, entry["ops"], []string{"receive"})
}

// THE FILES ARE NOT TOUCHED. The view a lowering is applied to is in memory; a compiler
// that wrote to the repository would be one nobody could trust with a repository.
func TestCompileLeavesTheProjectAlone(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.NilError(t, err)

	again, _, err := c.Authored(context.Background())
	assert.NilError(t, err)
	assert.Assert(t, !hasResource(again.Flat(), "queue", "checkout_reserve"),
		"reading the project again found a queue nobody wrote: the view leaked onto disk")
}

// THE OBJECT SAYS WHAT NOBODY WROTE. Without that record it cannot be taken apart again:
// decompile would write a queue into somebody's repository as though they had asked for
// it, and a console could not tell a generated resource from an authored one.
func TestObjectRecordsWhatItLowered(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	flat := obj.Flat()

	rec := interp.LoweredOf(flat)
	assert.Assert(t, len(rec.Resources) > 0, "nothing was recorded")

	workflow := idOf(t, flat, "workflow", "checkout")
	made, added := schema.Became(flat, workflow)
	assert.Equal(t, len(made), 3, "one queue per step, and the one they fail into")
	for _, id := range made {
		assert.Equal(t, rec.Resources[id].From, workflow)
		assert.Equal(t, rec.Resources[id].Kind, "queue")
		assert.Assert(t, rec.Produced(id))
	}

	// ...and the entries it put on resources somebody else wrote, which is the other half
	// of what has to be taken back off.
	byWhat := map[string]string{}
	for _, g := range added {
		byWhat[g.Kind+"/"+g.Name+" "+g.Op] = g.On
	}
	assert.Equal(t, byWhat["queue/checkout_reserve receive"], idOf(t, flat, "function", "reserve_stock"))
	assert.Equal(t, byWhat["queue/checkout_charge send"], idOf(t, flat, "function", "reserve_stock"))
	assert.Equal(t, byWhat["queue/checkout_charge receive"], idOf(t, flat, "function", "charge_card"))

	// A resource that becomes nothing has no record and answers with nothing.
	none, alsoNone := schema.Became(flat, idOf(t, flat, "function", "checkout"))
	assert.Equal(t, len(none), 0)
	assert.Equal(t, len(alsoNone), 0)
}

// A LOWERABLE RESOURCE IN A NAMESPACE produces into that namespace. Scope arithmetic is
// where a path builder goes wrong, and it goes wrong silently: a resource placed one
// level up is a resource in the wrong namespace, and it compiles.
func TestLoweringInsideANamespace(t *testing.T) {
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

	for _, r := range schema.Resources(rootOf(obj.Flat())) {
		if r.Kind == "queue" && r.Name == "ship_pack" {
			assert.Equal(t, r.Scope, "shop")
			return
		}
	}
	t.Fatal("queue/ship_pack is not in the compiled object")
}

// TWO OPS ON ONE TARGET ARE BOTH KEPT. A step whose failure goes back to its own queue
// makes its function both receive from that queue and send to it — two entries at one
// address. Written one at a time, the second silently replaced the first, and what was
// lost is the `receive`: the queue keeps its work and nothing consumes it, which is a
// deadlock that compiles.
func TestTwoOpsOnOneTargetMerge(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/workflow/retry.yaml", []byte(
		"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zr\n"+
			"description: a step that sends its failures back to itself\n"+
			"steps:\n"+
			"    - name: again\n"+
			"      run: charge_card\n"+
			"      attempts: 2\n"+
			"      on-error: again\n"), 0o644))

	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	fn := resource(t, obj.Flat(), "function", "charge_card")
	queues, _ := fn["queue"].(map[string]any)
	entry, _ := queues["retry_again"].(map[string]any)
	assert.Assert(t, entry != nil, "the function declares nothing about its own queue: %v", queues)
	ops, _ := entry["ops"].([]string)
	sort.Strings(ops)
	assert.DeepEqual(t, ops, []string{"receive", "send"})
}

// AN AUTHORED ENTRY SURVIVES a lowering that adds another op to the same target. The
// author's access is theirs; a compiler that replaced it would be taking away something
// somebody wrote.
func TestAnAuthoredEntryIsNotReplaced(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/workflow/hand.yaml", []byte(
		"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zs\n"+
			"description: one step, whose function already sends to the queue it will receive from\n"+
			"steps:\n"+
			"    - name: only\n"+
			"      run: relay_hand\n"+
			"      attempts: 1\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/function/relay_hand.yaml", []byte(
		"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zt\n"+
			"description: sends to the queue it also receives from, and said so itself\n"+
			"source: \".\"\n"+
			"execution:\n"+
			"    call: relay\n"+
			"    timeout: 5s\n"+
			"    memory: 16MB\n"+
			"queue:\n"+
			"    hand_only:\n"+
			"        ops: [send]\n"), 0o644))

	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	fn := resource(t, obj.Flat(), "function", "relay_hand")
	queues, _ := fn["queue"].(map[string]any)
	entry, _ := queues["hand_only"].(map[string]any)
	assert.Assert(t, entry != nil, "%v", queues)
	ops, _ := entry["ops"].([]string)
	sort.Strings(ops)
	// the author wrote send; the lowering added receive
	assert.DeepEqual(t, ops, []string{"receive", "send"})
}

// COMPILING TWICE GIVES THE SAME OBJECT. Ids are derived, names are templated, and the
// order things are placed in is fixed — so a build that looked like a delete and a create
// every time would be a build nothing downstream could trust.
func TestLoweringIsDeterministic(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	first, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	second, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	assert.DeepEqual(t, first.Flat(), second.Flat())
}

func hasResource(flat map[string]any, kind, name string) bool {
	for _, r := range schema.Resources(rootOf(flat)) {
		if r.Kind == kind && r.Name == name {
			return true
		}
	}
	return false
}

func resource(t *testing.T, flat map[string]any, kind, name string) map[string]any {
	t.Helper()
	for _, r := range schema.Resources(rootOf(flat)) {
		if r.Kind == kind && r.Name == name {
			return r.Fields
		}
	}
	t.Fatalf("no %s/%s", kind, name)
	return nil
}

func idOf(t *testing.T, flat map[string]any, kind, name string) string {
	t.Helper()
	for _, r := range schema.Resources(rootOf(flat)) {
		if r.Kind == kind && r.Name == name {
			return r.ID
		}
	}
	t.Fatalf("no %s/%s", kind, name)
	return ""
}

func rootOf(flat map[string]any) map[string]any {
	if root, ok := flat["object"].(map[string]any); ok {
		return root
	}
	return flat
}

// AN OP THE AUTHOR ALSO WROTE IS NOT THE COMPILER'S TO TAKE BACK. A lowering that grants
// what somebody already granted adds nothing — and must record nothing, or decompiling to
// what was written would delete a line out of their repository.
func TestAnAuthoredOpIsNotRecordedAsGranted(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/workflow/hand.yaml", []byte(
		"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zs\n"+
			"description: one step, whose function already declared the receive\n"+
			"steps:\n"+
			"    - name: only\n"+
			"      run: relay_own\n"+
			"      attempts: 1\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/function/relay_own.yaml", []byte(
		"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zu\n"+
			"description: says for itself that it receives from the queue its step will make\n"+
			"source: \".\"\n"+
			"execution:\n"+
			"    call: relay\n"+
			"    timeout: 5s\n"+
			"    memory: 16MB\n"+
			"queue:\n"+
			"    hand_only:\n"+
			"        ops: [receive]\n"), 0o644))

	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	fnID := idOf(t, obj.Flat(), "function", "relay_own")
	for _, g := range interp.LoweredOf(obj.Flat()).Entries {
		assert.Assert(t, !(g.On == fnID && g.Op == "receive"),
			"the compiler claims it granted an op the author wrote: %+v", g)
	}
}

// AN ENTRY CAN LAND ON A RESOURCE THE SAME LOWERING PRODUCED. An agent's loop is
// generated, and the entry saying it searches the memory that lowering also generated
// belongs on the loop. A check that looked only at what somebody WROTE deleted it, and
// the memory became a resource nothing reaches — which reads, in a drawing and in a
// deploy, as a feature that was never wired up.
func TestAnEntryLandsOnAProducedResource(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	loop := resource(t, obj.Flat(), "function", "support_infer")
	vectors, _ := loop["vector"].(map[string]any)
	assert.Assert(t, vectors != nil, "the loop declares no vector access at all: %v", loop)
	entry, _ := vectors["support_memory"].(map[string]any)
	assert.Assert(t, entry != nil, "nothing says the loop searches the memory: %v", vectors)
	assert.DeepEqual(t, entry["ops"], []string{"search"})
}

// THE ROOT DOCUMENT IS WHAT THE SCHEMA SAYS IT IS. This vocabulary declares
// Leaf("project"), so its root is project.yaml — and a binding that answered with the
// engine's fallback named a file that does not exist, which made the project's own
// document unaddressable and unreadable through the session.
func TestSessionKnowsTheRootDocument(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	s, err := schema.AdoptSession(fs)
	assert.NilError(t, err)

	assert.DeepEqual(t, s.Root(), []string{"project"})

	id, err := s.Get(s.Root(), []string{"id"})
	assert.NilError(t, err, "the project's own document is not readable")
	assert.Assert(t, id != "")
}
