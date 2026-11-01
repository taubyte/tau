package schema_test

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp"
	"github.com/taubyte/tau/pkg/tcc/jsonschema"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

func TestCompileHoldsWhatRuns(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)

	authored, _, err := c.Authored(context.Background())
	assert.NilError(t, err)
	assert.Assert(t, !hasResource(authored.Flat(), "sync", "checkout_reserve"),
		"what was WRITTEN has only the queues somebody wrote")

	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	flat := obj.Flat()

	for _, name := range []string{"checkout_reserve", "checkout_charge", "checkout_failed"} {
		assert.Assert(t, hasResource(flat, "sync", name), "sync/%s is not in the object", name)
	}
	// the agent's loop and its memory, from the other lowering in the vocabulary
	assert.Assert(t, hasResource(flat, "function", "support_infer"))
	assert.Assert(t, hasResource(flat, "database", "support_memory"))

	// A PRODUCED RESOURCE IS AN ORDINARY RESOURCE. It went through the same pipeline, so
	// `max-attempts` is a number and its dead-letter reference resolved to an id like
	// anyone else's — which is the whole claim: this is config, not a picture of it.
	q := resource(t, flat, "sync", "checkout_reserve")
	assert.Equal(t, nested(q, "queue", "max_attempts"), 4)
	dead, _ := nested(q, "queue", "dead_letter").(map[string]any)
	name, _ := dead["name"].(string)
	assert.Assert(t, name != "" && name != "checkout_failed",
		"the reference resolved like any other: %v", dead)

	// AND THE ACCESS IT IMPLIES IS ON THE RESOURCE THAT DECLARES IT — the entry an author
	// would have written, on the step's DRIVER, naming the queue it receives from. The
	// driver is produced in the same scope, so the whole pipeline is inside the box.
	drv := resource(t, flat, "function", "checkout_reserve")
	queues, _ := drv["sync"].(map[string]any)
	assert.Assert(t, queues != nil, "the driver declares no queue access at all: %v", drv)
	entry, _ := queues["checkout_reserve"].(map[string]any)
	assert.Assert(t, entry != nil, "nothing says this driver receives from its step's queue: %v", queues)
	assert.DeepEqual(t, entry["ops"], []string{"receive"})

	// The author's function knows no queues: the driver invokes it.
	fn := resource(t, flat, "function", "reserve_stock")
	_, hasQueues := fn["sync"]
	assert.Assert(t, !hasQueues, "the step's function carries queue access it should know nothing about: %v", fn)
}

func TestCompileLeavesTheProjectAlone(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.NilError(t, err)

	again, _, err := c.Authored(context.Background())
	assert.NilError(t, err)
	assert.Assert(t, !hasResource(again.Flat(), "sync", "checkout_reserve"),
		"reading the project again found a queue nobody wrote: the view leaked onto disk")
}

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
	assert.Equal(t, len(made), 7, "a queue and a driver per step, and the one they fail into")

	kinds := map[string]int{}
	for _, id := range made {
		assert.Equal(t, rec.Resources[id].From, workflow)
		assert.Assert(t, rec.Produced(id))
		kinds[rec.Resources[id].Kind]++
	}
	assert.Equal(t, kinds["sync"], 4)
	assert.Equal(t, kinds["function"], 3, "a driver per step, whatever the step does")
	assert.Equal(t, kinds["stream"], 0, "nothing authored asked for a stream")
	assert.Equal(t, kinds["group"], 0)
	assert.Equal(t, kinds["namespace"], 0)

	// ...and the entries it put on resources somebody else wrote, which is the other half
	// of what has to be taken back off.
	byWhat := map[string]string{}
	for _, g := range added {
		byWhat[strings.Join(g.Path, "/")+" "+g.Value] = g.On
	}
	assert.Equal(t, byWhat["sync/checkout_reserve/ops receive"], idOf(t, flat, "function", "checkout_reserve"))
	assert.Equal(t, byWhat["sync/checkout_settle/ops send"], idOf(t, flat, "function", "checkout_reserve"))
	assert.Equal(t, byWhat["sync/checkout_charge/ops receive"], idOf(t, flat, "function", "checkout_charge"))
	// and the one write that lands on something the AUTHOR wrote: where a start goes
	assert.Equal(t, byWhat["entry/start/name checkout_reserve"], idOf(t, flat, "workflow", "checkout"))

	// A resource that becomes nothing has no record and answers with nothing.
	none, alsoNone := schema.Became(flat, idOf(t, flat, "function", "checkout"))
	assert.Equal(t, len(none), 0)
	assert.Equal(t, len(alsoNone), 0)
}

