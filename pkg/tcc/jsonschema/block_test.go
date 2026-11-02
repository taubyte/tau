package jsonschema

import (
	"encoding/json"
	"testing"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
	"gotest.tools/v3/assert"
)

// A block is published as an object of its own fields, its lists as arrays and its
// groups as maps, beside a field the kind declares under the same key.
func TestABlockIsAnObjectOfItsOwn(t *testing.T) {
	root := []*engine.Node{
		engine.DefineGroup("things", engine.DefineIter(
			[]*engine.Attribute{
				engine.String("id", engine.Required()),
				engine.String(engine.Key("build", "kind")),
			},
			engine.Resource("things", "Thing", "Thing", "thing"),
			engine.Nested(engine.DefineBlock("build", []*engine.Attribute{
				engine.String(engine.Key("dockerfile", "path"), engine.Required()),
			},
				engine.DefineList("steps", []*engine.Attribute{engine.String("name", engine.Required())}),
				engine.DefineGroup("inputs", engine.DefineIter([]*engine.Attribute{engine.String("name")})),
			)),
		)),
	}
	raw, err := GenerateJSONSchema(root, JSONSchemaOptions{ExtPrefix: "x-t-"})
	assert.NilError(t, err)

	type object struct {
		Type                 string                     `json:"type"`
		Required             []string                   `json:"required"`
		Properties           map[string]json.RawMessage `json:"properties"`
		Items                json.RawMessage            `json:"items"`
		AdditionalProperties json.RawMessage            `json:"additionalProperties"`
	}
	type schemaDoc struct {
		Defs map[string]object `json:"$defs"`
	}
	var doc schemaDoc
	assert.NilError(t, json.Unmarshal(raw, &doc))

	var build object
	assert.NilError(t, json.Unmarshal(doc.Defs["Thing"].Properties["build"], &build))
	assert.Equal(t, build.Type, "object")
	_, kind := build.Properties["kind"]
	assert.Assert(t, kind, "the kind's own field under build joins the block")

	var dockerfile object
	assert.NilError(t, json.Unmarshal(build.Properties["dockerfile"], &dockerfile))
	assert.DeepEqual(t, dockerfile.Required, []string{"path"})
	assert.DeepEqual(t, build.Required, []string{"dockerfile"})

	var steps object
	assert.NilError(t, json.Unmarshal(build.Properties["steps"], &steps))
	assert.Equal(t, steps.Type, "array")
	assert.Assert(t, len(steps.Items) > 0)

	var inputs object
	assert.NilError(t, json.Unmarshal(build.Properties["inputs"], &inputs))
	assert.Assert(t, len(inputs.AdditionalProperties) > 0, "a group in a block is a map of entries")
}
