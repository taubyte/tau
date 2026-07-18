package gen

import (
	"strings"
	"testing"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v1/schema"
)

// The TS emitter must produce an accessor class + a Session factory for every
// resource the Go generator covers — they share the DSL walk.
func TestGenerateTSCoversResources(t *testing.T) {
	models, err := Structs(schema.GenerationRoot())
	if err != nil {
		t.Fatal(err)
	}
	out, err := GenerateTS(schema.GenerationRoot())
	if err != nil {
		t.Fatal(err)
	}
	ts := string(out)

	// One accessor class per RESOURCE (bare container structs like App have a
	// struct but no accessor class), plus Session.
	resources := 0
	for _, m := range models {
		if m.SpecImport != "" {
			resources++
		}
	}
	if got := strings.Count(ts, "export class "); got != resources+1 {
		t.Fatalf("class count: got %d, want %d", got, resources+1)
	}
	if !strings.Contains(ts, "export class Session {") {
		t.Error("missing Session class")
	}
	for _, m := range models {
		if m.SpecImport == "" {
			continue // bare container struct — no accessor class
		}
		if !strings.Contains(ts, "export class "+m.Spec+"Config {") {
			t.Errorf("missing accessor class for %s", m.Spec)
		}
	}
}

// Accessors must address (resource, field) by path, be async, type InSet fields
// as unions, and read the legacy key as a fallback.
func TestGenerateTSAccessors(t *testing.T) {
	out, err := GenerateTS(schema.GenerationRoot())
	if err != nil {
		t.Fatal(err)
	}
	ts := string(out)

	for _, want := range []string{
		`export type FunctionType = "http" | "https" | "pubsub" | "p2p";`,
		`function(name: string, app?: string): FunctionConfig {`, // Session factory (app-scoped)
		`: ["functions", name];`,                                                      // resource path (ternary fallback)
		`functionNames(app?: string): Promise<string[]> {`,                            // list
		`delete(): Promise<void> {`,                                                   // delete
		`applications(): Promise<string[]> {`,                                         // app list
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
