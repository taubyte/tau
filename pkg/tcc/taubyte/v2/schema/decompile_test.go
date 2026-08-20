package schema_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/interp/decompile"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// COMPILATION IS REVERSIBLE, and that is a property of the whole vocabulary rather than
// a feature of one kind: a compiled project goes back to the yaml it came from. Every
// construct has to survive the trip, which is the thing to hold on to while adding one.
//
// The workflow is the case worth guarding. It LOWERS — it becomes queues — and the day
// those queues are written into the compiled object, this test is what says whether the
// project still decompiles as a workflow or as three queue documents and a workflow
// that has quietly become something else. Today the lowering is a plan and nothing is
// emitted, so this passes; that is exactly why it is worth pinning now.
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
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".yaml") {
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
// Today the plan is not emitted, so this passes for a boring reason. It is here for the
// day it is: the failure it catches is a project that compiles, deploys, and decompiles
// into somebody else's config.
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
