package gen

import (
	"strings"
	"testing"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	v2 "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
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

	// One accessor class per RESOURCE, plus Session, the shared ResourceConfig
	// base, and the two documents that are resources without being a resource
	// GROUP: the container (Application) and the project root.
	resources := 0
	for _, m := range models {
		if m.SpecImport != "" {
			resources++
		}
	}
	if got := strings.Count(ts, "export class "); got != resources+4 {
		t.Fatalf("class count: got %d, want %d", got, resources+4)
	}
	for _, want := range []string{
		"export class Session {",
		"export class ResourceConfig {",                         // the shared, kind-agnostic surface
		"export class ApplicationConfig extends ResourceConfig", // the container is addressable too
		"export class ProjectConfig extends ResourceConfig",     // ...and so is the project root
	} {
		if !strings.Contains(ts, want) {
			t.Errorf("missing %s", want)
		}
	}
	for _, m := range models {
		if m.SpecImport == "" {
			continue // bare container struct — no accessor class
		}
		if !strings.Contains(ts, "export class "+m.Spec+"Config extends ResourceConfig {") {
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
		`function(name: string, app?: string): FunctionConfig {`,                          // Session factory (app-scoped)
		`super(s, app ? ["applications", app, "functions", name] : ["functions", name]);`, // resource address
		`functionNames(app?: string): Promise<string[]> {`,                                // list
		`delete(): Promise<void> {`,                                                       // delete
		`applicationNames(): Promise<string[]> {`,                                         // the container's list, named like every other lister
		"async type(): Promise<FunctionType | undefined> {",                               // async + union
		`this.s.binding.get(this.s.handle, this.res, ["trigger", "type"])`,                // field path
		`this.s.binding.set(this.s.handle, this.res, ["execution", "memory"]`,             // setter path
		"async memory(): Promise<string | undefined> {",                                   // Bytes -> string (source form)
		`(await this.s.binding.get(this.s.handle, this.res, ["trigger", "service"]))`,     // compat fallback
		`application(name: string): ApplicationConfig {`,                                  // the container is addressed like any resource
		`super(s, ["config"]);`,                                                           // the project root's own document
		`resourceAt(path: string): Promise<string[] | null> {`,                            // path -> address
		`serialize(): Promise<SerializedResource> {`,                                      // one resource's file + YAML
		`setDoc(doc: Record<string, unknown>): Promise<void> {`,                           // whole-document diff
		`generate(field: string[]): Promise<string> {`,                                    // DSL-minted values
		`address(kind: string, name: string, app?: string): Promise<string[]> {`,          // kind -> address
		`names(kind: string, app?: string): Promise<string[]> {`,                          // kind -> instances
		`async resource(kind: string, name: string, app?: string): Promise<ResourceConfig> {`,
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

// Nothing had ever run a compiler over this emitter's output, and it showed: an
// attribute whose key is nested (`Key("serve", "kind")`) names itself with the
// "/" that separates the levels of the compiled object, and the emitter used
// that name verbatim — `async serve/kind()`, `type ChannelServe/kind`, neither
// of which parses. The check is total rather than a list of known names: strip
// TypeScript's comments and string literals, and a "/" left anywhere in what
// remains is a path that got emitted where an identifier belongs.
func TestGeneratedTSEmitsOnlyLegalIdentifiers(t *testing.T) {
	for _, dsl := range []struct {
		name string
		root *engine.Node
	}{
		{"v1", schema.CompileRoot()},
		{"v2", v2.CompileRoot()},
	} {
		t.Run(dsl.name, func(t *testing.T) {
			out, err := GenerateTS(dsl.root)
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(stripTSCommentsAndStrings(string(out)), "\n") {
				if strings.Contains(line, "/") {
					t.Errorf("emitted a path where an identifier belongs: %s", strings.TrimSpace(line))
				}
			}
		})
	}
}

// stripTSCommentsAndStrings blanks out every comment and string literal, leaving
// the code that has to be identifiers and syntax. The emitter writes no division
// and no regex literal, so a surviving "/" is always a defect.
func stripTSCommentsAndStrings(src string) string {
	var b strings.Builder
	for i := 0; i < len(src); {
		switch {
		case strings.HasPrefix(src[i:], "//"):
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				i = len(src)
				break
			}
			// keep the newlines so a reported line number still means something
			for _, r := range src[i : i+2+end+2] {
				if r == '\n' {
					b.WriteByte('\n')
				}
			}
			i += 2 + end + 2
		case src[i] == '"' || src[i] == '\'' || src[i] == '`':
			quote := src[i]
			i++
			for i < len(src) && src[i] != quote {
				if src[i] == '\\' {
					i++
				}
				i++
			}
			i++
		default:
			b.WriteByte(src[i])
			i++
		}
	}
	return b.String()
}

// A node may hold SEVERAL switches: a database has a type and one engine key per
// type, an application has a readiness probe and a liveness probe. The emitter
// used to compute one alias per node, so the last switch named them all —
// sqlEngine() came back typed by the admission key, and a caller who obeyed the
// types would pass "validator" where an engine belongs. Each switch names itself.
func TestTSSwitchAliasIsPerSwitch(t *testing.T) {
	n := &engine.Node{Attributes: []*engine.Attribute{
		engine.String(engine.Key("readiness", "kind"), engine.Path("readiness", engine.Either("http", "function")), engine.Captures()),
		engine.String(engine.Key("readiness", "path"), engine.Path("readiness", engine.Either("http"), "path")),
		engine.String(engine.Key("liveness", "kind"), engine.Path("liveness", engine.Either("tcp", "exec")), engine.Captures()),
		engine.String(engine.Key("liveness", "port"), engine.Path("liveness", engine.Either("tcp"), "port")),
	}}

	enums := map[string]tsEnum{}
	var order []string
	fields, err := tsFieldsOf(n, "probes", "Probe", enums, &order)
	if err != nil {
		t.Fatal(err)
	}

	byName := map[string]tsField{}
	for _, f := range fields {
		byName[f.name] = f
	}
	for _, want := range []struct{ field, alias, param string }{
		{"readinessKind", "ProbeReadinessKind", "readinessKind"},
		{"readinessPath", "ProbeReadinessKind", "readinessKind"},
		{"livenessKind", "ProbeLivenessKind", "livenessKind"},
		{"livenessPort", "ProbeLivenessKind", "livenessKind"},
	} {
		f, ok := byName[want.field]
		if !ok {
			t.Errorf("no field %s in %v", want.field, byName)
			continue
		}
		if f.alias != want.alias || f.param != want.param {
			t.Errorf("%s: alias/param = %s/%s, want %s/%s", want.field, f.alias, f.param, want.alias, want.param)
		}
	}
	// ...and the two aliases are two types, each carrying its own switch's values.
	if got := enums["ProbeReadinessKind"].values; strings.Join(got, ",") != "http,function" {
		t.Errorf("ProbeReadinessKind = %v, want [http function]", got)
	}
	if got := enums["ProbeLivenessKind"].values; strings.Join(got, ",") != "tcp,exec" {
		t.Errorf("ProbeLivenessKind = %v, want [tcp exec]", got)
	}
}

// A union of values is a union of LITERALS. Only a number is a bare one in
// TypeScript; "GET" emitted as GET names a type that does not exist. The engine's
// own InSet annotates the string case, but a vocabulary's set-validator over a
// StringSlice need not know the annotation exists, so the quoting is read off the
// attribute's declared type instead.
func TestTSEnumLiteralsAreQuotedByType(t *testing.T) {
	n := &engine.Node{Attributes: []*engine.Attribute{
		engine.StringSlice("methods", engine.Annotate("enum", []string{"GET", "POST"})),
		engine.Int("replicas", engine.InSet(1, 3, 5)),
	}}
	enums := map[string]tsEnum{}
	var order []string
	if _, err := tsFieldsOf(n, "routes", "Route", enums, &order); err != nil {
		t.Fatal(err)
	}
	if !enums["RouteMethods"].quoted {
		t.Error("a string-valued enum must be emitted as string literals")
	}
	if enums["RouteReplicas"].quoted {
		t.Error("a numeric enum must be emitted as number literals")
	}
}
