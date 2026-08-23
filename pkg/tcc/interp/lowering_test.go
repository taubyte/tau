package interp_test

import (
	"context"
	"strconv"
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
					interp.Named(interp.Concat(interp.Attr("name"), interp.Text("-"), interp.Each("name"))),
					interp.Carries(interp.Concat(interp.Text("stage "), interp.Each("name"))),
					// what runs this stage says so on itself, at a path of its own
					interp.Writes(interp.Concat(interp.Attr("name"), interp.Text("-"), interp.Each("name")),
						interp.On("job", interp.Each("run")), interp.At(interp.Text("runs"))),
					interp.Writes(interp.Concat(interp.Attr("name"), interp.Text("-"), interp.Next("name")),
						interp.On("job", interp.Each("run")), interp.At(interp.Text("hands-off-to"))),
				),
				interp.Into("log",
					interp.Named(interp.Concat(interp.Attr("name"), interp.Text("-log"))),
					interp.Carries(interp.Text("what happened")),
				),
			),
		)),
	).With(engine.Leaf("project"), engine.Identity("id", "name"))
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

	c, err := interp.New(project, root, interp.WithVirtual(fs, "/"),
		interp.WithLayout(interp.Layout{Resource: "{kind}/{name}.yaml"}))
	assert.NilError(t, err)
	// AUTHORED, because a plan is what the written config becomes — reading it off the
	// compiled object would be reading it off the answer.
	obj, _, err := c.Authored(context.Background())
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
func TestLoweringWrites(t *testing.T) {
	plan := lowerDoc(t, "id: one\nname: build\nstages:\n    - name: compile\n      run: cc\n    - name: bare\n")

	compile, bare := byName(t, plan, "build-compile"), byName(t, plan, "build-bare")

	// both writes land on what RUNS this stage: this one's name, and the next one's
	assert.Equal(t, len(compile.Writes), 2)
	assert.Equal(t, compile.Writes[0].On.Kind+"/"+compile.Writes[0].On.Name, "job/cc")
	assert.DeepEqual(t, compile.Writes[0].Path, []string{"runs"})
	assert.Equal(t, compile.Writes[0].Value, "build-compile")
	assert.Equal(t, compile.Writes[1].Value, "build-bare", "chains to the next element")

	// `bare` has no `run`, so neither write can be addressed — and a half-written value
	// is worse than none, so both are dropped rather than emitted as "{each.run}".
	assert.Equal(t, len(bare.Writes), 0, "no run, and nothing after it: both unfillable, both dropped")
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
				interp.Into("nonesuch", interp.Named(interp.Text("x"))),
				interp.Into("pipeline"),
			))),
	).With(engine.Identity("id", "name"))
	problems := interp.CheckLowerings(bad)
	assert.Equal(t, len(problems), 3, "got %v", problems)
	assert.Equal(t, problems[0], "lowering into nonesuch, which is not a kind")
	assert.Equal(t, problems[1], "lowering into pipeline produces no name")
	// ...and a kind that lowers into itself produces resources forever. The runtime cap
	// stops the hang and nothing else: the plan it returns is silently short, which is a
	// lie about what a config becomes.
	assert.Equal(t, problems[2], "lowerings cycle: pipeline -> pipeline")
}

// A schema that declares no lowering pays nothing and answers nothing.
func TestNoLoweringDeclared(t *testing.T) {
	root := engine.Root(
		[]*engine.Attribute{engine.String("id")},
		engine.DefineGroup("job", engine.DefineIter([]*engine.Attribute{engine.String("id")})),
	).With(engine.Identity("id", "name"))
	assert.Assert(t, !interp.UsesLowering(root))
	assert.Equal(t, len(interp.LoweringPlan(root, map[string]any{})), 0)
}

