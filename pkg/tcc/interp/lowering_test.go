package interp_test

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp"
	"gotest.tools/v3/assert"
)

// A vocabulary tcc has never heard of. The point of these tests is that lowering is the
// INTERPRETER's feature: nothing here is a workflow, a queue or an entity, and the
// expansion works anyway because the vocabulary declares what it becomes.
func pipelineSchema() (engine.Schema, *engine.Node) {
	root := engine.Root(
		[]*engine.Attribute{engine.String("id")},
		engine.DefineGroup("job", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required(), engine.Generated(engine.GenCID)),
		}, engine.Resource("job", "Job", "Job", "job"))),
		engine.DefineGroup("log", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required(), engine.Generated(engine.GenCID)),
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
				interp.Into("job",
					interp.PerEach("stages"),
					interp.Named("{name}-{each.name}"),
					interp.Carries("stage {each.name}"),
					interp.LinksTo("job", "{each.run}", "runs"),
					interp.LinksTo("job", "{name}-{each.next.name}", "then",
						interp.From("job", "{each.run}")),
				),
				interp.Into("log",
					interp.Named("{name}-log"),
					interp.Carries("what happened"),
				),
			),
		)),
	).With(engine.Leaf("project"))
	return engine.SchemaDefinition(root), root
}

// Compiled through the INTERPRETER, not the engine alone: a compiled resource is keyed
// by its id, and that keying is one of the interpreter's transforms. Reading the plan
// off a raw engine object would key instances by filename and quietly derive the same
// id for two different resources — which is how this helper was wrong to begin with.
func lowerDoc(t *testing.T, doc string) []interp.Derived { return lowerNamed(t, "build", doc) }

// A resource's NAME is its filename in this vocabulary, so renaming one means moving the
// file — which is exactly what a derived id has to survive.
func lowerNamed(t *testing.T, file, doc string) []interp.Derived {
	t.Helper()
	project, root := pipelineSchema()

	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml", []byte("id: root\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/pipeline/"+file+".yaml", []byte(doc), 0o644))

	c, err := interp.New(project, root, interp.WithVirtual(fs, "/"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
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

	// `runs` from its own field, and `then` to the element after it
	assert.Equal(t, len(compile.Links), 2)
	assert.Equal(t, compile.Links[0].Kind+"/"+compile.Links[0].Name, "job/cc")
	assert.Equal(t, compile.Links[0].Verb, "runs")
	assert.Equal(t, compile.Links[1].Name, "build-bare", "chains to the next element")

	// `bare` has no `run`, so the link cannot be filled — and a half-written edge is
	// worse than no edge, so it is dropped rather than emitted as "job/{each.run}".
	assert.Equal(t, len(bare.Links), 0, "no run, and nothing after it: both links unfillable, both dropped")
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
				interp.Into("nonesuch", interp.Named("x")),
				interp.Into("pipeline"),
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

// A list lowers into a CHAIN, not into independent things: an element can name the one
// after it. Without that, an ordered list produces N unconnected resources and the
// order it was written in means nothing.
func TestLoweringChainsToTheNextElement(t *testing.T) {
	plan := lowerDoc(t, "id: one\nname: build\nstages:\n    - name: compile\n      run: cc\n    - name: test\n      run: ct\n    - name: ship\n      run: cs\n")

	next := func(name string) string {
		for _, l := range byName(t, plan, name).Links {
			if l.Verb == "then" {
				return l.Kind + "/" + l.Name
			}
		}
		return ""
	}
	assert.Equal(t, next("build-compile"), "job/build-test")
	assert.Equal(t, next("build-test"), "job/build-ship")
	assert.Equal(t, next("build-ship"), "", "the last element has no next, and that is where the chain ends")
}

// A lowering has to produce a resource that could EXIST: a kind's required fields are
// required of anything of that kind, including what a compiler wrote. The mistake this
// catches is real — a workflow lowering into a code-bearing kind and setting none of
// the code.
func TestCheckLoweringsRefusesAnUnbuildableResource(t *testing.T) {
	root := engine.Root(
		[]*engine.Attribute{engine.String("id")},
		engine.DefineGroup("worker", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required(), engine.Generated(engine.GenCID)),
			engine.String("call", engine.Required()),
			engine.String("mem", engine.Required(), engine.Default("32MB")),
		}, engine.Resource("worker", "Worker", "Worker", "worker"))),
		engine.DefineGroup("pipeline", engine.DefineIter([]*engine.Attribute{engine.String("id")},
			interp.Lowers(interp.Into("worker", interp.Named("{name}-w"))))),
	)

	problems := interp.CheckLowerings(root)
	assert.Equal(t, len(problems), 1, "got %v", problems)
	assert.Equal(t, problems[0], "lowering into worker leaves call unset, which that kind requires")

	// setting it satisfies the check; a field with a default or one the platform
	// generates never needed setting
	ok := engine.Root(
		[]*engine.Attribute{engine.String("id")},
		engine.DefineGroup("worker", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required(), engine.Generated(engine.GenCID)),
			engine.String("call", engine.Required()),
			engine.String("mem", engine.Required(), engine.Default("32MB")),
		}, engine.Resource("worker", "Worker", "Worker", "worker"))),
		engine.DefineGroup("pipeline", engine.DefineIter([]*engine.Attribute{engine.String("id")},
			interp.Lowers(interp.Into("worker", interp.Named("{name}-w"), interp.Sets("call", "run"))))),
	)
	assert.Equal(t, len(interp.CheckLowerings(ok)), 0)
}

