package jsonschema_test

import (
	"encoding/json"
	"testing"

	gjs "github.com/google/jsonschema-go/jsonschema"
	"github.com/taubyte/tau/pkg/tcc/jsonschema"
	v2 "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// THE SCHEMA HAS TO REFUSE WHAT THE COMPILER REFUSES, and the only way to know is to
// hand it a document. Its neighbour checks the SHAPE of the emitted choices; nothing
// checked what they accept, which is how a switch with nothing required inside its
// branches came to be published as no constraint at all — `source: {}` and
// `source: {branch: main}` validated clean against a schema for a compiler that
// rejects both.
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

// An OPTIONAL switch is one a document may not mention. A gateway with no `host` is
// reachable only by being mounted inside another, which is the whole reason the field
// is optional — and it compiled clean while failing the schema published for it.
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
