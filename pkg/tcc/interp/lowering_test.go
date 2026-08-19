package interp_test

import (
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
	"gotest.tools/v3/assert"
)

// A vocabulary tcc has never heard of. The point of these tests is that lowering is the
// INTERPRETER's feature: nothing here is a workflow, a queue or an entity, and the
// expansion works anyway because the vocabulary declares what it becomes.
func pipelineSchema() (engine.Schema, *engine.Node) {
	root := engine.Root(
		[]*engine.Attribute{engine.String("id")},
		engine.DefineGroup("job", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required()),
		}, engine.Resource("job", "Job", "Job", "job"))),
		engine.DefineGroup("log", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required()),
		}, engine.Resource("log", "Log", "Log", "log"))),
		engine.DefineGroup("pipeline", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required()),
			engine.String("name"),
		},
			engine.Nested(engine.DefineList("stages", []*engine.Attribute{
				engine.String("name", engine.Required()),
				engine.String("run"),
			})),
			engine.Resource("pipeline", "Pipeline", "Pipeline", "pipeline"),
			interp.Lowers(
				interp.Lowering{Into: "job", PerEach: "stages", Name: "{name}-{each.name}",
					Why:   "stage {each.name}",
					Links: []interp.Link{{To: "job/{each.run}", Verb: "runs"}}},
				interp.Lowering{Into: "log", Name: "{name}-log", Why: "what happened"},
			),
		)),
	).With(engine.Leaf("project"))
	return engine.SchemaDefinition(root), root
}

func lowerDoc(t *testing.T, doc string) []interp.Derived {
	t.Helper()
	project, root := pipelineSchema()

	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml", []byte("id: root\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/pipeline/build.yaml", []byte(doc), 0o644))

	e, err := engine.New(project, yaseer.VirtualFS(fs, "/"))
	assert.NilError(t, err)
	obj, err := e.Process()
	assert.NilError(t, err)
	return interp.LoweringPlan(root, obj.Flat())
}

// One per element of a list, one per resource, and the names come from the templates.
func TestLoweringPlan(t *testing.T) {
	plan := lowerDoc(t, "id: one\nname: build\nstages:\n    - name: compile\n      run: cc\n    - name: test\n      run: ct\n")

	assert.Equal(t, len(plan), 3)
	assert.Equal(t, plan[0].Kind+"/"+plan[0].Name, "job/build-compile")
	assert.Equal(t, plan[0].Of, "pipeline/build")
	assert.Equal(t, plan[0].Why, "stage compile")
	assert.Equal(t, plan[1].Kind+"/"+plan[1].Name, "job/build-test")
	assert.Equal(t, plan[2].Kind+"/"+plan[2].Name, "log/build-log", "one per resource, not per element")
}

// The wiring is what makes a plan drawable rather than a list of names: a consumer can
// show what is INSIDE a pipeline without knowing what a pipeline is.
func TestLoweringLinks(t *testing.T) {
	plan := lowerDoc(t, "id: one\nname: build\nstages:\n    - name: compile\n      run: cc\n    - name: bare\n")

	compile, bare := byName(t, plan, "build-compile"), byName(t, plan, "build-bare")

	assert.Equal(t, len(compile.Links), 1)
	assert.Equal(t, compile.Links[0].Kind+"/"+compile.Links[0].Name, "job/cc")
	assert.Equal(t, compile.Links[0].Verb, "runs")

	// `bare` has no `run`, so the link cannot be filled — and a half-written edge is
	// worse than no edge, so it is dropped rather than emitted as "job/{each.run}".
	assert.Equal(t, len(bare.Links), 0, "an unfillable link is dropped, not guessed")
}

func byName(t *testing.T, plan []interp.Derived, name string) interp.Derived {
	t.Helper()
	for _, d := range plan {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("no derived resource named %q in %v", name, plan)
	return interp.Derived{}
}

// The plan is a pure function of the config: same in, same out, every time.
func TestLoweringPlanIsStable(t *testing.T) {
	const doc = "id: one\nname: build\nstages:\n    - name: b\n    - name: a\n"
	first, second := lowerDoc(t, doc), lowerDoc(t, doc)
	assert.DeepEqual(t, first, second)
	assert.Equal(t, first[0].Name, "build-a", "sorted, so a map's iteration order cannot leak in")
}

// A lowering into a kind the vocabulary does not have is a plan nothing can carry out.
// Catching it at BUILD time is the difference between the vocabulary author seeing it
// and a user seeing it.
func TestCheckLowerings(t *testing.T) {
	_, root := pipelineSchema()
	assert.Equal(t, len(interp.CheckLowerings(root)), 0)

	bad := engine.Root(
		[]*engine.Attribute{engine.String("id")},
		engine.DefineGroup("pipeline", engine.DefineIter([]*engine.Attribute{engine.String("id")},
			interp.Lowers(
				interp.Lowering{Into: "nonesuch", Name: "x"},
				interp.Lowering{Into: "pipeline"},
			))),
	)
	problems := interp.CheckLowerings(bad)
	assert.Equal(t, len(problems), 2)
	assert.Equal(t, problems[0], "lowering into nonesuch, which is not a kind")
	assert.Equal(t, problems[1], "lowering into pipeline produces no name")
}

// A schema that declares no lowering pays nothing and answers nothing.
func TestNoLoweringDeclared(t *testing.T) {
	root := engine.Root(
		[]*engine.Attribute{engine.String("id")},
		engine.DefineGroup("job", engine.DefineIter([]*engine.Attribute{engine.String("id")})),
	)
	assert.Assert(t, !interp.UsesLowering(root))
	assert.Equal(t, len(interp.LoweringPlan(root, map[string]any{})), 0)
}
