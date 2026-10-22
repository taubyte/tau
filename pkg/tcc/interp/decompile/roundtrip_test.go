package decompile

import (
	"context"
	"errors"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp"
	"github.com/taubyte/tau/pkg/tcc/object"
	"gotest.tools/v3/assert"
)

func pipelineRoot() *engine.Node {
	return engine.Root(
		[]*engine.Attribute{engine.String("id")},
		engine.DefineGroup("job", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required()),
			engine.String("name"),
		}, engine.Resource("job", "Job", "Job", "job"))),
		engine.DefineGroup("pipeline", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required()),
			engine.String("name"),
		},
			engine.Resource("pipeline", "Pipeline", "Pipeline", "pipeline"),
			interp.Lowers(interp.Into("job", interp.Named(interp.Concat(interp.Attr("name"), interp.Text("-run"))))),
		)),
	).With(engine.Leaf("project"), engine.Identity("id", "name"))
}

func compiledPipeline(t *testing.T) Object {
	t.Helper()
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml", []byte("id: root\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/pipeline/build.yaml", []byte("id: QmP\n"), 0o644))
	root := pipelineRoot()
	c, err := interp.New(engine.SchemaDefinition(root), root, interp.WithVirtual(fs, "/"),
		interp.WithLayout(interp.Layout{Resource: "{kind}/{name}.yaml"}))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	return obj
}

func decompileTo(t *testing.T, obj Object, opts ...Option) afero.Fs {
	t.Helper()
	out := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(out, "/project.yaml", nil, 0o644))
	root := pipelineRoot()
	d, err := New(engine.SchemaDefinition(root), root, append([]Option{WithVirtual(out, "/")}, opts...)...)
	assert.NilError(t, err)
	assert.NilError(t, d.Decompile(obj))
	return out
}

func TestDecompileWritesWhatWasAuthored(t *testing.T) {
	out := decompileTo(t, compiledPipeline(t))
	body, err := afero.ReadFile(out, "/pipeline/build.yaml")
	assert.NilError(t, err)
	assert.Equal(t, string(body), "id: QmP\n")
	jobs, _ := afero.ReadDir(out, "/job")
	assert.Equal(t, len(jobs), 0, "a produced resource is not written back")
}

func TestDecompileAsLoweredWritesWhatRuns(t *testing.T) {
	out := decompileTo(t, compiledPipeline(t), WithAsLowered())
	jobs, err := afero.ReadDir(out, "/job")
	assert.NilError(t, err)
	assert.Equal(t, len(jobs), 1)
	assert.Equal(t, jobs[0].Name(), "build-run.yaml")
}

func TestValuesReadsAListInEitherShape(t *testing.T) {
	o := object.New[object.Refrence]()
	o.Set("a", []string{"x"})
	o.Set("b", []any{"x", 1, "y"})
	o.Set("c", "x")
	assert.DeepEqual(t, values(o, "a"), []string{"x"})
	assert.DeepEqual(t, values(o, "b"), []string{"x", "y"})
	assert.Assert(t, values(o, "c") == nil)
}

func TestRevokeLeavesWhatItDidNotWrite(t *testing.T) {
	res := object.New[object.Refrence]()
	block, err := res.CreatePath("access")
	assert.NilError(t, err)
	block.Set("mode", "edited")

	revoke(res, interp.Granted{Path: []string{"access", "mode"}, Value: "granted"})
	assert.Equal(t, block.Get("mode"), "edited", "a value somebody changed is theirs now")

	revoke(res, interp.Granted{Path: []string{"missing", "mode"}, Value: "granted"})
	revoke(res, interp.Granted{})
	assert.DeepEqual(t, res.Flat(), map[string]any{"access": map[string]any{"mode": "edited"}})

	revoke(res, interp.Granted{Path: []string{"access", "mode"}, Value: "edited"})
	assert.Equal(t, len(res.Flat()), 0, "the emptied block goes with the value")
}

func TestAFailingOptionFailsNew(t *testing.T) {
	root := pipelineRoot()
	_, err := New(engine.SchemaDefinition(root), root, func(*Decompiler) error { return errBoom })
	assert.ErrorIs(t, err, errBoom)
}

var errBoom = errors.New("boom")
