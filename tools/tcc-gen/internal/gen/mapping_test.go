package gen

import (
	"strings"
	"testing"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v1/schema"
)

// The mapping is the load-bearing logic: a wrong name or path silently generates
// an accessor that reads/writes the wrong config key. These cases lock the tricky
// rules — canonical Path (not Compat) for name+body, overrides, depth, type.
func TestMapping(t *testing.T) {
	cases := []struct {
		desc     string
		group    string
		attr     *engine.Attribute
		wantName string
		wantSet  string // "" => setter not applicable at this depth
		wantGet  string
	}{
		{
			desc:     "plain single segment",
			group:    "databases",
			attr:     engine.String("match"),
			wantName: "Match",
			wantSet:  `return basic.Set("match", value)`,
			wantGet:  `return basic.Get[string](g, "match")`,
		},
		{
			desc:     "name and body from nested Path",
			group:    "functions",
			attr:     engine.String("type", engine.Path("trigger", "type")),
			wantName: "Type",
			wantSet:  `return basic.SetChild("trigger", "type", value)`,
			wantGet:  `return basic.Get[string](g, "trigger", "type")`,
		},
		{
			desc:     "canonical Path wins over Compat for name+body",
			group:    "functions",
			attr:     engine.String("p2p-protocol", engine.Path("trigger", "protocol"), engine.Compat("trigger", "service")),
			wantName: "Protocol",
			wantSet:  `return basic.SetChild("trigger", "protocol", value)`,
			wantGet:  `return basic.Get[string](g, "trigger", "protocol")`,
		},
		{
			desc:     "name override",
			group:    "domains",
			attr:     engine.String("fqdn"),
			wantName: "FQDN",
			wantSet:  `return basic.Set("fqdn", value)`,
			wantGet:  `return basic.Get[string](g, "fqdn")`,
		},
		{
			desc:     "int type",
			group:    "databases",
			attr:     engine.Int("replicas-min", engine.Path("replicas", "min")),
			wantName: "Min",
			wantSet:  `return basic.SetChild("replicas", "min", value)`,
			wantGet:  `return basic.Get[int](g, "replicas", "min")`,
		},
		{
			desc:     "depth-3 getter (variadic), no setter",
			group:    "messaging",
			attr:     engine.Bool("mqtt", engine.Path("bridges", "mqtt", "enable")),
			wantName: "MQTT",
			wantSet:  "", // depth 3 exceeds basic.SetChild
			wantGet:  `return basic.Get[bool](g, "bridges", "mqtt", "enable")`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			if got := accessorName(tc.group, tc.attr); got != tc.wantName {
				t.Errorf("name = %q, want %q", got, tc.wantName)
			}
			segs, ok := pathSegs(tc.attr)
			if !ok {
				t.Fatalf("pathSegs not resolvable for %q", tc.attr.Name)
			}
			gt := goType(tc.attr.Type)
			if got := getBody(gt, segs); got != tc.wantGet {
				t.Errorf("getBody = %q, want %q", got, tc.wantGet)
			}
			if tc.wantSet != "" {
				if len(segs) > 2 {
					t.Fatalf("expected a setter but body depth is %d", len(segs))
				}
				if got := setBody(segs); got != tc.wantSet {
					t.Errorf("setBody = %q, want %q", got, tc.wantSet)
				}
			}
		})
	}
}

// TestCompatAlias locks the compat behaviour: the canonical getter always reads
// path-then-compat so legacy on-disk data still reads; a Compat with a distinct
// name ALSO yields a separate deprecated accessor for callers of the old name.
func TestCompatAlias(t *testing.T) {
	// Distinct alias: p2p-protocol -> Protocol (canonical, with fallback) + Service (deprecated).
	fns := resourceByGroup(t, "functions")
	protocol := findGetter(t, fns, "Protocol")
	if protocol.Doc != "" {
		t.Errorf("Protocol getter should not be deprecated, got doc %q", protocol.Doc)
	}
	for _, want := range []string{`g.Config().Get("trigger").Get("protocol")`, `return basic.Get[string](g, "trigger", "service")`} {
		if !strings.Contains(protocol.Body, want) {
			t.Errorf("Protocol getter body missing %q; got:\n%s", want, protocol.Body)
		}
	}
	assertHasGetter(t, fns, "Service", "// Deprecated: use Protocol.", `return basic.Get[string](g, "trigger", "service")`)
	assertHasSetter(t, fns, "Service", "// Deprecated: use Protocol.", `return basic.SetChild("trigger", "service", value)`)

	// Same-named compat: website paths -> single Paths getter with compat fallback.
	web := resourceByGroup(t, "websites")
	g := findGetter(t, web, "Paths")
	if g.Doc != "" {
		t.Errorf("Paths getter should not be deprecated, got doc %q", g.Doc)
	}
	for _, want := range []string{`g.Config().Get("paths")`, `return basic.Get[[]string](g, "source", "paths")`} {
		if !strings.Contains(g.Body, want) {
			t.Errorf("Paths getter body missing %q; got:\n%s", want, g.Body)
		}
	}
	if findGetterOK(web, "SourcePaths") != nil {
		t.Error("same-named compat must not emit a separate alias accessor")
	}
}

func resourceByGroup(t *testing.T, group string) *Resource {
	t.Helper()
	rs, err := Resources(schema.TaubyteRessources)
	if err != nil {
		t.Fatal(err)
	}
	var pkg string
	for _, g := range schema.TaubyteRessources {
		if name, _ := g.Match.(string); name == group && len(g.Children) > 0 {
			if d, ok := descriptorFor(g.Children[0]); ok {
				pkg = d.Package
			}
		}
	}
	for _, r := range rs {
		if r.Package == pkg {
			return r
		}
	}
	t.Fatalf("resource for group %q not generated", group)
	return nil
}

func findGetter(t *testing.T, r *Resource, name string) Accessor {
	t.Helper()
	if a := findGetterOK(r, name); a != nil {
		return *a
	}
	t.Fatalf("%s has no getter %q", r.Package, name)
	return Accessor{}
}

func findGetterOK(r *Resource, name string) *Accessor {
	for i := range r.Getters {
		if r.Getters[i].Name == name {
			return &r.Getters[i]
		}
	}
	return nil
}

func assertHasGetter(t *testing.T, r *Resource, name, doc, body string) {
	t.Helper()
	a := findGetter(t, r, name)
	if a.Doc != doc {
		t.Errorf("%s.%s getter doc = %q, want %q", r.Package, name, a.Doc, doc)
	}
	if a.Body != body {
		t.Errorf("%s.%s getter body = %q, want %q", r.Package, name, a.Body, body)
	}
}

func assertHasSetter(t *testing.T, r *Resource, name, doc, body string) {
	t.Helper()
	for _, a := range r.Setters {
		if a.Name == name {
			if a.Doc != doc {
				t.Errorf("%s.%s setter doc = %q, want %q", r.Package, name, a.Doc, doc)
			}
			if a.Body != body {
				t.Errorf("%s.%s setter body = %q, want %q", r.Package, name, a.Body, body)
			}
			return
		}
	}
	t.Fatalf("%s has no setter %q", r.Package, name)
}

// TestGenerateParses ensures every emitted file is valid, gofmt-able Go.
func TestGenerateParses(t *testing.T) {
	files, err := Generate(schema.TaubyteRessources)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no files generated")
	}
	for rel, b := range files {
		if !strings.HasSuffix(rel, ".go") {
			continue // non-Go generated output (e.g. schema.ts)
		}
		if _, err := funcNames(rel, b); err != nil {
			t.Errorf("%s does not parse: %v", rel, err)
		}
	}
}
