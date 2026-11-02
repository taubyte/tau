package jsonschema

import (
	"encoding/json"
	"testing"

	gjs "github.com/google/jsonschema-go/jsonschema"
	engine "github.com/taubyte/tau/pkg/tcc/engine"
	"gotest.tools/v3/assert"
)

func tableRoot() []*engine.Node {
	return []*engine.Node{
		engine.DefineGroup("things", engine.DefineIter(
			[]*engine.Attribute{engine.String("id")},
			engine.Resource("things", "Thing", "Thing", "thing"),
			engine.Nested(engine.DefineGroup("parts", engine.DefineIter(
				[]*engine.Attribute{engine.String("size")},
			)).With(engine.RequiredGroup())),
		)),
		engine.DefineGroup("stages", engine.DefineIter(
			[]*engine.Attribute{engine.StringSlice("branches")},
		)).With(engine.RequiredGroup()),
	}
}

func TestARequiredTableMustHoldAnEntry(t *testing.T) {
	raw, err := GenerateJSONSchema(tableRoot(), JSONSchemaOptions{})
	assert.NilError(t, err)
	var s gjs.Schema
	assert.NilError(t, json.Unmarshal(raw, &s))
	r, err := s.Resolve(nil)
	assert.NilError(t, err)

	stages := map[string]any{"prod": map[string]any{"branches": []any{"main"}}}
	thing := func(parts any) map[string]any {
		body := map[string]any{"id": "x"}
		if parts != nil {
			body["parts"] = parts
		}
		return map[string]any{"one": body}
	}
	type testCase struct {
		name  string
		doc   map[string]any
		valid bool
	}
	for _, tc := range []testCase{
		{"one stage", map[string]any{"stages": stages}, true},
		{"one stage, a thing with a part", map[string]any{"stages": stages, "things": thing(map[string]any{"a": map[string]any{"size": "1"}})}, true},
		{"no stages", map[string]any{}, false},
		{"empty stages", map[string]any{"stages": map[string]any{}}, false},
		{"a stage of the wrong shape", map[string]any{"stages": map[string]any{"prod": map[string]any{"branches": "main"}}}, false},
		{"a thing without parts", map[string]any{"stages": stages, "things": thing(nil)}, false},
		{"a thing with empty parts", map[string]any{"stages": stages, "things": thing(map[string]any{})}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := r.Validate(tc.doc)
			if tc.valid {
				assert.NilError(t, err)
			} else {
				assert.Assert(t, err != nil)
			}
		})
	}
}
