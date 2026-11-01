package jsonschema_test

import (
	"encoding/json"
	"testing"

	gjs "github.com/google/jsonschema-go/jsonschema"
	"github.com/taubyte/tau/pkg/tcc/jsonschema"
	v2 "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// The schema has to refuse what the compiler refuses, which only handing it a
// document proves.
func resolved(t *testing.T) *gjs.Resolved {
	t.Helper()
	b, err := jsonschema.GenerateJSONSchema(v2.V2Resources, jsonschema.JSONSchemaOptions{})
	assert.NilError(t, err)
	var s gjs.Schema
	assert.NilError(t, json.Unmarshal(b, &s))
	r, err := s.Resolve(nil)
	assert.NilError(t, err)
	return r
}

// one resource of one kind, as the published document shapes it
func doc(kind, name string, body map[string]any) map[string]any {
	return map[string]any{
		"id": "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR", // the project's own
		kind: map[string]any{name: body},
	}
}

func TestSchemaRefusesWhatTheCompilerRefuses(t *testing.T) {
	r := resolved(t)
	fn := func(source any) map[string]any {
		body := map[string]any{
			"id":          "QmNf1SA5DVFbNTVYYPvKqTLLYzGqPZAmtLqYmYnFCEcvLh",
			"description": "x",
			"runtime":     "wasm",
			"execution":   map[string]any{"call": "c", "timeout": "1s", "memory": "1MB"},
		}
		if source != nil {
			body["source"] = source
		}
		return doc("function", "checkout", body)
	}

	for _, tc := range []struct {
		name  string
		doc   map[string]any
		valid bool
	}{
		{"source absent", fn(nil), true},
		{"a bare path", fn("pkg/checkout"), true},
		{"a component", fn(map[string]any{"component": "paylib"}), true},
		{"a git url", fn(map[string]any{"git": "https://gitlab.com/a/b", "branch": "main"}), true},

		{"an empty source block", fn(map[string]any{}), false},
		{"a source with no branch", fn(map[string]any{"branch": "main"}), false},
		{"two branches at once", fn(map[string]any{"component": "x", "git": "https://g/x"}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := r.Validate(tc.doc)
			if tc.valid {
				assert.NilError(t, err, "the compiler accepts this and the schema must too")
			} else {
				assert.Assert(t, err != nil, "the compiler refuses this and the schema must too")
			}
		})
	}
}

func TestSchemaAcceptsAnOptionalSwitchLeftOut(t *testing.T) {
	r := resolved(t)
	assert.NilError(t, r.Validate(doc("gateway", "inner", map[string]any{
		"id":          "QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zg",
		"description": "mounted inside another, so it has no name of its own",
		"route": map[string]any{
			"path":    "/x",
			"handler": map[string]any{"function": map[string]any{"name": "checkout"}},
		},
	})), "a gateway with no host compiles; the schema must accept it")
}

func TestSchemaRefusesAScalarWhereABlockBelongs(t *testing.T) {
	r := resolved(t)
	for _, tc := range []struct {
		name string
		doc  map[string]any
	}{
		{"a proxy's forward", doc("proxy", "pg", map[string]any{
			"id": "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR", "protocol": "tcp",
			"service": "postgres", "forward": "db"})},
		{"a database's instances", doc("database", "orders", map[string]any{
			"id":        "QmPChzsSKcAiSFqNqrH7LM4t9GhqfHmi3ihn7C9hNc8Vgd",
			"engine":    map[string]any{"distributed": map[string]any{"size": "16MB"}},
			"instances": "alone"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Assert(t, r.Validate(tc.doc) != nil,
				"the compiler refuses this and the schema must too")
		})
	}
}