func TestWhatALoweringBecomesKeepsItsKindAndTakesTheContainerRole(t *testing.T) {
	obj := compileFor(t, schema.WithLocal("../fixtures/basic"))

	kinds := schema.ScopeKinds(obj.Flat())
	assert.Equal(t, kinds["checkout"], "workflow", "what workflow/checkout became: %v", kinds)
	assert.Equal(t, kinds["support"], "agent", "what agent/support became: %v", kinds)
	assert.Equal(t, kinds["shop"], "namespace", "an authored scope changed kind: %v", kinds)

	byGroup := map[string]schema.Kind{}
	s, err := schema.NewSession(afero.NewMemMapFs(), "/")
	assert.NilError(t, err)
	for _, k := range s.Kinds() {
		byGroup[k.Group] = k
	}
	assert.Assert(t, !byGroup["workflow"].Container, "the authored workflow leaked its compiled shape")
	assert.Assert(t, !byGroup["agent"].Container, "the authored agent leaked its compiled shape")

	compiled := map[string]engine.GroupInfo{}
	for _, kind := range engine.Groups(schema.KindRoot()) {
		compiled[kind.Group] = kind
	}
	assert.Assert(t, compiled["workflow"].Container)
	assert.Assert(t, compiled["agent"].Container)
	_, invented := compiled["group"]
	assert.Assert(t, !invented, "group became a vocabulary kind again")

	// ...so it is not in the document an editor validates against either.
	js, err := jsonschema.GenerateJSONSchema(schema.GenerationRoot(), jsonschema.JSONSchemaOptions{})
	assert.NilError(t, err)
	var doc struct {
		Properties map[string]any `json:"properties"`
	}
	assert.NilError(t, json.Unmarshal(js, &doc))
	_, offered := doc.Properties["workflow"]
	assert.Assert(t, offered, "the JSON Schema lost the authored workflow kind")
	_, group := doc.Properties["group"]
	assert.Assert(t, !group, "the JSON Schema invented a group kind")
	_, namespace := doc.Properties["namespace"]
	assert.Assert(t, namespace, "the authored scope went missing from the schema")
}

func TestAContainerKeepsWhatItReachesBesideWhatItHolds(t *testing.T) {
	obj := compileFor(t, schema.WithLocal("../fixtures/basic"))
	flat := obj.Flat()

	agent := resource(t, flat, "agent", "support")
	functions, _ := agent["function"].(map[string]any)
	assert.Assert(t, functions != nil, "the agent declares no function access at all: %v", agent)

	tool, _ := functions["checkout"].(map[string]any)
	assert.Assert(t, tool != nil, "the agent lost the tool it declared: %v", functions)
	assert.DeepEqual(t, tool["ops"], []string{"invoke"})

	// ...and the loop it became is under the same key, as a resource with a name
	loop := idOf(t, flat, "function", "support_infer")
	held, _ := functions[loop].(map[string]any)
	assert.Assert(t, held != nil, "the loop is not held inside the agent: %v", functions)
	assert.Equal(t, held["name"], "support_infer")

	// Nothing that walks the object may report the tool as something the agent holds.
	for _, r := range schema.Resources(rootOf(flat)) {
		assert.Assert(t, !(r.Scope == "support" && r.Name == "checkout"),
			"an access entry was reported as a resource inside the agent")
	}

	// The other access blocks are untouched: they share no key with anything produced.
	for key, target := range map[string]string{"database": "orders", "cache": "orders", "storage": "receipts"} {
		block, _ := agent[key].(map[string]any)
		assert.Assert(t, block != nil && block[target] != nil, "%s/%s went missing: %v", key, target, agent[key])
	}
}