// A list lowers into a CHAIN, not into independent things: an element can name the one
// after it. Without that, an ordered list produces N unconnected resources and the
// order it was written in means nothing.
func TestLoweringChainsToTheNextElement(t *testing.T) {
	plan := lowerDoc(t, "id: one\nname: build\nstages:\n    - name: compile\n      run: cc\n    - name: test\n      run: ct\n    - name: ship\n      run: cs\n")

	next := func(name string) string {
		for _, w := range byName(t, plan, name).Writes {
			if len(w.Path) == 1 && w.Path[0] == "hands-off-to" {
				return w.Value
			}
		}
		return ""
	}
	assert.Equal(t, next("build-compile"), "build-test")
	assert.Equal(t, next("build-test"), "build-ship")
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
			interp.Lowers(interp.Into("worker", interp.Named(interp.Concat(interp.Attr("name"), interp.Text("-w"))))))),
	).With(engine.Identity("id", "name"))

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
			interp.Lowers(interp.Into("worker", interp.Named(interp.Concat(interp.Attr("name"), interp.Text("-w"))), interp.Sets("call", interp.Text("run")))))),
	).With(engine.Identity("id", "name"))
	assert.Equal(t, len(interp.CheckLowerings(ok)), 0, "got %v", interp.CheckLowerings(ok))
}

// An edge may start somewhere other than the resource that carries it. Without that, a
// lowering can only draw what the derived resource does, and some of what it would draw
// is false: a queue handing work to another queue is not a thing that happens — the
// function that consumed the first one is what sends to the second.
func TestLoweringWritesIntoSomethingElse(t *testing.T) {
	plan := lowerDoc(t, "id: one\nname: build\nstages:\n    - name: compile\n      run: cc\n    - name: test\n      run: ct\n")

	compile := byName(t, plan, "build-compile")
	assert.Equal(t, len(compile.Writes), 2)
	for _, w := range compile.Writes {
		assert.Equal(t, w.On.Kind+"/"+w.On.Name, "job/cc",
			"both are written into what ran the stage, not into the stage's own resource")
	}
}

// A WRITE WITH NO TARGET GOES INTO THE RESOURCE THE LOWERING PRODUCED; one that names a
// resource goes into that. Which of the two is the whole reason On() exists — and the
// difference decides whose document carries the value.
func TestLoweringSaysWhereTheValueGoes(t *testing.T) {
	root := engine.Root(
		[]*engine.Attribute{engine.String("id")},
		engine.DefineGroup("job", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required(), engine.Generated(engine.GenCID)),
			engine.String("runs"),
		}, engine.Resource("job", "Job", "Job", "job"))),
		engine.DefineGroup("pipeline", engine.DefineIter([]*engine.Attribute{
			engine.String("id"), engine.String("name"),
		},
			engine.Resource("pipeline", "Pipeline", "Pipeline", "pipeline"),
			interp.Lowers(interp.Into("job", interp.Named(interp.Concat(interp.Attr("name"), interp.Text("-j"))),
				// written into THIS one: it is this one's to carry
				interp.Writes(interp.Text("a"), interp.At(interp.Text("runs"))),
				// written into something else: it lands on that, not here
				interp.Writes(interp.Text("b"), interp.On("job", interp.Text("b")), interp.At(interp.Text("runs"))),
			)),
		)),
	).With(engine.Identity("id", "name"))

	plan := interp.LoweringPlan(root, map[string]any{
		"pipeline": map[string]any{"x": map[string]any{"name": "x"}},
	})
	assert.Equal(t, len(plan), 1)
	assert.Equal(t, len(plan[0].Writes), 2)

	var own, elsewhere int
	for _, w := range plan[0].Writes {
		if w.On.Kind == "" {
			own++
			assert.Equal(t, w.Value, "a")
			continue
		}
		elsewhere++
		assert.Equal(t, w.On.Kind+"/"+w.On.Name, "job/b")
	}
	assert.Equal(t, own, 1)
	assert.Equal(t, elsewhere, 1)
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

