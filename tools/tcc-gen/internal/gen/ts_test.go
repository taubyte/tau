package gen

import (
	"strings"
	"testing"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v1/schema"
)

// The TS emitter must produce an accessor class + a Session factory for every
// resource the Go generator covers — they share the DSL walk.
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

	// one class per resource, plus Session
	if got := strings.Count(ts, "export class "); got != len(models)+1 {
		t.Fatalf("class count: got %d, want %d", got, len(models)+1)
	}
	if !strings.Contains(ts, "export class Session {") {
		t.Error("missing Session class")
	}
	for _, m := range models {
		if !strings.Contains(ts, "export class "+m.Spec+"Config {") {
			t.Errorf("missing accessor class for %s", m.Spec)
		}
	}
}

// Accessors must address (resource, field) by path, be async, type InSet fields
// as unions, and read the legacy key as a fallback.
func TestGenerateTSAccessors(t *testing.T) {
	out, err := GenerateTS(schema.TaubyteRessources)
	if err != nil {
		t.Fatal(err)
	}
	ts := string(out)

	for _, want := range []string{
		`export type FunctionType = "http" | "https" | "pubsub" | "p2p";`,
		`function(name: string): FunctionConfig {`,                                    // Session factory
		`this.res = ["functions", name];`,                                             // resource path
		"async type(): Promise<FunctionType | undefined> {",                           // async + union
		`this.s.binding.get(this.s.handle, this.res, ["trigger", "type"])`,            // field path
		`this.s.binding.set(this.s.handle, this.res, ["execution", "memory"]`,         // setter path
		"async memory(): Promise<string | undefined> {",                               // Bytes -> string (source form)
		`(await this.s.binding.get(this.s.handle, this.res, ["trigger", "service"]))`, // compat fallback
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
