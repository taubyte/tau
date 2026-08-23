package schema_test

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/interp"
	"github.com/taubyte/tau/pkg/tcc/jsonschema"
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
	assert.Equal(t, q["max_attempts"], 4)
	dead, _ := q["dead_letter"].(map[string]any)
	name, _ := dead["name"].(string)
	assert.Assert(t, name != "" && name != "checkout_failed",
		"the reference resolved like any other: %v", q["dead_letter"])

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
	assert.Equal(t, len(made), 4, "one queue per step, the one they fail into, and the scope holding them")

	kinds := map[string]int{}
	for _, id := range made {
		assert.Equal(t, rec.Resources[id].From, workflow)
		assert.Assert(t, rec.Produced(id))
		kinds[rec.Resources[id].Kind]++
	}
	// THE SCOPE IS RECORDED LIKE ANYTHING ELSE IT PRODUCED. It has to be: decompiling to
	// what somebody wrote has to take the directory back out too, or the next compile
	// finds an authored scope it did not write and refuses to produce into it.
	//
	// And it is a GROUP. What a workflow becomes behaves like a namespace — indexed,
	// addressable, resolving a bare name outward — and is not one: a namespace is a line
	// somebody drew, this is the inside of a construct, and a record calling it a
	// namespace is the object saying the author wrote something they did not.
	assert.Equal(t, kinds["queue"], 3)
	assert.Equal(t, kinds["group"], 1)
	assert.Equal(t, kinds["namespace"], 0)

	// ...and the entries it put on resources somebody else wrote, which is the other half
	// of what has to be taken back off.
	byWhat := map[string]string{}
	for _, g := range added {
		byWhat[strings.Join(g.Path, "/")+" "+g.Value] = g.On
	}
	assert.Equal(t, byWhat["queue/checkout_reserve/ops receive"], idOf(t, flat, "function", "reserve_stock"))
	assert.Equal(t, byWhat["queue/checkout_charge/ops send"], idOf(t, flat, "function", "reserve_stock"))
	assert.Equal(t, byWhat["queue/checkout_charge/ops receive"], idOf(t, flat, "function", "charge_card"))

	// A resource that becomes nothing has no record and answers with nothing.
	none, alsoNone := schema.Became(flat, idOf(t, flat, "function", "checkout"))
	assert.Equal(t, len(none), 0)
	assert.Equal(t, len(alsoNone), 0)
}

// WHAT A LOWERING BECOMES IS A GROUP, AND A GROUP IS NOT A NAMESPACE. It behaves like
// one — indexed, addressable, a bare reference inside it resolving outward — because that
// behaviour is entirely the SHAPE, which it shares. What it does not share is the name:
// an authored `/shop/eu` is a line somebody drew and `checkout` is the inside of a
// workflow, and anything that calls the second one a namespace is telling a reader the
// author wrote something they did not.
//
// The other half is that nobody can write one. The kind is real to the compiler and to
// the index; it is not in the document an editor validates, not offered as something to
// create, and refused if it turns up in a repository.
func TestWhatALoweringBecomesIsAGroupAndNobodyWritesOne(t *testing.T) {
	obj := compileFor(t, schema.WithLocal("../fixtures/basic"))

	kinds := schema.ScopeKinds(obj.Flat())
	assert.Equal(t, kinds["checkout"], "group", "what workflow/checkout became: %v", kinds)
	assert.Equal(t, kinds["support"], "group", "what agent/support became: %v", kinds)
	assert.Equal(t, kinds["shop"], "namespace", "an authored scope changed kind: %v", kinds)

	// tcc's own answer about the vocabulary, which is where every consumer gets it: the
	// group is a container like the namespace, and unlike it, nobody authors one.
	byGroup := map[string]schema.Kind{}
	s, err := schema.NewSession(afero.NewMemMapFs(), "/")
	assert.NilError(t, err)
	for _, k := range s.Kinds() {
		byGroup[k.Group] = k
	}
	assert.Assert(t, byGroup["group"].Container, "the produced scope is not a container: %v", byGroup["group"])
	assert.Assert(t, byGroup["group"].NotAuthored, "nothing says a group is not a person's to write")
	assert.Assert(t, !byGroup["namespace"].NotAuthored, "an authored namespace was marked unwritable")

	// ...so it is not in the document an editor validates against either.
	js, err := jsonschema.GenerateJSONSchema(schema.GenerationRoot(), jsonschema.JSONSchemaOptions{})
	assert.NilError(t, err)
	var doc struct {
		Properties map[string]any `json:"properties"`
	}
	assert.NilError(t, json.Unmarshal(js, &doc))
	_, offered := doc.Properties["group"]
	assert.Assert(t, !offered, "the JSON Schema offers a kind nobody may author")
	_, namespace := doc.Properties["namespace"]
	assert.Assert(t, namespace, "the authored scope went missing from the schema")
}

