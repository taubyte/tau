package schema_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/interp"
	"github.com/taubyte/tau/pkg/tcc/interp/decompile"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// COMPILATION IS REVERSIBLE, and that is a property of the whole vocabulary rather than
// a feature of one kind: a compiled project goes back to the yaml it came from. Every
// construct has to survive the trip, which is the thing to hold on to while adding one.
//
// The workflow is the case worth guarding. It LOWERS — it becomes queues, and those
// queues ARE in the compiled object — so this test is what says whether the project
// still decompiles as a workflow or as three queue documents and a workflow that has
// quietly become something else.
func TestProjectDecompilesBack(t *testing.T) {
	// TWO compiles of the same project: decompiling MUTATES the object it is handed
	// (the reverse pipeline is transforms, like the forward one), so comparing against
	// the object that was decompiled compares it with itself after the fact.
	want := compileFor(t, schema.WithLocal("../fixtures/basic")).Flat()

	out := decompileTo(t, compileFor(t, schema.WithLocal("../fixtures/basic")))
	got := compileFor(t, schema.WithVirtual(out, "/")).Flat()

	// THE WHOLE OBJECT, not a spot check: a round trip that loses one field of one kind
	// is a round trip that loses data, and the only way to know is to compare all of it.
	// It is also the assertion with nothing written into it — no expected value here is
	// a fact about the fixture that could be updated to match a bug.
	assert.DeepEqual(t, want, got)
}

// EVERY FILE COMES BACK, and no file is invented. A resource that decompiled to nothing
// would vanish from the repository the first time somebody wrote the project back.
func TestDecompileWritesEveryDocument(t *testing.T) {
	first := compileFor(t, schema.WithLocal("../fixtures/basic"))
	out := decompileTo(t, first)

	want := map[string]bool{}
	assert.NilError(t, filepath.Walk("../fixtures/basic", func(path string, info os.FileInfo, err error) error {
		// EVERY FILE, not every document: a zone names a file the compile reads into
		// the object, and a decompile that dropped it would hand back a zone pointing
		// at nothing.
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel("../fixtures/basic", path)
		want["/"+rel] = true
		return nil
	}))

	got := map[string]bool{}
	assert.NilError(t, afero.Walk(out, "/", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		got[path] = true
		return nil
	}))

	for path := range want {
		assert.Assert(t, got[path], "%s did not come back", path)
	}
	for path := range got {
		assert.Assert(t, want[path], "%s was invented by the decompiler", path)
	}
}

// A LOWERING MUST NOT LEAK INTO THE AUTHORED CONFIG. This is the one the whole shell
// rule exists for: a workflow becomes queues, and a project written back out has to
// contain the WORKFLOW — not the queues it becomes, and not a workflow that has quietly
// turned into something else.
//
// The queues are in the object, so this is a live guard rather than a placeholder: the
// failure it catches is a project that compiles, deploys, and decompiles into somebody
// else's config.
func TestLoweredResourcesDoNotDecompile(t *testing.T) {
	first := compileFor(t, schema.WithLocal("../fixtures/basic"))
	plan := schema.LoweringPlan(first.Flat())
	assert.Assert(t, len(plan) > 0, "the fixture must lower something for this to mean anything")

	out := decompileTo(t, first)
	for _, d := range plan {
		path := "/" + d.Kind + "/" + d.Name + ".yaml"
		_, err := out.Stat(path)
		assert.Assert(t, os.IsNotExist(err), "%s was written back as authored config", path)
	}

	// AND WHAT IT LOWERS FROM COMES BACK UNCHANGED — compared against the original
	// rather than against values written here, so this cannot be quietly updated to
	// match a round trip that lost something.
	want := compileFor(t, schema.WithLocal("../fixtures/basic")).Flat()
	got := compileFor(t, schema.WithVirtual(out, "/")).Flat()

	for _, kind := range []string{"workflow", "agent"} {
		assert.DeepEqual(t, resources(t, want, kind), resources(t, got, kind))
	}

	// a list of objects is still a list, in order — the construct a round trip is most
	// likely to flatten, checked as a shape rather than by its contents
	wf := only(t, resources(t, got, "workflow"))
	steps, ok := wf["steps"].([]any)
	assert.Assert(t, ok, "steps came back as %T, not a list", wf["steps"])
	assert.Assert(t, len(steps) > 1, "a one-step pipeline would not prove order survives")

	// and the plan computed from the round-tripped object is the same plan
	assert.DeepEqual(t, plan, schema.LoweringPlan(got))
}

