package schema_test

import (
	"context"
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
	c0, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c0.Compile(context.Background())
	assert.NilError(t, err)

	out := afero.NewMemMapFs()
	d, err := schema.NewDecompiler(decompile.WithVirtual(out, "/"))
	assert.NilError(t, err)
	assert.NilError(t, d.Decompile(obj))

	back, err := afero.ReadFile(out, "/workflow/checkout.yaml")
	assert.NilError(t, err, "the workflow has to come back a workflow")

	// A list of objects survives as a list of objects, in order — the construct the
	// steps are written in, and the one a round trip is most likely to flatten.
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/workflow/checkout.yaml", back, 0o644))
	// the steps' functions do not exist in this cut-down project; only the shape matters
	c, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	again, _, err := c.Compile(context.Background())
	assert.NilError(t, err, "what came back must compile: %s", back)

	wf := only(t, resources(t, again.Flat(), "workflow"))
	steps, ok := wf["steps"].([]any)
	assert.Assert(t, ok, "steps came back as %T, not a list: %s", wf["steps"], back)
	assert.Equal(t, len(steps), 2)
	assert.Equal(t, steps[0].(map[string]any)["name"], "reserve", "and in the order they were written")
	assert.Equal(t, steps[1].(map[string]any)["name"], "charge")
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

	plan := schema.LoweringPlan(obj.Flat())
	assert.Assert(t, len(plan) > 0, "the fixture has to lower something for this to mean anything")

	// All of them together, the way emission would write them: a derived resource may
	// reference another derived resource, and each alone would not resolve.
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\n"), 0o644))
	for _, d := range plan {
		assert.Assert(t, d.Doc != "", "%s/%s has no document", d.Kind, d.Name)
		assert.NilError(t, afero.WriteFile(fs, "/"+d.Kind+"/"+d.Name+".yaml", []byte(d.Doc), 0o644))
	}

	back, err := schema.New(schema.WithVirtual(fs, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = back.Compile(context.Background())
	assert.NilError(t, err, "what a lowering produces must be a project that compiles")
}