func TestLoweringInsideANamespace(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/namespace/shop/workflow/ship.yaml", []byte(
		"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zq\n"+
			"description: pack it, then hand it over\n"+
			"start: pack\n"+
			"steps:\n"+
			"    - name: pack\n"+
			"      call:\n"+
			"          function: notify\n"+
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
		if r.Kind == "sync" && r.Name == "ship_pack" {
			assert.Equal(t, r.Scope, "shop/ship")
			return
		}
	}
	t.Fatal("sync/ship_pack is not in the compiled object")
}

func TestTwoLoweringsWriteIntoOneProducedResource(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/workflow/retry.yaml", []byte(
		"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zr\n"+
			"description: two steps, so the first driver both receives and sends\n"+
			"start: first\n"+
			"steps:\n"+
			"    - name: first\n"+
			"      call:\n"+
			"          function: charge_card\n"+
			"      attempts: 2\n"+
			"    - name: second\n"+
			"      call:\n"+
			"          function: reserve_stock\n"+
			"      attempts: 1\n"), 0o644))

	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	drv := resource(t, obj.Flat(), "function", "retry_first")
	queues, _ := drv["sync"].(map[string]any)
	assert.Assert(t, queues != nil, "the driver declares no queue access at all: %v", drv)

	mine, _ := queues["retry_first"].(map[string]any)
	assert.Assert(t, mine != nil, "the queue's own lowering did not say who consumes it: %v", queues)
	assert.DeepEqual(t, mine["ops"], []string{"receive"})

	next, _ := queues["retry_second"].(map[string]any)
	assert.Assert(t, next != nil, "the driver's own lowering did not say where work goes next: %v", queues)
	assert.DeepEqual(t, next["ops"], []string{"send"})

	fns, _ := drv["function"].(map[string]any)
	ran, _ := fns["charge_card"].(map[string]any)
	assert.Assert(t, ran != nil, "and nothing says it invokes the step's function: %v", fns)
	assert.DeepEqual(t, ran["ops"], []string{"invoke"})
}

func TestAnAuthoredValueIsNotReplaced(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/workflow/hand.yaml", []byte(
		"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zs\n"+
			"description: one step, and an entry the author filled in themselves\n"+
			"start: only\n"+
			"entry:\n"+
			"    start:\n"+
			"        name: somewhere_else\n"+
			"steps:\n"+
			"    - name: only\n"+
			"      call:\n"+
			"          function: charge_card\n"+
			"      attempts: 1\n"), 0o644))

	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	wf := resource(t, obj.Flat(), "workflow", "hand")
	entry, _ := wf["entry"].(map[string]any)
	start, _ := entry["start"].(map[string]any)
	assert.Assert(t, start != nil, "the record is not there at all: %v", wf["entry"])
	assert.Equal(t, start["name"], "somewhere_else", "the lowering overwrote what the author wrote")
	// and the fields they left empty are filled in beside it
	assert.Equal(t, start["kind"], "sync")
	assert.Equal(t, start["as"], "send")
}

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