// A LOWERING IS A FUNCTION OF THE SHELL, so every field it reads has to be one the shell
// has. A template naming a field that does not exist is not an error anywhere else —
// expand leaves the unfilled `{each.attemps}` in the value, and it travels into a
// derived resource looking like config — so it is caught where the mistake is.
func TestCheckLoweringsRefusesAnInputThatDoesNotExist(t *testing.T) {
	root := engine.Root(
		[]*engine.Attribute{engine.String("id")},
		engine.DefineGroup("job", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required(), engine.Generated(engine.GenCID)),
		}, engine.Resource("job", "Job", "Job", "job"))),
		engine.DefineGroup("pipeline", engine.DefineIter([]*engine.Attribute{
			engine.String("id"), engine.String("name"),
		},
			engine.Nested(engine.DefineList("stages", []*engine.Attribute{
				engine.String("name"), engine.Int("attempts"),
			})),
			interp.Lowers(interp.Into("job",
				interp.PerEach("stages"),
				interp.Named(interp.Concat(interp.Attr("name"), interp.Text("-"), interp.Each("name"))),
				interp.Sets("tries", interp.Each("attemps")),                       // a typo
				interp.Writes(interp.Next("nmae"), interp.At(interp.Text("runs"))), // and another
			)),
		)),
	).With(engine.Identity("id", "name"))

	problems := interp.CheckLowerings(root)
	assert.Equal(t, len(problems), 2, "got %v", problems)
	assert.Assert(t, strings.Contains(problems[0], "{each.attemps}"), problems[0])
	assert.Assert(t, strings.Contains(problems[1], "{each.next.nmae}"), problems[1])

	// what the shell really has is accepted, on the resource and on its elements
	ok := engine.Root(
		[]*engine.Attribute{engine.String("id")},
		engine.DefineGroup("job", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required(), engine.Generated(engine.GenCID)),
		}, engine.Resource("job", "Job", "Job", "job"))),
		engine.DefineGroup("pipeline", engine.DefineIter([]*engine.Attribute{
			engine.String("id"), engine.String("name"),
		},
			engine.Nested(engine.DefineList("stages", []*engine.Attribute{
				engine.String("name"), engine.Int("attempts"),
			})),
			interp.Lowers(interp.Into("job",
				interp.PerEach("stages"),
				interp.Named(interp.Concat(interp.Attr("name"), interp.Text("-"), interp.Each("name"))),
				interp.Sets("tries", interp.Each("attempts")),
				interp.Writes(interp.Next("name"), interp.At(interp.Text("runs"))),
			)),
		)),
	)
	assert.Equal(t, len(interp.CheckLowerings(ok)), 0, "got %v", interp.CheckLowerings(ok))
}

// testVerbs is a verb set the way a vocabulary declares one: the generic layer asks it
// for the names and knows nothing else about a verb.

// Lowerable carries two shapes of the SAME kind. CheckLowerings rejects an after shape
// that cannot act as a container, without requiring a second group in the vocabulary.
func TestLowerableTakesBeforeAndAfterShapes(t *testing.T) {
	jobs := engine.DefineGroup("job", engine.DefineIter([]*engine.Attribute{engine.String("id")}))
	build := func(after *engine.Node) *engine.Node {
		before := engine.DefineIter([]*engine.Attribute{engine.String("id")},
			interp.Lowers(interp.Into("job", interp.Named(interp.Concat(interp.Attr("name"), interp.Text("-run"))))))
		return engine.Root([]*engine.Attribute{engine.String("id")}, jobs,
			engine.DefineGroup("pipeline", interp.Lowerable(before, after))).
			With(engine.Identity("id", "name"))
	}

	valid := engine.DefineIterGroup([]*engine.Attribute{engine.String("id")}, jobs).With(engine.Leaf("scope"))
	assert.DeepEqual(t, interp.CheckLowerings(build(valid)), []string(nil))
	assert.DeepEqual(t, interp.CheckLowerings(build(engine.DefineIter([]*engine.Attribute{engine.String("id")}))),
		[]string{"a lowered shape is not a group, so it cannot hold resources of its own"})
	assert.DeepEqual(t, interp.CheckLowerings(build(engine.DefineIterGroup([]*engine.Attribute{engine.String("id")}))),
		[]string{"a lowered shape holds no resources of its own, so nothing could be placed in it"})
}