// THE OTHER DEPTH. `WithAsLowered` writes everything, which is where a console's
// "generated, not editable" pane comes from — the same call the explorer's wasm build
// makes. Half way is what a repository holds; this is what a reader is shown.
//
// It is checked against the plan rather than against filenames written here, and the
// second half is the part that matters more than it looks: this tree does NOT compile,
// and it must not. Writing it back over a repository would give the workflow's queues an
// author, and the next compile refuses — a name a lowering takes has to be free. A reader
// who mistook the generated pane for config they could commit would find that out after
// committing it.
func TestDecompileAllTheWayWritesWhatWasLowered(t *testing.T) {
	first := compileFor(t, schema.WithLocal("../fixtures/basic"))
	plan := schema.LoweringPlan(first.Flat())
	assert.Assert(t, len(plan) > 0, "the fixture must lower something for this to mean anything")

	// Asked BEFORE the decompile: it rewrites the object it is handed — ids back to
	// names, the envelope unwrapped — so a question about the compiled shape has to be
	// asked while it is still the compiled shape.
	kinds := schema.ScopeKinds(first.Flat())

	out := afero.NewMemMapFs()
	d, err := schema.NewDecompiler(decompile.WithVirtual(out, "/"), schema.WithAsLowered())
	assert.NilError(t, err)
	assert.NilError(t, d.Decompile(first))

	for _, r := range plan {
		path := documentOf(r, kinds)
		_, err := out.Stat(path)
		assert.NilError(t, err, "%s is what a lowering produced and was not written", path)
	}

	// and it is written INSIDE THE SOURCE, under the source's own kind. A workflow that
	// has lowered IS the scope its queues are in, so the directory a reader opens is
	// `workflow/checkout` — not a manufactured kind beside it, and not a namespace.
	for _, name := range []string{"checkout_reserve", "checkout_charge", "checkout_failed"} {
		_, err := out.Stat("/workflow/checkout/sync/" + name + ".yaml")
		assert.NilError(t, err)
	}
	// The workflow's own fields moved into the container's leaf, which is what taking
	// the container shape MEANS: one document for the fields, directories for what it
	// holds — the same layout v1's application has.
	_, err = out.Stat("/workflow/checkout/meta.yaml")
	assert.NilError(t, err, "the workflow that became a container has no document of its own")
	_, err = out.Stat("/workflow/checkout.yaml")
	assert.Assert(t, err != nil, "the flat authored document was written beside the container it became")
	_, err = out.Stat("/namespace/checkout/meta.yaml")
	assert.Assert(t, err != nil, "what a workflow became was written as a namespace")
	_, err = out.Stat("/group/checkout/meta.yaml")
	assert.Assert(t, err != nil, "what a workflow became was written as a kind of its own")

	// AND IT STILL DOES NOT COMPILE, which is the half that matters. Nothing guards it
	// any more — there is no kind to refuse — and nothing has to: the authored schema
	// says a workflow is one flat document, so a directory where that document belongs is
	// refused by the shape itself, at the file, without a rule written for the occasion.
	c, err := schema.New(schema.WithVirtual(out, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.ErrorContains(t, err, "required attribute 'id'",
		"the generated depth compiled, which would make it look like a repository")
}

// documentOf is where a planned resource's document lives, built the way any consumer
// has to build it: off the layout this vocabulary states, never off a rule spelled out
// again here. A produced SCOPE is a directory with the leaf document inside it; anything
// else is one file, under the scope directories it sits in.
//
// A SCOPE DIRECTORY IS NAMED AFTER ITS KIND, and a scope PATH is names, so the two are
// not the same string: `checkout` is a group and `shop` is a namespace, and `kinds` — from
// schema.ScopeKinds — is how a consumer asks which without keeping a list of its own.
func documentOf(r interp.Derived, kinds map[string]string) string {
	l := schema.LayoutOf()
	dir := func(at, name string) string {
		return "/" + strings.NewReplacer("{kind}", kinds[at], "{name}", name).Replace(l.Scope)
	}
	path, at := "", ""
	for _, level := range strings.Split(r.Scope, "/") {
		if level == "" {
			continue
		}
		at = strings.TrimPrefix(at+"/"+level, "/")
		path += dir(at, level)
	}
	return path + "/" + strings.NewReplacer("{kind}", r.Kind, "{name}", r.Name).Replace(l.Resource)
}

func compileFor(t *testing.T, opt schema.Option) schema.Object {
	t.Helper()
	c, err := schema.New(opt, schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	return obj
}

func decompileTo(t *testing.T, obj schema.Object) afero.Fs {
	t.Helper()
	out := afero.NewMemMapFs()
	d, err := schema.NewDecompiler(decompile.WithVirtual(out, "/"))
	assert.NilError(t, err)
	assert.NilError(t, d.Decompile(obj))
	return out
}

// WHAT A LOWERING PRODUCES HAS TO BE AUTHORABLE. The document each derived resource
// would be is not a description of it — it is config, and the day it is emitted it has
// to compile. Generated config that would be rejected if a person wrote it is worse
// than none: it looks like an answer.
//
// This is the test that caught two real ones. A field's COMPILED key is not its authored
// path, so `execution/call` — one attribute, three lines of yaml — was written flat and
// failed with "unknown key". And a scalar copied out of a compiled resource carries its
// WIRE form, so a vector's `size` came out as 2000000000, which does not compile at all
// because Bytes requires a unit.
func TestGeneratedDocumentsCompile(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	// COMPILING IS WHAT PROVES IT. A lowering produces resources, and they go through the
	// same pipeline as everything else — so a lowering that produces something a kind
	// would reject is a compile error, here, rather than a document that reads fine and
	// deploys to nothing. There is nothing left to check separately.
	plan := schema.LoweringPlan(obj.Flat())
	assert.Assert(t, len(plan) > 0, "the fixture has to lower something for this to mean anything")
	for _, d := range plan {
		assert.Assert(t, d.ID != "", "%s/%s has no id to be stored under", d.Kind, d.Name)
		assert.Assert(t, hasResource(obj.Flat(), d.Kind, d.Name),
			"%s/%s is planned and is not in the compiled object", d.Kind, d.Name)
	}
}
