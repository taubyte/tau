package gen

import (
	"strings"
	"testing"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v1/schema"
)

// The TS emitter must produce an accessor class for every resource the Go
// generator covers — they share the DSL walk, so any drift is a bug.
func TestGenerateTSCoversResources(t *testing.T) {
	models, err := Structs(schema.TaubyteRessources)
	if err != nil {
		t.Fatal(err)
	}
	out, err := GenerateTS(schema.TaubyteRessources)
	if err != nil {
		t.Fatal(err)
	}
	ts := string(out)

	if got := strings.Count(ts, "export class "); got != len(models) {
		t.Fatalf("class count: got %d, want %d", got, len(models))
	}
	for _, m := range models {
		if !strings.Contains(ts, "export class "+m.Spec+"Config {") {
			t.Errorf("missing accessor class for %s", m.Spec)
		}
	}
}

// Accessors must map the flat field to its nested config path, type it, and
// expose InSet fields as unions.
func TestGenerateTSAccessorsAndUnions(t *testing.T) {
	out, err := GenerateTS(schema.TaubyteRessources)
	if err != nil {
		t.Fatal(err)
	}
	ts := string(out)

	for _, want := range []string{
		`export type FunctionType = "http" | "https" | "pubsub" | "p2p";`,
		"get type(): FunctionType | undefined",
		`return getPath(this.data, ["trigger", "type"]);`, // flat field -> nested key
		`setPath(this.data, ["execution", "memory"], v);`, // depth-2 setter
		"get memory(): number | undefined",
	} {
		if !strings.Contains(ts, want) {
			t.Errorf("generated TS missing: %s", want)
		}
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
