package interp

import (
	"context"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"gotest.tools/v3/assert"
)

func whereAttributes() []*engine.Attribute {
	return []*engine.Attribute{
		engine.String(engine.Key("where", "policy"), engine.Path("where", engine.Either("pinned", "cdn", "failover")), engine.Captures(), Inherited()),
		engine.StringSlice(engine.Key("where", "in"), engine.Path("where", engine.Either("pinned", "cdn")), Inherited()),
		engine.String(engine.Key("where", "main"), engine.Path("where", "failover", "main"), Inherited()),
	}
}

func inheritSchema() (engine.Schema, *engine.Node) {
	id := func(attrs ...*engine.Attribute) []*engine.Attribute {
		return append([]*engine.Attribute{engine.String("id", engine.Required(), engine.Generated(engine.GenCID))}, attrs...)
	}
	root := engine.Root(
		[]*engine.Attribute{engine.String("id")},
		engine.DefineGroup("job", engine.DefineIter(id(whereAttributes()...), engine.Resource("job", "Job", "Job", "job"))),
		engine.DefineGroup("note", engine.DefineIter(id(), engine.Resource("note", "Note", "Note", "note"))),
		engine.DefineGroup("pipeline", engine.DefineIter(append(id(engine.String("name"), engine.String("own")), whereAttributes()...),
			engine.Resource("pipeline", "Pipeline", "Pipeline", "pipeline"),
			Lowers(
				Into("job", Named(Concat(Attr("name"), Text("-run")))),
				Into("job", When("own"), Named(Concat(Attr("name"), Text("-own"))), Sets(engine.Key("where", "policy"), Attr("own"))),
				Into("note", Named(Concat(Attr("name"), Text("-note")))),
			),
		)),
	).With(engine.Leaf("project"), engine.Identity("id", "name"))
	return engine.SchemaDefinition(root), root
}

func inheritPlan(t *testing.T, doc string) (*engine.Node, map[string]Derived) {
	t.Helper()
	project, root := inheritSchema()
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml", []byte("id: root\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/pipeline/build.yaml", []byte("id: one\n"+doc), 0o644))
	c, err := New(project, root, WithVirtual(fs, "/"), WithLayout(Layout{Resource: "{kind}/{name}.yaml"}))
	assert.NilError(t, err)
	obj, _, err := c.Authored(context.Background())
	assert.NilError(t, err)
	out := map[string]Derived{}
	for _, d := range LoweringPlan(root, obj.Flat()) {
		out[d.Name] = d
	}
	return root, out
}

func TestWhatALoweringProducesInheritsItsSource(t *testing.T) {
	root, plan := inheritPlan(t, "where:\n    pinned: [eu, us]\n")
	run := plan["build-run"]
	assert.Equal(t, run.Fields["where/policy"], "pinned")
	assert.DeepEqual(t, authored(root, run)["where"], map[string]any{"pinned": []string{"eu", "us"}})
	assert.Equal(t, len(plan["build-note"].Fields), 0, "a kind that does not declare it inherits nothing")

	root, plan = inheritPlan(t, "where:\n    failover:\n        main: eu\n")
	assert.DeepEqual(t, authored(root, plan["build-run"])["where"], map[string]any{"failover": map[string]any{"main": "eu"}})

	root, plan = inheritPlan(t, "where:\n    cdn: []\n")
	assert.DeepEqual(t, authored(root, plan["build-run"])["where"], map[string]any{"cdn": []string{}})

	root, plan = inheritPlan(t, "own: cdn\n")
	assert.DeepEqual(t, authored(root, plan["build-own"])["where"], map[string]any{"cdn": []any{}})

	root, plan = inheritPlan(t, "own: cdn\nwhere:\n    pinned: [eu]\n")
	own := plan["build-own"]
	assert.Equal(t, own.Fields["where/policy"], "cdn", "what the lowering states wins")
	_, mixed := own.Fields["where/in"]
	assert.Assert(t, !mixed, "a block is inherited whole or not at all")
	assert.DeepEqual(t, authored(root, own)["where"], map[string]any{"cdn": []any{}})

	_, plan = inheritPlan(t, "")
	assert.Equal(t, len(plan["build-run"].Fields), 0, "nothing stated, nothing inherited")
}

func TestAnUnresolvableBranchStaysFlat(t *testing.T) {
	_, root := inheritSchema()
	job := iterOfKind(root, "job")
	assert.DeepEqual(t, authoredPath(job, "where/in", nil), []string{"where/in"})
	assert.DeepEqual(t, authoredPath(job, "where/in", map[string]any{"where/policy": "failover"}), []string{"where/in"})
	assert.DeepEqual(t, authoredPath(nil, "x", nil), []string{"x"})
	assert.Assert(t, !captures(nil, "x"))
	assert.Assert(t, !captures(job, "nope"))
	assert.Assert(t, !has(map[string]any{"a": "b"}, []string{"a", "c"}))
	assert.Assert(t, !has(map[string]any{}, nil))
	assert.DeepEqual(t, inherit(root, "nope", "job", nil, nil), map[string]any(nil))

	where := whereAttributes()[0]
	v, ok := attrOf(map[string]any{"where/policy": "pinned"}, where)
	assert.Assert(t, ok)
	assert.Equal(t, v, "pinned")
	_, ok = attrOf(map[string]any{"where": "flat"}, where)
	assert.Assert(t, !ok)
	_, ok = attrOf(map[string]any{}, engine.String("plain"))
	assert.Assert(t, !ok)
}
