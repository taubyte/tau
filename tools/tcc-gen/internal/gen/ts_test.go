package gen

import (
	"strings"
	"testing"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	v2 "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

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
		if !m.Bare {
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
		if m.Bare {
			continue // the scope container has no accessor class
		}
		if !strings.Contains(ts, "export class "+m.Spec+"Config extends ResourceConfig {") {
			t.Errorf("missing accessor class for %s", m.Spec)
		}
	}
}

func TestGenerateTSAccessors(t *testing.T) {
	out, err := GenerateTS(schema.CompileRoot())
	if err != nil {
		t.Fatal(err)
	}
	ts := string(out)

	for _, want := range []string{
		`export type PackageKind = "git" | "image" | "builtin";`,                       // a closed set becomes a union
		`export type PackageBuildKind = "steps" | "dockerfile";`,                       // and so does a block's switch
		`function(name: string, app?: string): FunctionConfig {`,                       // Session factory (app-scoped)
		`super(s, app ? ["namespace", app, "function", name] : ["function", name]);`,   // resource address
		`functionNames(app?: string): Promise<string[]> {`,                             // list
		`delete(): Promise<void> {`,                                                    // delete
		`applicationNames(app?: string): Promise<string[]> {`,                          // the container's list, named like every other lister
		"async kind(): Promise<PackageKind | undefined> {",                             // async + union
		`this.s.binding.get(this.s.handle, this.res, ["build", "dockerfile", "path"])`, // nested field path
		`setMemory(v: string): Promise<void> {`,                                        // setter
		"async memory(): Promise<string | undefined> {",                                // Bytes -> string (source form)
		`.catch(() => null)`, // a block read that tolerates absence
		`application(name: string, app?: string): ApplicationConfig {`, // the container is addressed like any resource
		`super(s, ["project"]);`,                                                 // the project root's own document
		`resourceAt(path: string): Promise<string[] | null> {`,                   // path -> address
		`serialize(): Promise<SerializedResource> {`,                             // one resource's file + YAML
		`setDoc(doc: Record<string, unknown>): Promise<void> {`,                  // whole-document diff
		`generate(field: string[]): Promise<string> {`,                           // DSL-minted values
		`address(kind: string, name: string, app?: string): Promise<string[]> {`, // kind -> address
		`names(kind: string, app?: string): Promise<string[]> {`,                 // kind -> instances
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

// TWO ATTRIBUTES ON ONE ACCESSOR: the later one is named by its whole path, so the
// earlier keeps its name and neither is dropped; one that clashes even then is
// refused.
func TestTSAClashingAccessorIsNamedByItsPath(t *testing.T) {
	n := &engine.Node{Attributes: []*engine.Attribute{
		engine.String("name"),
		engine.String(engine.Key("dead_letter", "name"), engine.Path("dead_letter", "name")),
	}}
	enums := map[string]tsEnum{}
	var order []string
	fields, err := tsFieldsOf(n, "queues", "Queue", enums, &order)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range fields {
		names = append(names, f.name)
	}
	if strings.Join(names, ",") != "name,deadLetterName" {
		t.Fatalf("accessors = %v, want [name deadLetterName]", names)
	}

	clash := &engine.Node{Attributes: []*engine.Attribute{
		engine.String("deadLetterName", engine.Accessor("DeadLetterName")),
		engine.String("name"),
		engine.String(engine.Key("dead_letter", "name"), engine.Path("dead_letter", "name")),
		engine.String(engine.Key("dead", "letter_name"), engine.Path("dead", "letter_name")),
	}}
	if _, err := tsFieldsOf(clash, "queues", "Queue", enums, &order); err == nil || !strings.Contains(err.Error(), "both come out as the accessor") {
		t.Fatalf("a clash its path cannot settle must be refused, got %v", err)
	}
}
