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

func TestProjectDecompilesBack(t *testing.T) {
	// Two compiles: decompiling mutates the object it is handed.
	want := compileFor(t, schema.WithLocal("../fixtures/basic")).Flat()

	out := decompileTo(t, compileFor(t, schema.WithLocal("../fixtures/basic")))
	got := compileFor(t, schema.WithVirtual(out, "/")).Flat()

	// The whole object: a round trip losing one field of one kind loses data.
	assert.DeepEqual(t, want, got)
}

func TestDecompileWritesEveryDocument(t *testing.T) {
	first := compileFor(t, schema.WithLocal("../fixtures/basic"))
	out := decompileTo(t, first)

	want := map[string]bool{}
	assert.NilError(t, filepath.Walk("../fixtures/basic", func(path string, info os.FileInfo, err error) error {
		// Every file, not every document: a zone names a file the compile reads in.
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

	// What it lowers from comes back unchanged, compared against the original.
	want := compileFor(t, schema.WithLocal("../fixtures/basic")).Flat()
	got := compileFor(t, schema.WithVirtual(out, "/")).Flat()

	for _, kind := range []string{"workflow", "agent"} {
		assert.DeepEqual(t, resources(t, want, kind), resources(t, got, kind))
	}

	// A list of objects is still a list, in order.
	wf := only(t, resources(t, got, "workflow"))
	steps, ok := wf["steps"].([]any)
	assert.Assert(t, ok, "steps came back as %T, not a list", wf["steps"])
	assert.Assert(t, len(steps) > 1, "a one-step pipeline would not prove order survives")

	// and the plan computed from the round-tripped object is the same plan
	assert.DeepEqual(t, plan, schema.LoweringPlan(got))
}

func TestDecompileAllTheWayWritesWhatWasLowered(t *testing.T) {
	first := compileFor(t, schema.WithLocal("../fixtures/basic"))
	plan := schema.LoweringPlan(first.Flat())
	assert.Assert(t, len(plan) > 0, "the fixture must lower something for this to mean anything")

	// Asked before the decompile, which rewrites the object it is handed.
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

	// Written inside the source, under its own kind: a lowered workflow is the scope
	// its queues are in, so the directory is `workflow/checkout`.
	for _, name := range []string{"checkout_reserve", "checkout_charge", "checkout_failed"} {
		_, err := out.Stat("/workflow/checkout/sync/" + name + ".yaml")
		assert.NilError(t, err)
	}
	// The workflow's own fields moved into the container's leaf document.
	_, err = out.Stat("/workflow/checkout/meta.yaml")
	assert.NilError(t, err, "the workflow that became a container has no document of its own")
	_, err = out.Stat("/workflow/checkout.yaml")
	assert.Assert(t, err != nil, "the flat authored document was written beside the container it became")
	_, err = out.Stat("/namespace/checkout/meta.yaml")
	assert.Assert(t, err != nil, "what a workflow became was written as a namespace")
	_, err = out.Stat("/group/checkout/meta.yaml")
	assert.Assert(t, err != nil, "what a workflow became was written as a kind of its own")

	// And it does not compile: the authored schema says a workflow is one flat
	// document, so a directory there is refused by the shape itself.
	c, err := schema.New(schema.WithVirtual(out, "/"), schema.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.ErrorContains(t, err, "required attribute 'id'",
		"the generated depth compiled, which would make it look like a repository")
}

// documentOf is where a planned resource's document lives, built off the layout
// the vocabulary states: a produced scope is a directory holding its leaf
// document, anything else one file under its scope directories. A scope directory
// is named after its kind, which ScopeKinds answers.
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

func TestGeneratedDocumentsCompile(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	// Compiling is what proves it: a lowering producing something a kind rejects is a
	// compile error here.
	plan := schema.LoweringPlan(obj.Flat())
	assert.Assert(t, len(plan) > 0, "the fixture has to lower something for this to mean anything")
	for _, d := range plan {
		assert.Assert(t, d.ID != "", "%s/%s has no id to be stored under", d.Kind, d.Name)
		assert.Assert(t, hasResource(obj.Flat(), d.Kind, d.Name),
			"%s/%s is planned and is not in the compiled object", d.Kind, d.Name)
	}
}

func TestAnEntrypointRoundTripsOnEitherTier(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NilError(t, copyTree(fs, "../fixtures/basic", "/"))
	for path, doc := range map[string]string{
		"/function/exports_default.yaml": "id: " + cidFor("exports_default") + "\nruntime: wasm\nexecution:\n    call: default\n    timeout: 1s\n    memory: 1MB\n",
		"/function/module.yaml":          "id: " + cidFor("module") + "\nruntime: js\nexecution:\n    timeout: 1s\n    memory: 1MB\n",
	} {
		assert.NilError(t, afero.WriteFile(fs, path, []byte(doc), 0o644))
	}
	out := decompileTo(t, compileFor(t, schema.WithVirtual(fs, "/")))

	component, err := afero.ReadFile(out, "/function/exports_default.yaml")
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(string(component), "call: default"), "a component's export is the author's:\n%s", component)

	module, err := afero.ReadFile(out, "/function/module.yaml")
	assert.NilError(t, err)
	assert.Assert(t, !strings.Contains(string(module), "call:"), "a module's convention is not written back:\n%s", module)

	fns := resources(t, compileFor(t, schema.WithVirtual(out, "/")).Flat(), "function")
	assert.Equal(t, nested(namedResource(t, fns, "exports_default"), "execution", "call"), "default")
	assert.Equal(t, nested(namedResource(t, fns, "module"), "execution", "call"), "default")
}
