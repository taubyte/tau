package jsonschema

import (
	"encoding/json"
	"testing"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
	"gotest.tools/v3/assert"
)

func TestRequiredIsRecordedAtTheLevelTheFieldLivesAt(t *testing.T) {
	root := []*engine.Node{
		engine.DefineGroup("things", engine.DefineIter(
			[]*engine.Attribute{
				engine.String("id", engine.Required()),
				engine.String("call", engine.Path("execution", "call"), engine.Required()),
				engine.String("deep", engine.Path("a", "b", "c"), engine.Required()),
				engine.String("optional", engine.Path("execution", "timeout")),
			},
			engine.Resource("things", "Thing", "Thing", "thing"),
		)),
	}
	raw, err := GenerateJSONSchema(root, JSONSchemaOptions{ExtPrefix: "x-t-"})
	assert.NilError(t, err)

	type schemaDoc struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	var doc schemaDoc
	assert.NilError(t, json.Unmarshal(raw, &doc))

	type thingSchema struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	var thing thingSchema
	assert.NilError(t, json.Unmarshal(doc.Defs["Thing"], &thing))

	// the root lists the top-level field and the CONTAINERS of the nested ones —
	// a required execution/call means `execution` must be there to hold it —
	// and never a nested leaf's bare name.
	assert.DeepEqual(t, thing.Required, []string{"id", "execution", "a"})
	for _, leaked := range []string{"call", "deep"} {
		for _, r := range thing.Required {
			assert.Assert(t, r != leaked,
				"%q is nested; naming it at the root rejects every valid document", leaked)
		}
	}

	// every name in `required` must actually BE a property of that object
	for _, r := range thing.Required {
		_, ok := thing.Properties[r]
		assert.Assert(t, ok, "required names %q, which is not a property here", r)
	}

	// ...and the leaf is required on its own object, one level down
	type execSchema struct {
		Required []string `json:"required"`
	}
	var exec execSchema
	assert.NilError(t, json.Unmarshal(thing.Properties["execution"], &exec))
	assert.DeepEqual(t, exec.Required, []string{"call"})
}

func TestAConditionalDefaultCarriesItsCondition(t *testing.T) {
	root := []*engine.Node{
		engine.DefineGroup("things", engine.DefineIter(
			[]*engine.Attribute{
				engine.String("id", engine.Required()),
				engine.String("tier", engine.InSet("a", "b")),
				engine.String("entry", engine.Default("main"), engine.DefaultWhen("tier", "b")),
				engine.String("always", engine.Default("x")),
			},
			engine.Resource("things", "Thing", "Thing", "thing"),
		)),
	}
	raw, err := GenerateJSONSchema(root, JSONSchemaOptions{ExtPrefix: "x-t-"})
	assert.NilError(t, err)

	type defSchema struct {
		Properties map[string]map[string]any `json:"properties"`
	}
	type schemaDoc struct {
		Defs map[string]defSchema `json:"$defs"`
	}
	var doc schemaDoc
	assert.NilError(t, json.Unmarshal(raw, &doc))

	entry := doc.Defs["Thing"].Properties["entry"]
	assert.Equal(t, entry["default"], "main")
	assert.DeepEqual(t, entry["x-t-default-when"], map[string]any{"field": "tier", "in": []any{"b"}})

	always := doc.Defs["Thing"].Properties["always"]
	assert.Equal(t, always["default"], "x")
	_, conditional := always["x-t-default-when"]
	assert.Assert(t, !conditional, "an unconditional default names no condition")
}