// A LOWERABLE RESOURCE IN A NAMESPACE produces into a scope of its own, INSIDE that
// one. Scope arithmetic is where a path builder goes wrong, and it goes wrong silently: a
// resource placed one level up is a resource in the wrong namespace, and it compiles.
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

	// the scope the workflow became sits inside the one it was written in, and its
	// queues are in that — never one level up, which is where a path builder puts them
	assert.Assert(t, scopeExists(obj.Flat(), "shop", "ship"))
	assert.Assert(t, !scopeExists(obj.Flat(), "", "ship"), "the scope was placed at the root")

	for _, r := range schema.Resources(rootOf(obj.Flat())) {
		if r.Kind == "queue" && r.Name == "ship_pack" {
			assert.Equal(t, r.Scope, "shop/ship")
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
			"      on_error: again\n"), 0o644))

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

// scopeExists reports whether a scope of this name sits at this path. Scopes are the one
// thing Resources() does not list — it lists what is inside them — so a question about a
// scope is asked of Scopes(), which is the vocabulary's own answer and knows there is
// more than one KIND of scope.
func scopeExists(flat map[string]any, at, name string) bool {
	want := strings.TrimPrefix(at+"/"+name, "/")
	_, ok := schema.ScopeKinds(flat)[want]
	return ok
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
		assert.Assert(t, !(g.On == fnID && g.Value == "receive"),
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

// A RESOURCE TWO SCOPES DEEP IS STILL A RESOURCE. Scopes nest — v2 allows eight levels —
// and the session read only the shapes that fit in four segments, so anything inside a
// namespace inside a namespace answered "too deep". Became() accepted the address that
// Get and Serialize rejected.
func TestSessionAddressesNestedScopes(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	s, err := schema.AdoptSession(fs)
	assert.NilError(t, err)

	deep := []string{"namespace", "shop", "namespace", "eu", "function", "relay"}

	id, err := s.Get(deep, []string{"id"})
	assert.NilError(t, err, "a function two namespaces down is not readable")
	assert.Assert(t, id != "")

	// the address a caller builds by kind, and the one a file path maps back to, agree
	built, err := s.Address("function", "relay", "shop/eu")
	assert.NilError(t, err)
	assert.DeepEqual(t, built, deep)

	back, ok := s.ResourceAt("/namespace/shop/namespace/eu/function/relay.yaml")
	assert.Assert(t, ok, "the file a decompile writes maps back to no address")
	assert.DeepEqual(t, back, deep)

	// ...and the scope itself is addressable, which is where its own fields live
	scope, ok := s.ResourceAt("/namespace/shop/namespace/eu/scope.yaml")
	assert.Assert(t, ok)
	assert.DeepEqual(t, scope, []string{"namespace", "shop", "namespace", "eu"})
}

// A NAME A LOWERING TAKES IS NOT A NAME AN AUTHOR CAN LOSE. A workflow is a scope for
// what it becomes, so a queue somebody wrote at the root and a queue the compiler wrote
// inside `checkout` are two resources that never meet — the collision that used to be a
// refused compile is not a collision at all any more.
//
// The scope's own name cannot collide with a namespace either, now that what a lowering
// produces is a `group`: an authored `namespace/checkout` and the group `checkout` are
// two different kinds and never meet. What is refused instead is writing a `group` at
// all — the kind is the compiler's, and a document of it in a repository is refused where
// it was written rather than fought over afterwards.
func TestAProducedNameCannotTakeAnAuthoredOne(t *testing.T) {
	free := afero.NewMemMapFs()
	assert.NilError(t, copyTree(free, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(free, "/queue/checkout_reserve.yaml", []byte(
		"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ4Aa\n"+
			"description: a queue a person wrote, with the name a workflow also produces\n"), 0o644))

	c, err := schema.New(schema.WithVirtual(free, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err, "an authored queue was refused for a name it no longer shares")

	scopes := map[string]bool{}
	for _, r := range schema.Resources(rootOf(obj.Flat())) {
		if r.Kind == "queue" && r.Name == "checkout_reserve" {
			scopes[r.Scope] = true
		}
	}
	assert.Assert(t, scopes[""] && scopes["checkout"],
		"both queues called checkout_reserve should exist, in different scopes: %v", scopes)

	// THE SCOPE'S NAME IS STILL TAKEN, and being a different KIND of scope does not
	// untake it. Every name resolution in tcc walks a chain of scope NAMES — the by-name
	// index is keyed by them and so is the outward walk a bare reference does — so an
	// authored `namespace/checkout` and a produced `group/checkout` are one name with two
	// directories behind it, and which of them a reference means is not a question the
	// compiler should be answering by accident.
	taken := afero.NewMemMapFs()
	assert.NilError(t, copyTree(taken, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(taken, "/namespace/checkout/scope.yaml", []byte(
		"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ4Ab\n"), 0o644))

	c, err = schema.New(schema.WithVirtual(taken, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.ErrorContains(t, err, "namespace/checkout is written here AND is the scope workflow/checkout becomes")

	// WRITING THE COMPILER'S KIND IS REFUSED WHERE IT WAS WRITTEN, whatever it is called.
	// The key is in the schema, so nothing else would have said no: the document would
	// have compiled into exactly the shape a lowering produces.
	written := afero.NewMemMapFs()
	assert.NilError(t, copyTree(written, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(written, "/group/mine/scope.yaml", []byte(
		"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ4Ac\n"), 0o644))

	c, err = schema.New(schema.WithVirtual(written, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.ErrorContains(t, err, "group/mine is written here, and group is a kind only a lowering produces")
}

// A REFERENCE BETWEEN TWO PRODUCED RESOURCES RESOLVES BY ITSELF. A queue carries a
// `dead_letter` that is a Ref like any other, and the queue it names is one the same
// lowering made — so it resolves the way a bare name always resolves: in the scope it is
// written in, which is the scope the lowering produced. Nothing needed a second shape to
// dodge the index, which was the first answer and the wrong one.
func TestAProducedReferenceResolvesInTheProducedScope(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	flat := obj.Flat()

	failed := idOf(t, flat, "queue", "checkout_failed")
	for _, name := range []string{"checkout_reserve", "checkout_charge"} {
		q := resource(t, flat, "queue", name)
		dead, _ := q["dead_letter"].(map[string]any)
		assert.Equal(t, dead["name"], failed,
			"%s's dead-letter reference did not resolve to the queue beside it: %v", name, dead)
	}

	// AND WHAT REACHES IN SAYS WHERE. The step's function was written outside that scope,
	// so its entry names the queue and the namespace it is in — an author writing the
	// same thing by hand would have had to write both.
	fn := resource(t, flat, "function", "reserve_stock")
	queues, _ := fn["queue"].(map[string]any)
	entry, _ := queues["checkout_reserve"].(map[string]any)
	assert.Assert(t, entry != nil, "%v", queues)
	assert.Equal(t, entry["namespace"], "/checkout")
	assert.DeepEqual(t, entry["ops"], []string{"receive"})
}