// An edge may start somewhere other than the resource that carries it. Without that, a
// lowering can only draw what the derived resource does, and some of what it would draw
// is false: a queue handing work to another queue is not a thing that happens — the
// function that consumed the first one is what sends to the second.
func TestLoweringLinkOrigin(t *testing.T) {
	plan := lowerDoc(t, "id: one\nname: build\nstages:\n    - name: compile\n      run: cc\n    - name: test\n      run: ct\n")

	compile := byName(t, plan, "build-compile")
	var chained, own int
	for _, l := range compile.Links {
		if l.From.Kind != "" {
			chained++
			assert.Equal(t, l.From.Kind+"/"+l.From.Name, "job/cc", "the chain starts at what ran")
		} else {
			own++
		}
	}
	assert.Equal(t, chained, 1)
	assert.Equal(t, own, 1, "the link with no origin is the resource's own")
}

// A lowering's edges carry the vocabulary's own classes. Without them every edge a
// lowering produced was drawn as a use, including the ones that only POINT — and a
// queue naming its dead-letter queue is the same statement as the authored field, so
// drawing the two differently says the lowering invented a relationship.
func TestLoweringLinkClasses(t *testing.T) {
	root := engine.Root(
		[]*engine.Attribute{engine.String("id")},
		engine.DefineGroup("job", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required(), engine.Generated(engine.GenCID)),
		}, engine.Resource("job", "Job", "Job", "job"))),
		engine.DefineGroup("pipeline", engine.DefineIter([]*engine.Attribute{
			engine.String("id"), engine.String("name"),
		},
			engine.Resource("pipeline", "Pipeline", "Pipeline", "pipeline"),
			interp.Lowers(interp.Into("job", interp.Named("{name}-j"),
				interp.LinksTo("job", "a", "uses", interp.Effect("read")),
				interp.LinksTo("job", "b", "points at", interp.AsReference()),
			)),
		)),
	)

	plan := interp.LoweringPlan(root, map[string]any{
		"pipeline": map[string]any{"x": map[string]any{"name": "x"}},
	})
	assert.Equal(t, len(plan), 1)
	assert.Equal(t, plan[0].Links[0].Effect, "read")
	assert.Equal(t, plan[0].Links[0].Ref, false)
	assert.Equal(t, plan[0].Links[1].Ref, true, "a link that points is not a use")
}

// A resource a compiler wrote is stored under its id and referred to by it, so the id
// has to be the SAME every time the config is compiled — otherwise every build looks
// like a delete and a create, and two clouds compiling one project disagree about what
// is the same resource.
func TestDerivedIDIsDeterministic(t *testing.T) {
	const doc = "id: one\nname: build\nstages:\n    - name: compile\n      run: cc\n"
	first, second := lowerDoc(t, doc), lowerDoc(t, doc)
	assert.Equal(t, first[0].ID, second[0].ID)
	assert.Assert(t, first[0].ID != "", "a derived resource with no id cannot be stored")
	assert.Assert(t, strings.HasPrefix(first[0].ID, "Qm"), "same shape as a minted id: %s", first[0].ID)

	// it hangs off the SOURCE's id, so renaming the source does not re-key what it
	// produced — the name it PRODUCES changes, the identity does not
	renamed := lowerNamed(t, "renamed", doc)
	assert.Equal(t, byName(t, renamed, "renamed-compile").ID, first[0].ID)

	// ...and two different sources never collide, however alike their parts are
	other := lowerDoc(t, "id: two\nname: build\nstages:\n    - name: compile\n      run: cc\n")
	assert.Assert(t, other[0].ID != first[0].ID)

	// the part is part of it, so two steps of one workflow are two resources
	pair := lowerDoc(t, "id: one\nname: build\nstages:\n    - name: a\n      run: cc\n    - name: b\n      run: cc\n")
	assert.Assert(t, byName(t, pair, "build-a").ID != byName(t, pair, "build-b").ID)
}
