package jsonschema

import (
	"encoding/json"
	"testing"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
	"gotest.tools/v3/assert"
)

// JSON Schema's `required` names a property of the OBJECT IT SITS ON. A field
// authored at execution/call is not a top-level property, so listing its bare
// name at the root makes every valid document fail to validate — the emitter
// did exactly that, and it went unnoticed while only `id` (top-level, and named
// `id`) was required.
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

	var doc struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	assert.NilError(t, json.Unmarshal(raw, &doc))

	var thing struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
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
	var exec struct {
		Required []string `json:"required"`
	}
	assert.NilError(t, json.Unmarshal(thing.Properties["execution"], &exec))
	assert.DeepEqual(t, exec.Required, []string{"call"})
}