// WHAT A LOWERING PRODUCES GOES INSIDE ITS SOURCE. The source's own id and kind identify
// the container; only the job receives a derived id.
func TestLowerableProducesAScope(t *testing.T) {
	after := engine.DefineIterGroup([]*engine.Attribute{
		engine.String("id", engine.Required(), engine.Generated(engine.GenCID)),
		engine.String("name"), engine.String("runner"),
	},
		engine.DefineGroup("job", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required(), engine.Generated(engine.GenCID)),
		}, engine.Resource("job", "Job", "Job", "job")))).
		With(engine.Leaf("scope"))

	root := engine.Root(
		[]*engine.Attribute{engine.String("id")},
		engine.DefineGroup("job", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required(), engine.Generated(engine.GenCID)),
			engine.String("feeds"),
		}, engine.Resource("job", "Job", "Job", "job"))),
		engine.DefineGroup("pipeline", interp.Lowerable(engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required()),
			engine.String("name"),
			engine.String("runner"),
		},
			engine.Resource("pipeline", "Pipeline", "Pipeline", "pipeline"),
			interp.Lowers(interp.Into("job",
				interp.Named(interp.Concat(interp.Attr("name"), interp.Text("-run"))),
				interp.Carries(interp.Text("the work")),
				// a value into a resource OUTSIDE the produced scope, which therefore has
				// to say where the produced one is
				interp.Writes(interp.Scope(), interp.On("job", interp.Attr("runner")), interp.At(interp.Text("feeds"))),
			))), after)),
	).With(engine.Leaf("project"), engine.Identity("id", "name"))

	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml", []byte("id: root\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/pipeline/build.yaml", []byte("id: one\nrunner: worker\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/job/worker.yaml", []byte("id: two\n"), 0o644))

	c, err := interp.New(engine.SchemaDefinition(root), root, interp.WithVirtual(fs, "/"),
		interp.WithLayout(interp.Layout{Resource: "{kind}/{name}.yaml", Scope: "{kind}/{name}"}))
	assert.NilError(t, err)

	authored, _, err := c.Authored(context.Background())
	assert.NilError(t, err)
	plan := interp.LoweringPlan(root, authored.Flat())
	assert.Equal(t, len(plan), 1, "only the job is produced: %v", plan)

	job := byName(t, plan, "build-run")
	assert.Equal(t, job.Scope, "build", "what a pipeline becomes lives inside it")
	assert.Equal(t, len(job.Writes), 1)
	assert.Equal(t, job.Writes[0].Value, "/build", "{scope} is absolute, so it reads the same from anywhere")

	// and it COMPILES: the produced scope is loaded by the definition the vocabulary
	// handed over, and the produced job is in it
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)
	flat, _ := obj.Flat()[interp.ProjectKey].(map[string]any)
	scopes, _ := flat["pipeline"].(map[string]any)
	assert.Equal(t, len(scopes), 1, "the produced scope is not in the object: %v", flat)
	for id, v := range scopes {
		assert.Equal(t, id, "one", "the source's authored id is the scope id")
		one, _ := v.(map[string]any)
		assert.Equal(t, one["name"], "build")
		jobs, _ := one["job"].(map[string]any)
		assert.Equal(t, len(jobs), 1, "the produced job is not inside the scope: %v", one)
	}
	// the write landed on the job somebody DID write, outside that scope
	outer, _ := flat["job"].(map[string]any)
	assert.Equal(t, len(outer), 1)
	for _, v := range outer {
		one, _ := v.(map[string]any)
		assert.Equal(t, one["feeds"], "/build")
	}
}

