package gen

import (
	"strings"
	"testing"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v1/schema"
)

// The TS emitter must cover exactly the same resources/fields as the Go struct
// proposals — they share the DSL walk, so any drift is a bug.
func TestGenerateTSMatchesStructs(t *testing.T) {
	models, err := Structs(schema.TaubyteRessources)
	if err != nil {
		t.Fatal(err)
	}
	out, err := GenerateTS(schema.TaubyteRessources)
	if err != nil {
		t.Fatal(err)
	}
	ts := string(out)

	if got := strings.Count(ts, "export interface "); got != len(models) {
		t.Fatalf("interface count: got %d, want %d", got, len(models))
	}
	for _, m := range models {
		if !strings.Contains(ts, "export interface "+m.Spec+" {") {
			t.Errorf("missing interface for resource %s", m.Spec)
		}
		for _, f := range m.Fields {
			opt := "?"
			if f.Required {
				opt = ""
			}
			want := "  " + tsName(f.Name) + opt + ": " + tsFieldType(f) + ";"
			if !strings.Contains(ts, want) {
				t.Errorf("%s: missing line %q", m.Spec, want)
			}
		}
	}

	// Idiomatic-TS spot checks: required id (no ?), optional camelCase field,
	// and an InSet union.
	if !strings.Contains(ts, "  id: string;") {
		t.Error("id should be required (no ?)")
	}
	if !strings.Contains(ts, `  type?: "http" | "https" | "pubsub" | "p2p";`) {
		t.Error("function trigger type should be an optional string union")
	}
	if !strings.Contains(ts, "  certType?: ") {
		t.Error("expected camelCased optional certType")
	}
}

func TestTsTypeMapping(t *testing.T) {
	cases := map[string]string{
		"string":   "string",
		"[]string": "string[]",
		"bool":     "boolean",
		"int":      "number",
		"uint64":   "number",
	}
	for in, want := range cases {
		if got := tsType(in); got != want {
			t.Errorf("tsType(%q) = %q, want %q", in, got, want)
		}
	}
}
