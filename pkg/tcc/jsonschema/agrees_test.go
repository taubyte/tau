package jsonschema_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/jsonschema"
	v2 "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

func TestSchemaAndCompilerAgree(t *testing.T) {
	b, err := jsonschema.GenerateJSONSchema(v2.V2Resources, jsonschema.JSONSchemaOptions{})
	assert.NilError(t, err)
	if out := os.Getenv("SCHEMA_OUT"); out != "" {
		assert.NilError(t, os.WriteFile(out, b, 0o644))
	}

	c, err := v2.New(v2.WithLocal("../taubyte/v2/fixtures/basic"), v2.WithBranch("main"))
	assert.NilError(t, err)
	_, _, err = c.Compile(context.Background())
	assert.NilError(t, err, "the fixture must compile for this to mean anything")

	// every switch this vocabulary declares is a closed choice, and every branch it
	// offers is one the schema will accept
	var doc map[string]any
	assert.NilError(t, json.Unmarshal(b, &doc))
	defs, _ := doc["$defs"].(map[string]any)
	deref := func(x any) map[string]any {
		m, _ := x.(map[string]any)
		for {
			r, ok := m["$ref"].(string)
			if !ok {
				return m
			}
			m, _ = defs[r[len("#/$defs/"):]].(map[string]any)
		}
	}
	switches := 0
	for kind, v := range doc["properties"].(map[string]any) {
		e := deref(deref(v)["additionalProperties"])
		branches, ok := e["oneOf"].([]any)
		if !ok {
			continue
		}
		switches++
		// Every branch must assert something: demand its own key, or be the alternative
		// saying an optional switch is absent. A branch asserting neither makes the
		// choice vacuous.
		for _, br := range branches {
			assert.Assert(t, asserts(br.(map[string]any)),
				"%s has a branch that asserts nothing", kind)
		}
	}
	assert.Assert(t, switches > 0, "no switch was expressed as a closed choice at all")
}

// asserts reports whether a oneOf branch constrains anything: `required` for a
// branch that demands its key, `not` for the alternative that says the switch is
// absent, or either of those nested under `properties`.
func asserts(m map[string]any) bool {
	if m["required"] != nil || m["not"] != nil {
		return true
	}
	props, _ := m["properties"].(map[string]any)
	for _, v := range props {
		if child, ok := v.(map[string]any); ok && asserts(child) {
			return true
		}
	}
	return false
}
