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
	out, err := GenerateTS(schema.CompileRoot())
	if err != nil {
		t.Fatal(err)
	}
	ts := string(out)

	// One accessor class per RESOURCE, plus Session, plus the two documents that
	// are resources without being a resource GROUP: the container (Application)
	// and the project root.
	resources := 0
	for _, m := range models {
		if m.SpecImport != "" {
			resources++
		}
	}
	if got := strings.Count(ts, "export class "); got != resources+3 {
		t.Fatalf("class count: got %d, want %d", got, resources+3)
	}
	for _, want := range []string{
		"export class Session {",
		"export class ApplicationConfig {", // the container is addressable too
		"export class ProjectConfig {",     // ...and so is the project root
	} {
		if !strings.Contains(ts, want) {
			t.Errorf("missing %s", want)
		}
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
	out, err := GenerateTS(schema.CompileRoot())
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
		`application(name: string): ApplicationConfig {`,                              // the container is addressed like any resource
		`this.res = ["config"];`,                                                      // the project root's own document
		`resourceAt(path: string): Promise<string[] | null> {`,                        // path -> address
		`serialize(): Promise<SerializedResource> {`,                                  // one resource's file + YAML
		`setDoc(doc: Record<string, unknown>): Promise<void> {`,                       // whole-document diff
		`generate(field: string[]): Promise<string> {`,                                // DSL-minted values
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
