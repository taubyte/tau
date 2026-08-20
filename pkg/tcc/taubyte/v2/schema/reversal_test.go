package schema_test

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/interp"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gopkg.in/yaml.v3"
	"gotest.tools/v3/assert"
)

// WHAT A LOWERING GIVES BACK, pinned.
//
// A shell keeps the remainder — the authored fields nothing it produced carries — so
// what is carried is exactly what a shell may stop storing. This test is the guard on
// that list: a lowering that stops carrying a field, or starts composing one it used to
// copy whole, moves a line here. Without it that is a silent change, and the field it
// moves is one a shell has already dropped.
func TestWorkflowCarriesBackWhatItCarriesAway(t *testing.T) {
	assert.DeepEqual(t, schema.Carriages("workflow"), []interp.Carriage{
		// a step's attempts becomes its queue's max-attempts, whole
		{Field: "attempts", In: "steps", Into: "queue", As: "max-attempts"},
		// and its run is the NAME of the resource everything about this step is written
		// into — the function — so the name comes back from there
		{Field: "run", In: "steps", Into: "queue", As: ">"},
	})

	// A step's NAME is read too — into `{name}_{each.name}` — and is not here, because
	// composition is one-way: recovering `reserve` from `checkout_reserve` means knowing
	// where to split a name that may contain an underscore itself. So it stays in the
	// shell, along with everything the lowering never reads.
	remainder := map[string]bool{}
	for _, f := range schema.Remainder("workflow") {
		remainder[f] = true
	}
	for _, kept := range []string{"steps/name", "timeout", "concurrency", "description"} {
		assert.Assert(t, remainder[kept], "%s comes back from nothing, so a shell has to keep it", kept)
	}
	for _, carried := range []string{"steps/attempts", "steps/run"} {
		assert.Assert(t, !remainder[carried], "%s is carried, so a shell storing it stores it twice", carried)
	}
}

// AND THE VALUES REALLY COME BACK — checked against the fixture's own document, read off
// disk. Comparing the read-back to the compiled object would be the same computation run
// twice; comparing it to what somebody wrote is the claim.
func TestWorkflowValuesReadBackFromWhatItBecame(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	got := schema.ReadBack(schema.LoweringPlan(obj.Flat()), schema.Carriages("workflow"))

	var authored struct {
		Steps []struct {
			Run      string `yaml:"run"`
			Attempts int    `yaml:"attempts"`
		} `yaml:"steps"`
	}
	body, err := os.ReadFile("../fixtures/basic/workflow/checkout.yaml")
	assert.NilError(t, err)
	assert.NilError(t, yaml.Unmarshal(body, &authored))
	assert.Assert(t, len(authored.Steps) > 0)

	want := map[string]string{}
	for i, s := range authored.Steps {
		at := "steps/" + strconv.Itoa(i)
		want[at+"/run"] = s.Run
		want[at+"/attempts"] = strconv.Itoa(s.Attempts)
	}
	assert.DeepEqual(t, got, want)
}

// An agent carries values out of a NESTED block rather than a list, and gets them back
// the same way: `memory/size` is authored as `2GB`, compiles to a byte count, and lands
// in the vector as `2GB` again — the scalar's own inverse, not a second table.
func TestAgentValuesReadBackInAuthoredForm(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	got := schema.ReadBack(schema.LoweringPlan(obj.Flat()), schema.Carriages("agent"))

	var authored struct {
		Memory struct {
			Dimensions int    `yaml:"dimensions"`
			Size       string `yaml:"size"`
		} `yaml:"memory"`
	}
	body, err := os.ReadFile("../fixtures/basic/agent/support.yaml")
	assert.NilError(t, err)
	assert.NilError(t, yaml.Unmarshal(body, &authored))

	assert.DeepEqual(t, got, map[string]string{
		"memory-dimensions": strconv.Itoa(authored.Memory.Dimensions),
		"memory-size":       authored.Memory.Size,
	})
}
