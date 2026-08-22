package interp_test

import (
	"context"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp"
	"gotest.tools/v3/assert"
)

// A TERM CAN BE ASKED WHAT IT REFERENCES, WITHOUT BEING RUN.
//
// This is the property the string templates could not have and the reason Named's doc has
// always said a term rather than a closure: a lowering has to be printable, inspectable
// and — the new part — reversible, and all three are questions asked of the DECLARATION
// with no config anywhere in the room. There is no way to evaluate a term from outside
// this package at all: `env` is unexported, so this test could not cheat if it wanted to.
func TestATermSaysWhatItReadsWithoutBeingRun(t *testing.T) {
	// the exact shape the vocabulary declares for a step's failure queue
	name := interp.Concat(interp.Attr("name"), interp.Text("_"), interp.Each("on_error"))

	parts := name.Parts()
	assert.Equal(t, len(parts), 3, "a join says what it is made of: %v", parts)

	assert.Equal(t, parts[0].Origin(), interp.OfSource)
	assert.Equal(t, parts[0].Field(), "name")

	sep, fixed := parts[1].Literal()
	assert.Assert(t, fixed, "the separator is text the lowering wrote, and says so")
	assert.Equal(t, sep, "_")

	assert.Equal(t, parts[2].Origin(), interp.OfElement)
	assert.Equal(t, parts[2].Field(), "on_error")

	// every field it reads, in order, with where each is read from
	assert.DeepEqual(t, name.Reads(), []interp.Read{
		{Field: "name", Origin: interp.OfSource},
		{Field: "on_error", Origin: interp.OfElement},
	})

	// ONE FIELD OR A BUILT STRING, which is a question about the term rather than about
	// its punctuation. It used to be "the template is `{`, one key, `}` and nothing else",
	// and it decided whether a scalar kept its type and whether a value could be given
	// back.
	_, sole := name.Sole()
	assert.Assert(t, !sole, "a join has no single value behind it")

	one, sole := interp.Each("attempts").Sole()
	assert.Assert(t, sole)
	assert.DeepEqual(t, one, interp.Read{Field: "attempts", Origin: interp.OfElement})

	// a nested key is SEGMENTS — the "/" is the engine's to write, never the vocabulary's
	assert.DeepEqual(t, interp.Attr(engine.Key("memory", "size")).Key(), []string{"memory", "size"})
	assert.Equal(t, interp.Attr(engine.Key("memory", "size")).Field(), "memory/size")

	// and it still PRINTS the way it always did, which is what diagnostics and the record
	// of what a lowering declared are made of
	assert.Equal(t, name.String(), "{name}_{each.on_error}")
	assert.Equal(t, interp.Next("name").String(), "{each.next.name}")
	assert.Equal(t, interp.Scope().String(), "{scope}")

	// a Concat of one thing IS that thing, so "reads exactly one field" is a property of
	// the value and not of how somebody happened to spell it
	folded, sole := interp.Concat(interp.Each("attempts")).Sole()
	assert.Assert(t, sole)
	assert.DeepEqual(t, folded, one)
}

// THE SEPARATOR IS NOT GUESSED, and this is the test that says so.
//
// `.notes/v2/shell-dedup.md` §2: "reading `checkout_reserve` back means knowing it splits
// at the first underscore, which is a guess as soon as a name contains one. Invertibility
// has to be a property the DSL states, not one inferred from the template string."
//
// So: a pipeline called `my_build` with a stage called `run_tests`, which the lowering
// composes into `my_build_run_tests`. Splitting at the first underscore gives
// `build_run_tests`; at the last, `tests`. Both are wrong, and both are what a scanner
// over the finished string is reduced to. The term knows the prefix is
// `Attr("name")` + `Text("_")`, fills it in, and cuts there — so what comes back is
// `run_tests`, exactly.
func TestAComposedNameGivesBackThePartThatWasComposedIn(t *testing.T) {
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
			})),
			engine.Resource("pipeline", "Pipeline", "Pipeline", "pipeline"),
			interp.Lowers(interp.Into("job",
				interp.PerEach("stages"),
				interp.Named(interp.Concat(interp.Attr("name"), interp.Text("_"), interp.Each("name"))),
			)),
		)),
	).With(engine.Leaf("project"), engine.Identity("id", "name"))

	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml", []byte("id: root\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/pipeline/my_build.yaml",
		[]byte("id: one\nstages:\n    - name: run_tests\n"), 0o644))

	c, err := interp.New(engine.SchemaDefinition(root), root, interp.WithVirtual(fs, "/"),
		interp.WithLayout(interp.Layout{Resource: "{kind}/{name}.yaml"}))
	assert.NilError(t, err)
	authored, _, err := c.Authored(context.Background())
	assert.NilError(t, err)
	plan := interp.LoweringPlan(root, authored.Flat())

	assert.Equal(t, len(plan), 1)
	assert.Equal(t, plan[0].Name, "my_build_run_tests",
		"three underscores, and only one of them is the separator")

	cs := interp.Carriages(root, "pipeline")
	assert.Equal(t, len(cs), 1, "the stage's name is carried by the job's: %v", cs)
	assert.Assert(t, cs[0].Composed())

	back := interp.ReadBack(plan, cs)
	assert.DeepEqual(t, back, map[string]string{"stages/0/name": "run_tests"})

	// and the shell therefore stops keeping it, which is the whole point of asking
	for _, f := range interp.Remainder(root, "pipeline") {
		assert.Assert(t, f != "stages/name",
			"the stage's name comes back from what it became, so a shell storing it stores it twice")
	}
}