// THE DIAGNOSTIC FORM IS NOT A VALUE. `{each.x}` is how a term PRINTS — it is what
// CheckLowerings shows a vocabulary author who named a field the shell does not have —
// and for a while it was also what a field got SET to when the element had nothing behind
// it. A produced resource came back holding `note: "{each.nodefault}"`, which reads as
// config, compiles, and is a string nobody wrote.
//
// The sibling path never did this: a write whose value cannot be filled is dropped. A
// field is the same question and gets the same answer, so what a produced resource holds
// is only ever values that came from somewhere.
func TestLoweringNeverWritesTheDiagnosticForm(t *testing.T) {
	root := engine.Root(
		[]*engine.Attribute{engine.String("id")},
		engine.DefineGroup("job", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required(), engine.Generated(engine.GenCID)),
			engine.String("note"), engine.String("note2"), engine.Float("factor"),
		}, engine.Resource("job", "Job", "Job", "job"))),
		engine.DefineGroup("pipeline", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required()),
			engine.String("name"),
		},
			engine.Nested(engine.DefineList("stages", []*engine.Attribute{
				engine.String("name", engine.Required()),
				engine.String("outcome", engine.Default("failed")),
				engine.Float("weight", engine.Default(2.5)),
				engine.String("nodefault"),
			})),
			engine.Resource("pipeline", "Pipeline", "Pipeline", "pipeline"),
			interp.Lowers(interp.Into("job",
				interp.PerEach("stages"),
				interp.Named(interp.Concat(interp.Attr("name"), interp.Text("-"), interp.Each("name"))),
				interp.Carries(interp.Concat(interp.Text("stage "), interp.Each("name"))),
				interp.Sets("note", interp.Each("outcome")),
				interp.Sets("factor", interp.Each("weight")),
				interp.Sets("note2", interp.Each("nodefault")), // nothing behind it
			)),
		)),
	).With(engine.Leaf("project"), engine.Identity("id", "name"))

	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml", []byte("id: root\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/pipeline/build.yaml",
		[]byte("id: one\nstages:\n    - name: compile\n"), 0o644))

	c, err := interp.New(engine.SchemaDefinition(root), root, interp.WithVirtual(fs, "/"),
		interp.WithLayout(interp.Layout{Resource: "{kind}/{name}.yaml"}))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	// POSITIVELY: nothing anywhere in the compiled object is a printed term. Asserting
	// that one field is absent would pass again the day a second field leaks.
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch t2 := v.(type) {
		case map[string]any:
			for k, child := range t2 {
				walk(path+"/"+k, child)
			}
		case []any:
			for i, child := range t2 {
				walk(path+"/"+strconv.Itoa(i), child)
			}
		case []string:
			for i, s := range t2 {
				walk(path+"/"+strconv.Itoa(i), s)
			}
		case string:
			assert.Assert(t, !strings.Contains(t2, "{"),
				"a printed term escaped into the compiled object at %s: %q", path, t2)
		}
	}
	walk("", obj.Flat())

	// and the fields that DID have something behind them are all there, in wire form
	plan := interp.LoweringPlan(root, obj.Flat())
	assert.Equal(t, len(plan), 1, "%v", plan)
	assert.DeepEqual(t, plan[0].Fields, map[string]any{"note": "failed", "factor": "2.5"})
}

// A NAME IS AN ADDRESS. The same leak in `Named(...)` did not put a string in a field, it
// put one in the resource's address — `build-{each.label}` is a file nobody could author,
// a key the by-name index would hand back, and nothing can refer to it. Refused before
// anything is written, and reported against what somebody wrote: the pipeline, and the
// stage inside it.
func TestALoweringWhoseNameCannotBeFilledIsRefused(t *testing.T) {
	root := engine.Root(
		[]*engine.Attribute{engine.String("id")},
		engine.DefineGroup("job", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required(), engine.Generated(engine.GenCID)),
		}, engine.Resource("job", "Job", "Job", "job"))),
		engine.DefineGroup("pipeline", engine.DefineIter([]*engine.Attribute{
			engine.String("id", engine.Required()),
			engine.String("name"),
		},
			engine.Nested(engine.DefineList("stages", []*engine.Attribute{
				engine.String("name", engine.Required()),
				engine.String("label"), // optional, and the name reads it
			})),
			engine.Resource("pipeline", "Pipeline", "Pipeline", "pipeline"),
			interp.Lowers(interp.Into("job",
				interp.PerEach("stages"),
				interp.Named(interp.Concat(interp.Attr("name"), interp.Text("-"), interp.Each("label"))),
			)),
		)),
	).With(engine.Leaf("project"), engine.Identity("id", "name"))

	compile := func(stages string) error {
		fs := afero.NewMemMapFs()
		assert.NilError(t, afero.WriteFile(fs, "/project.yaml", []byte("id: root\n"), 0o644))
		assert.NilError(t, afero.WriteFile(fs, "/pipeline/build.yaml", []byte("id: one\nstages:\n"+stages), 0o644))
		c, err := interp.New(engine.SchemaDefinition(root), root, interp.WithVirtual(fs, "/"),
			interp.WithLayout(interp.Layout{Resource: "{kind}/{name}.yaml"}))
		assert.NilError(t, err)
		_, _, err = c.Compile(context.Background())
		return err
	}

	err := compile("    - name: compile\n")
	assert.Assert(t, err != nil, "a job addressed build-{each.label} was produced anyway")
	assert.Assert(t, strings.Contains(err.Error(), "pipeline/build (stages/0)"), err)
	assert.Assert(t, strings.Contains(err.Error(), "job"), err)
	assert.Assert(t, !strings.Contains(err.Error(), "{each."), "the message is prose, not the leak itself: %v", err)

	// with something behind it, the same lowering compiles
	assert.NilError(t, compile("    - name: compile\n      label: cc\n"))
}
