package interp

import (
	"context"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"gotest.tools/v3/assert"
)

func backSchema() (engine.Schema, *engine.Node) {
	id := engine.String("id", engine.Required(), engine.Generated(engine.GenCID))
	root := engine.Root(
		[]*engine.Attribute{engine.String("id")},
		engine.DefineGroup("job", engine.DefineIter([]*engine.Attribute{id,
			engine.String("attempts"), engine.String("label"), engine.String("feeds"), engine.String("mixed"),
		}, engine.Resource("job", "Job", "Job", "job"))),
		engine.DefineGroup("pipeline", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required()),
			engine.String("name"),
			engine.String("tier"),
			engine.String("kept"),
		},
			engine.Nested(engine.DefineList("stages", []*engine.Attribute{
				engine.String("name", engine.Required()),
				engine.String("attempts"),
				engine.String("run"),
				engine.String("note"),
			})),
			engine.Resource("pipeline", "Pipeline", "Pipeline", "pipeline"),
			Lowers(
				Into("job",
					PerEach("stages"),
					Named(Concat(Attr("name"), Text("-"), Each("name"))),
					Sets("attempts", Each("attempts")),
					Sets("label", Attr("name")),
					Sets("mixed", Concat(Each("note"), Attr("tier"))),
					Sets("feeds", Next("name")),
					Writes(Text("go"), On("job", Each("run")), At(Text("feeds"))),
					Writes(Text("again"), On("job", Each("run")), At(Text("label"))),
				),
				Into("job",
					Named(Concat(Attr("name"), Text("-tier"))),
					Sets("attempts", Attr("tier")),
					Sets("label", Each("note")),
				),
			),
		)),
	).With(engine.Leaf("project"), engine.Identity("id", "name"))
	return engine.SchemaDefinition(root), root
}

func TestCarriagesAreWhatInverts(t *testing.T) {
	_, root := backSchema()
	cs := Carriages(root, "pipeline")
	type row struct{ Field, In, As string }
	var got []row
	for _, c := range cs {
		got = append(got, row{c.Field, c.In, c.As})
	}
	assert.DeepEqual(t, got, []row{
		{"tier", "", "attempts"},
		{"attempts", "stages", "attempts"},
		{"name", "stages", producedName},
		{"run", "stages", targetName},
	})
	for _, c := range cs {
		assert.Equal(t, c.Composed(), c.As == producedName, c.Field)
	}
	assert.Assert(t, Carriages(root, "nokind") == nil)
}

func TestRemainderIsWhatNothingGivesBack(t *testing.T) {
	_, root := backSchema()
	assert.DeepEqual(t, Remainder(root, "pipeline"), []string{"kept", "stages/note"})
	assert.Assert(t, Remainder(root, "nokind") == nil)
	assert.DeepEqual(t, Remainder(root, "job"), []string{"attempts", "feeds", "label", "mixed"})
}

func TestReadBackRecoversTheAuthoredValues(t *testing.T) {
	project, root := backSchema()
	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml", []byte("id: root\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/pipeline/build.yaml", []byte(
		"id: one\ntier: gold\nstages:\n    - name: com-pile\n      attempts: \"3\"\n      run: cc\n    - name: test\n"), 0o644))
	c, err := New(project, root, WithVirtual(fs, "/"), WithLayout(Layout{Resource: "{kind}/{name}.yaml"}))
	assert.NilError(t, err)
	obj, _, err := c.Authored(context.Background())
	assert.NilError(t, err)

	got := ReadBack(LoweringPlan(root, obj.Flat()), Carriages(root, "pipeline"))
	assert.DeepEqual(t, got, map[string]string{
		"tier":              "gold",
		"stages/0/attempts": "3",
		"stages/0/name":     "com-pile",
		"stages/0/run":      "cc",
		"stages/1/name":     "test",
	})
}

func TestReadBackSkipsWhatCannotBeCut(t *testing.T) {
	_, root := backSchema()
	cs := Carriages(root, "pipeline")
	plan := []Derived{
		{Kind: "job", Name: "other-x", Source: Source{Name: "build", Path: "stages/0"}},
		{Kind: "job", Name: "build-y", Source: Source{Name: "build", Path: "elsewhere/0"}},
		{Kind: "log", Name: "build-z", Source: Source{Name: "build", Path: "stages/1"}},
		{Kind: "job", Name: "build-tier", Fields: map[string]any{"attempts": 3}, Source: Source{Name: "build"}},
	}
	assert.DeepEqual(t, ReadBack(plan, cs), map[string]string{})
}

func TestValueAtReadsEachLandingPlace(t *testing.T) {
	d := Derived{Name: "made", Fields: map[string]any{"s": "v", "n": 1},
		Writes: []Written{{Value: "x"}, {Value: "y", On: End{Kind: "job", Name: "cc"}}}}
	for as, want := range map[string]string{producedName: "made", targetName: "cc", "s": "v"} {
		v, ok := valueAt(d, as)
		assert.Assert(t, ok, as)
		assert.Equal(t, v, want)
	}
	for _, as := range []string{"n", "absent"} {
		_, ok := valueAt(d, as)
		assert.Assert(t, !ok, as)
	}
	_, ok := valueAt(Derived{}, producedName)
	assert.Assert(t, !ok)
	_, ok = valueAt(Derived{Writes: []Written{{Value: "x"}}}, targetName)
	assert.Assert(t, !ok, "a write into the produced resource names no target")
}

func TestSoleTargetNeedsOneName(t *testing.T) {
	_, ok := soleTarget(nil)
	assert.Assert(t, !ok)
	got, ok := soleTarget([]write{{}, {onKind: "job", onName: Each("run")}, {onKind: "job", onName: Each("run")}})
	assert.Assert(t, ok)
	assert.Assert(t, got.Equal(Each("run")))
	_, ok = soleTarget([]write{{onKind: "job", onName: Each("run")}, {onKind: "job", onName: Each("other")}})
	assert.Assert(t, !ok, "two different targets give no name back")
}