func TestAnAuthoredOpIsNotRecordedAsGranted(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/workflow/hand.yaml", []byte(
		"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zs\n"+
			"description: one step, whose function already declared the receive\n"+
			"start: only\n"+
			"steps:\n"+
			"    - name: only\n"+
			"      call:\n"+
			"          function: relay_own\n"+
			"      attempts: 1\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/function/relay_own.yaml", []byte(
		"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zu\n"+
			"description: says for itself that it receives from the queue its step will make\n"+
			"runtime: wasm\n"+
			"execution:\n"+
			"    call: relay\n"+
			"    timeout: 5s\n"+
			"    memory: 16MB\n"+
			"sync:\n"+
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

func TestAnEntryLandsOnAProducedResource(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	loop := resource(t, obj.Flat(), "function", "support_infer")
	vectors, _ := loop["database"].(map[string]any)
	assert.Assert(t, vectors != nil, "the loop declares no vector access at all: %v", loop)
	entry, _ := vectors["support_memory"].(map[string]any)
	assert.Assert(t, entry != nil, "nothing says the loop searches the memory: %v", vectors)
	assert.DeepEqual(t, entry["ops"], []string{"search"})
}

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

func TestSessionAddressesNestedScopes(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	s, err := schema.AdoptSession(fs)
	assert.NilError(t, err)

	deep := []string{"namespace", "shop", "namespace", "local", "function", "relay"}

	id, err := s.Get(deep, []string{"id"})
	assert.NilError(t, err, "a function two namespaces down is not readable")
	assert.Assert(t, id != "")

	// the address a caller builds by kind, and the one a file path maps back to, agree
	built, err := s.Address("function", "relay", "shop/local")
	assert.NilError(t, err)
	assert.DeepEqual(t, built, deep)

	back, ok := s.ResourceAt("/namespace/shop/namespace/local/function/relay.yaml")
	assert.Assert(t, ok, "the file a decompile writes maps back to no address")
	assert.DeepEqual(t, back, deep)

	// ...and the scope itself is addressable, which is where its own fields live
	scope, ok := s.ResourceAt("/namespace/shop/namespace/local/meta.yaml")
	assert.Assert(t, ok)
	assert.DeepEqual(t, scope, []string{"namespace", "shop", "namespace", "local"})
}

func TestAProducedNameCannotTakeAnAuthoredOne(t *testing.T) {
	free := afero.NewMemMapFs()
	assert.NilError(t, copyTree(free, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(free, "/sync/checkout_reserve.yaml", []byte(
		"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ4Aa\n"+
			"description: a queue a person wrote, with the name a workflow also produces\n"+
			"queue:\n    consensus: raft\n"), 0o644))

	c, err := schema.New(schema.WithVirtual(free, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err, "an authored queue was refused for a name it no longer shares")

	scopes := map[string]bool{}
	for _, r := range schema.Resources(rootOf(obj.Flat())) {
		if r.Kind == "sync" && r.Name == "checkout_reserve" {
			scopes[r.Scope] = true
		}
	}
	assert.Assert(t, scopes[""] && scopes["checkout"],
		"both queues called checkout_reserve should exist, in different scopes: %v", scopes)

	// The scope's name is still taken: resolution walks scope names, so an authored
	// `namespace/checkout` and a lowered `workflow/checkout` would be one name.
	taken := afero.NewMemMapFs()
	assert.NilError(t, copyTree(taken, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(taken, "/namespace/checkout/meta.yaml", []byte(
		"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ4Ab\n"), 0o644))

	c, err = schema.New(schema.WithVirtual(taken, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.ErrorContains(t, err, "namespace/checkout is written here AND is the scope workflow/checkout becomes")

	// Writing into the compiler's space needs no rule: a directory where a flat
	// workflow document belongs does not load.
	written := afero.NewMemMapFs()
	assert.NilError(t, copyTree(written, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(written, "/workflow/checkout/queue/mine.yaml", []byte(
		"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ4Ac\n"+
			"description: a queue written by hand inside what a workflow becomes\n"), 0o644))

	c, err = schema.New(schema.WithVirtual(written, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.ErrorContains(t, err, "required attribute 'id'")
}

func TestAProducedReferenceResolvesInTheProducedScope(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	flat := obj.Flat()

	failed := idOf(t, flat, "sync", "checkout_failed")
	for _, name := range []string{"checkout_reserve", "checkout_charge"} {
		q := resource(t, flat, "sync", name)
		dead, _ := nested(q, "queue", "dead_letter").(map[string]any)
		assert.Equal(t, dead["name"], failed,
			"%s's dead-letter reference did not resolve to the queue beside it: %v", name, dead)
	}

	// AND WHAT REACHES IN SAYS WHERE. The driver and its queue are both in the produced
	// scope, so the entry names it — an author writing the same thing by hand would have
	// had to write the namespace too.
	drv := resource(t, flat, "function", "checkout_reserve")
	queues, _ := drv["sync"].(map[string]any)
	entry, _ := queues["checkout_reserve"].(map[string]any)
	assert.Assert(t, entry != nil, "%v", queues)
	assert.Equal(t, entry["namespace"], "/checkout")
	assert.DeepEqual(t, entry["ops"], []string{"receive"})

	// AND WHAT REACHES OUT DOES NOT. The function a step runs was written at the project
	// root, so the driver's entry for it carries NO namespace and the bare name resolves
	// outward — up out of the produced scope, the same walk every other bare name does.
	// Writing the produced scope there would have named a function that is not in it.
	fns, _ := drv["function"].(map[string]any)
	out, _ := fns["reserve_stock"].(map[string]any)
	assert.Assert(t, out != nil, "the driver does not say it invokes the step's function: %v", fns)
	_, saidWhere := out["namespace"]
	assert.Assert(t, !saidWhere, "the entry names a namespace the function is not in: %v", out)
	assert.DeepEqual(t, out["ops"], []string{"invoke"})
}

func TestADriverCanFindTheStepItCameFrom(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	flat := obj.Flat()

	rec := interp.LoweredOf(flat)
	prov, ok := rec.Resources[idOf(t, flat, "function", "checkout_settle")]
	assert.Assert(t, ok, "the driver is not recorded as produced at all")
	assert.Equal(t, prov.From, idOf(t, flat, "workflow", "checkout"), "it does not say what it came from")
	assert.Equal(t, prov.At, "steps/1", "it does not say where inside")

	// and walking that join reaches the step, with the field only the driver reads
	wf := resource(t, flat, "workflow", "checkout")
	steps, _ := wf["steps"].([]any)
	assert.Assert(t, len(steps) > 1)
	step, _ := steps[1].(map[string]any)
	assert.Equal(t, step["name"], "settle")
	assert.Equal(t, step["wait"], "30s", "the duration is not on the step: %v", step)

	// and it is not left as a string for somebody to parse: it lands on the queue's own
	// `delay`, which is a Duration, so tcc converts it and the pause is a property of
	// the thing that does the holding
	q := resource(t, flat, "sync", "checkout_settle")
	assert.Equal(t, nested(q, "queue", "delay"), int64(30000000000))
	before := resource(t, flat, "sync", "checkout_reserve")
	_, delayed := nestedOK(before, "queue", "delay")
	assert.Assert(t, !delayed, "a step that does not wait produced a delayed queue: %v", before)

	// the same join carries what a call is handed and where its answer goes
	first, _ := steps[0].(map[string]any)
	call, _ := first["call"].(map[string]any)
	assert.Equal(t, call["input"], "order.lines")
	assert.Equal(t, call["result"], "reservation")

	// and a step that waits invokes nothing: the driver holds no function grant at all
	drv := resource(t, flat, "function", "checkout_settle")
	_, invokes := drv["function"]
	assert.Assert(t, !invokes, "a wait step's driver was granted a call: %v", drv["function"])
}

func TestAnAgentCanHandOffToAnotherAgent(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/agent/triage.yaml", []byte(
		"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zv\n"+
			"description: hands the question to the agent that can answer it\n"+
			"model:\n    name: opus\n"+
			"instructions: route.md\n"+
			"limits:\n    steps: 4\n"+
			"agent:\n    support:\n        ops: [invoke]\n"), 0o644))

	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	// the entry survives as an entry, beside the loop the agent lowered into
	triage := resource(t, obj.Flat(), "agent", "triage")
	agents, _ := triage["agent"].(map[string]any)
	entry, _ := agents["support"].(map[string]any)
	assert.Assert(t, entry != nil, "the handoff is not there at all: %v", triage["agent"])
	assert.DeepEqual(t, entry["ops"], []string{"invoke"})

	// and it is a USE, so the graph draws it like any other
	byID := map[string]schema.Instance{}
	for _, r := range schema.Resources(obj.Flat()) {
		byID[r.ID] = r
	}
	var handoff bool
	for _, rel := range schema.Relations(obj.Flat()) {
		if byID[rel.From].Name == "triage" && byID[rel.To].Name == "support" {
			handoff = true
			assert.Equal(t, rel.Verb, "invoke")
		}
	}
	assert.Assert(t, handoff, "nothing draws the handoff")
}

func TestAnAgentCanUseAnMcpServer(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/mcp/orders.yaml", []byte(
		"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zw\n"+
			"description: the order tools, as an MCP server\n"+
			"function:\n    checkout:\n        ops: [invoke]\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/agent/shopper.yaml", []byte(
		"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zx\n"+
			"description: an agent whose tools come from an MCP server\n"+
			"model:\n    name: opus\n"+
			"instructions: orders.md\n"+
			"limits:\n    steps: 6\n"+
			"mcp:\n    orders:\n        ops: [list, call]\n"), 0o644))

	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	shopper := resource(t, obj.Flat(), "agent", "shopper")
	servers, _ := shopper["mcp"].(map[string]any)
	entry, _ := servers["orders"].(map[string]any)
	assert.Assert(t, entry != nil, "the agent declares no MCP access: %v", shopper["mcp"])
	assert.DeepEqual(t, entry["ops"], []string{"list", "call"})

	// AND AN ENTRY MAY BOUND ITSELF to some of the catalogue. `call` on a server means
	// any tool it advertises, and the set is the target's to change — so the day its
	// owner adds one, every accessor that asked for `call` silently gained it. A bound
	// narrows: a name nobody offers grants nothing, so it fails closed.
	assert.NilError(t, afero.WriteFile(fs, "/agent/bounded.yaml", []byte(
		"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ2Ab\n"+
			"model:\n    name: opus\n"+
			"limits:\n    steps: 6\n"+
			"mcp:\n    orders:\n        ops: [list, call]\n        tools: [get_order, refund]\n"), 0o644))
	c, err = schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err = c.Compile(context.Background())
	assert.NilError(t, err)
	bounded := resource(t, obj.Flat(), "agent", "bounded")
	servers, _ = bounded["mcp"].(map[string]any)
	entry, _ = servers["orders"].(map[string]any)
	assert.DeepEqual(t, entry["tools"], []string{"get_order", "refund"})

	// A PATTERN IS NOT A NAME. It would match tools that do not exist yet, which is the
	// widening the bound exists to stop, and it is a grammar in a string besides.
	assert.NilError(t, afero.WriteFile(fs, "/agent/bounded.yaml", []byte(
		"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ2Ab\n"+
			"model:\n    name: opus\n"+
			"limits:\n    steps: 6\n"+
			"mcp:\n    orders:\n        ops: [call]\n        tools: [\"get_*\"]\n"), 0o644))
	c, err = schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.ErrorContains(t, err, "never a pattern")
	assert.NilError(t, fs.Remove("/agent/bounded.yaml"))

	// and a verb the kind does not declare is refused, which is what Ops() is for
	assert.NilError(t, afero.WriteFile(fs, "/agent/shopper.yaml", []byte(
		"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zx\n"+
			"model:\n    name: opus\n"+
			"limits:\n    steps: 6\n"+
			"mcp:\n    orders:\n        ops: [subscribe]\n"), 0o644))
	c, err = schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.ErrorContains(t, err, "subscribe")
}

func TestAnAgentDeclaresNoConversationState(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	assert.NilError(t, afero.WriteFile(fs, "/agent/chat.yaml", []byte(
		"id: QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zy\n"+
			"description: an agent that would keep the conversation\n"+
			"model:\n    name: opus\n"+
			"instructions: help.md\n"+
			"sessions:\n    size: 64MB\n"+
			"limits:\n    steps: 6\n"), 0o644))

	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.ErrorContains(t, err, "unknown key 'sessions'")

	// WHAT THE FIXTURE'S AGENT BECOMES, whole. `agent/support` names an embedder and a
	// width, so it lowers into its loop and its vector index and nothing else.
	assert.NilError(t, fs.Remove("/agent/chat.yaml"))
	c, err = schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	var became []string
	for _, d := range schema.LoweringPlan(obj.Flat()) {
		if strings.HasPrefix(d.Of, "agent/") {
			became = append(became, d.Kind+"/"+d.Name)
		}
	}
	sort.Strings(became)
	assert.DeepEqual(t, became, []string{"database/support_memory", "function/support_infer"})
}
