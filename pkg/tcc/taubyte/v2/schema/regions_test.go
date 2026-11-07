package schema_test

import (
	"context"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// onlyNamed places a resource exactly where its namespace says, and nowhere when it
// says nothing: the smallest Regions that can leave something unplaced. It records
// every placement it is asked about, so a test can see what the audit computed.
type onlyNamed struct{ asked *[]schema.Placement }

func (o onlyNamed) Place(p schema.Placement) schema.Answer {
	if o.asked != nil {
		*o.asked = append(*o.asked, p)
	}
	return schema.Answer{In: p.In}
}

func (onlyNamed) Allow([]string) []schema.Concern { return nil }
func (onlyNamed) Serves() []string                { return []string{"region-1", "region-2"} }

func compileDocs(t *testing.T, env string, docs map[string]string) map[string]any {
	t.Helper()
	fs := afero.NewMemMapFs()
	for path, body := range withPackages(docs) {
		require.NoError(t, afero.WriteFile(fs, path, []byte(body), 0o644))
	}
	opts := []schema.Option{schema.WithVirtual(fs, "/"), schema.WithBranch("main")}
	if env != "" {
		opts = append(opts, schema.WithEnvironments("production", "preview"), schema.WithEnvironment(env))
	}
	c, err := schema.New(opts...)
	require.NoError(t, err)
	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err)
	return obj.Flat()
}

func auditWith(t *testing.T, r schema.Regions, docs map[string]string) []schema.Finding {
	t.Helper()
	return schema.Audit(compileDocs(t, "", docs), nil, schema.WithRegions(r))
}

const (
	regionsProject = "id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nname: p\n"
	everyBranch    = "environments:\n    prod:\n        branches: [\"*\"]\n"
	regionsShop    = "id: QmQPeNsJPyVWPFDVHb77w8G42Fvo15z4bmmjRoBHfW1YZv\n"
	regionsShopEU  = "id: QmTXGKe63GYuKA54yerPQmGyEtFjhUF5p1g2hahTpm6pkA\n"
)

// regionsBlock is a scope's `regions`, either field omitted when empty.
func regionsBlock(topology, in string) string {
	out := "regions:\n"
	if topology != "" {
		out += "    topology: " + topology + "\n"
	}
	if in != "" {
		out += "    in: " + in + "\n"
	}
	return out
}

func regionsCache(block string) string {
	return "id: QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB\nsize: 1MB\nconsensus: crdt\n" + block
}

func findingsTitled(found []schema.Finding, sev schema.Severity) []string {
	var out []string
	for _, f := range found {
		if f.Severity == sev {
			out = append(out, f.Kind+"/"+f.Name+": "+f.Title)
		}
	}
	return out
}

func TestNoRegionsRefusesAndPlacesInDefault(t *testing.T) {
	r := schema.WithNoRegions()

	// Every placement lands in the one region; a scope naming others is refused by
	// Allow, once, rather than once for each resource under it.
	for _, p := range []schema.Placement{{}, {Topology: schema.TopologyActive, In: []string{"region-1", "region-2"}}, {In: []string{"default"}}} {
		got := r.Place(p)
		require.Equal(t, []string{schema.DefaultRegion}, got.In)
		require.Empty(t, got.Concerns)
	}

	require.Empty(t, r.Allow(nil))
	require.Empty(t, r.Allow([]string{"default"}))
	allow := r.Allow([]string{"region-1", "region-1", "default"})
	require.Len(t, allow, 1)
	require.Equal(t, schema.Broken, allow[0].Severity)
	require.Equal(t, `allows region "region-1", which this build does not serve; everything under it runs in "default"`, allow[0].Title)
	require.Contains(t, allow[0].Detail, "serves one region")
}

func TestTheAuditHearsNoRegionsByDefault(t *testing.T) {
	docs := map[string]string{
		"/project.yaml": regionsProject + regionsBlock("", "[region-1]") +
			"environments:\n    production:\n        branches: [main]\n        regions: [region-1]\n",
		"/namespace/shop/meta.yaml":           regionsShop + regionsBlock("", "[region-1]"),
		"/namespace/shop/cache/sessions.yaml": regionsCache(""),
	}
	obj := compileDocs(t, "", docs)

	want := []string{
		`project/p: allows region "region-1", which this build does not serve; everything under it runs in "default"`,
		`namespace/shop: allows region "region-1", which this build does not serve; everything under it runs in "default"`,
		`environments/production: allows region "region-1", which this build does not serve; everything under it runs in "default"`,
	}
	// THE DEFAULT REFUSES: no validator, a nil one, and WithNoRegions are one answer.
	for _, found := range [][]schema.Finding{
		schema.Audit(obj, nil),
		schema.Audit(obj, nil, schema.WithRegions(nil)),
		schema.Audit(obj, nil, schema.WithRegions(schema.WithNoRegions())),
	} {
		require.ElementsMatch(t, want, findingsTitled(found, schema.Broken))
	}
	require.ElementsMatch(t, want, findingsTitled(schema.Place(obj), schema.Broken))

	// Nothing named anywhere: nothing to say, and nothing broken.
	clean := compileDocs(t, "", map[string]string{
		"/project.yaml": regionsProject + everyBranch,
		"/cache/c.yaml": regionsCache(""),
	})
	require.Empty(t, schema.Place(clean))
}

func TestThePlacementTheRegionsIsAsked(t *testing.T) {
	docs := map[string]string{
		"/project.yaml": regionsProject + regionsBlock("active", "[region-1, region-2]") +
			"environments:\n    production:\n        branches: [main]\n        regions: [region-2]\n" +
			"    preview:\n        branches: [\"*\"]\n",
		"/namespace/shop/meta.yaml":     regionsShop + regionsBlock("failover", "[region-1, region-2]"),
		"/namespace/shop/cache/c.yaml":  regionsCache(""),
		"/cache/root.yaml":              "id: QmVs4sQjPFqJZ7Ug5MPpFxPTNiZ1CT8xY4XPKUbnDNbNAY\nsize: 1MB\nconsensus: crdt\n",
		"/namespace/shop/secret/s.yaml": "id: QmWATWQ7fVPP2EFGu71UkfnqhYXDYH566qy47CnJDgvs8u\nsource: value\nrotation: 720h\n",
	}

	var asked []schema.Placement
	schema.Audit(compileDocs(t, "production", docs), nil, schema.AuditIn("production"),
		schema.WithRegions(onlyNamed{&asked}))

	require.Equal(t, []schema.Placement{
		{Topology: "single", In: []string{"region-2"}},
		{Topology: "single", In: []string{"region-2"}},
	}, asked, "two caches are placed and a secret is not; production's region-2 narrows shop's failover and the root's active to one region, which each runs in alone")

	// Told no environment, it is the chain alone; an environment with no regions of
	// its own narrows nothing.
	for _, env := range []string{"", "preview"} {
		asked = nil
		schema.Audit(compileDocs(t, env, docs), nil, schema.AuditIn(env), schema.WithRegions(onlyNamed{&asked}))
		for _, p := range asked {
			require.Equal(t, []string{"region-1", "region-2"}, p.In, env)
		}
	}

	// A chain and an environment that share nothing place nothing — an empty list,
	// not a missing one.
	asked = nil
	schema.Audit(compileDocs(t, "production", map[string]string{
		"/project.yaml": regionsProject + regionsBlock("", "[region-1]") +
			"environments:\n    production:\n        branches: [main]\n        regions: [region-2]\n" +
			"    preview:\n        branches: [\"*\"]\n",
		"/cache/c.yaml": regionsCache(""),
	}), nil, schema.AuditIn("production"), schema.WithRegions(onlyNamed{&asked}))
	require.Equal(t, []schema.Placement{{Topology: "single", In: []string{}}}, asked)

	// A project whose only regions are its environment's runs in those.
	asked = nil
	schema.Audit(compileDocs(t, "production", map[string]string{
		"/project.yaml": regionsProject +
			"environments:\n    production:\n        branches: [main]\n        regions: [region-2]\n" +
			"    preview:\n        branches: [\"*\"]\n",
		"/cache/c.yaml": regionsCache(""),
	}), nil, schema.AuditIn("production"), schema.WithRegions(onlyNamed{&asked}))
	require.Equal(t, []schema.Placement{{In: []string{"region-2"}}}, asked)
}

// Each field of a scope's block is its own or its parent's, and what it resolves to
// says how the scope runs.
func TestATopologyNestsField(t *testing.T) {
	type testCase struct {
		name         string
		root, shop   string
		eu           string // shop/eu, nested
		broken, note []string
	}
	for _, tc := range []testCase{
		{name: "a child inherits both fields", root: regionsBlock("active", "[region-1, region-2, region-3]")},
		{name: "a child picks its own topology within its parent's regions",
			root: regionsBlock("active", "[region-1, region-2, region-3]"),
			shop: regionsBlock("failover", "[region-2, region-1]"),
			eu:   regionsBlock("single", "[region-1]")},
		{name: "a child widening its parent", root: regionsBlock("active", "[region-1, region-2]"),
			shop:   regionsBlock("", "[region-1, region-3]"),
			broken: []string{`namespace/shop: runs in region "region-3", which the scope above it does not`}},
		{name: "several regions and no topology", root: regionsBlock("", "[region-1, region-2]"),
			broken: []string{"project/p: runs in 2 regions and does not say how"}},
		{name: "a topology with no regions", root: regionsBlock("active", ""),
			broken: []string{"project/p: is active and names no region"}},
		{name: "single across several", root: regionsBlock("single", "[region-1, region-2]"),
			broken: []string{"project/p: is single and runs in 2 regions"}},
		{name: "a child single over its parent's several", root: regionsBlock("active", "[region-1, region-2]"),
			shop:   regionsBlock("single", ""),
			broken: []string{"namespace/shop: is single and runs in 2 regions"}},
		{name: "failover written with one region", root: regionsBlock("failover", "[region-1]"),
			broken: []string{"project/p: fails over with one region"}},
		{name: "a child narrowing a failover to one runs it single",
			root: regionsBlock("failover", "[region-1, region-2]"),
			shop: regionsBlock("", "[region-2]"),
			note: []string{`namespace/shop: is failover above and runs in "region-2" alone`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs := map[string]string{
				"/project.yaml":                          regionsProject + tc.root + everyBranch,
				"/namespace/shop/meta.yaml":              regionsShop + tc.shop,
				"/namespace/shop/namespace/eu/meta.yaml": regionsShopEU + tc.eu,
			}
			found := auditWith(t, schema.Preview("region-1", "region-2", "region-3"), docs)
			require.ElementsMatch(t, tc.broken, findingsTitled(found, schema.Broken))
			var notes []string
			for _, f := range found {
				if f.Severity == schema.Note && (f.Kind == "project" || f.Kind == "namespace") {
					notes = append(notes, f.Kind+"/"+f.Name+": "+f.Title)
				}
			}
			require.ElementsMatch(t, tc.note, notes)
		})
	}
}

func TestAnEnvironmentOfOneRegionRunsATopologySingle(t *testing.T) {
	docs := map[string]string{
		"/project.yaml": regionsProject + regionsBlock("active", "[region-1, region-2]") +
			"environments:\n    production:\n        branches: [main]\n" +
			"    preview:\n        branches: [\"*\"]\n        regions: [region-1]\n",
		"/cache/c.yaml": regionsCache(""),
	}
	found := schema.Audit(compileDocs(t, "preview", docs), nil, schema.AuditIn("preview"),
		schema.WithRegions(schema.Preview("region-1", "region-2")))
	require.Empty(t, findingsTitled(found, schema.Broken))
	require.Contains(t, findingsTitled(found, schema.Note), `project/p: is active and runs in "region-1" alone in preview`)

	found = schema.Audit(compileDocs(t, "production", docs), nil, schema.AuditIn("production"),
		schema.WithRegions(schema.Preview("region-1", "region-2")))
	require.Empty(t, findingsTitled(found, schema.Note))
}

func TestATopologyVariesPerEnvironment(t *testing.T) {
	docs := map[string]string{
		"/project.yaml": regionsProject +
			"regions:\n    topology: active\n    in: [region-1, region-2]\n    topology_preview: single\n    in_preview: [region-1]\n" +
			"environments:\n    production:\n        branches: [main]\n    preview:\n        branches: [\"*\"]\n",
		"/cache/c.yaml": regionsCache(""),
	}
	for env, want := range map[string]schema.Placement{
		"production": {Topology: "active", In: []string{"region-1", "region-2"}},
		"preview":    {Topology: "single", In: []string{"region-1"}},
	} {
		var asked []schema.Placement
		found := schema.Audit(compileDocs(t, env, docs), nil, schema.AuditIn(env), schema.WithRegions(onlyNamed{&asked}))
		require.Empty(t, findingsTitled(found, schema.Broken), env)
		require.Equal(t, []schema.Placement{want}, asked, env)
	}
}

// A REGIONS THAT REFUSES IS HEARD AS IT SPOKE: its severity, against the resource.
type refuses struct{}

func (refuses) Place(p schema.Placement) schema.Answer {
	return schema.Answer{In: p.In, Concerns: []schema.Concern{{schema.Broken, "is refused", "because"}}}
}
func (refuses) Serves() []string { return []string{"region-1"} }
func (refuses) Allow([]string) []schema.Concern {
	return []schema.Concern{{schema.Broken, "allowance refused", "because"}}
}

func TestTheAuditFilesWhatTheRegionsSays(t *testing.T) {
	found := auditWith(t, refuses{}, map[string]string{
		"/project.yaml": regionsProject + regionsBlock("", "[region-1]") + everyBranch,
		"/cache/c.yaml": regionsCache(""),
	})
	require.Contains(t, findingsTitled(found, schema.Broken), "cache/c: is refused")
	require.Contains(t, findingsTitled(found, schema.Broken), "project/p: allowance refused")
}

func TestWithRegionIsOneRegionOfMany(t *testing.T) {
	r := schema.WithRegion("region-1")
	require.Equal(t, []string{"region-1"}, r.Serves())
	require.Empty(t, r.Allow([]string{"region-1", "region-2"}))

	type testCase struct {
		p    schema.Placement
		want []string
	}
	for _, tc := range []testCase{
		{schema.Placement{}, []string{"region-1"}},
		{schema.Placement{Topology: "single", In: []string{"region-2"}}, []string{"region-2"}},
		{schema.Placement{Topology: "failover", In: []string{"region-1", "region-2"}}, []string{"region-1", "region-2"}},
		{schema.Placement{Topology: "active", In: []string{"region-2", "region-3"}}, []string{"region-2", "region-3"}},
	} {
		got := r.Place(tc.p)
		require.Equal(t, tc.want, got.In, "%+v", tc.p)
		require.Empty(t, got.Concerns)
	}

	// A STANDBY IS SAID TO SERVE, since nothing here holds it back.
	got := schema.WithRegion("region-2").Place(schema.Placement{Topology: "failover", In: []string{"region-1", "region-2"}})
	require.Len(t, got.Concerns, 1)
	require.Equal(t, schema.Check, got.Concerns[0].Severity)
	require.Equal(t, `stands by in "region-2", and this build answers from a standby as from a primary`, got.Concerns[0].Title)
}

func TestRegionsNamedIsEveryScopeAndEnvironment(t *testing.T) {
	obj := compileDocs(t, "", map[string]string{
		"/project.yaml": regionsProject + regionsBlock("active", "[region-2, region-1]") +
			"environments:\n    production:\n        branches: [main]\n        regions: [region-3]\n",
		"/namespace/shop/meta.yaml": regionsShop + regionsBlock("", "[region-4]"),
	})
	require.Equal(t, []string{"region-1", "region-2", "region-3", "region-4"}, schema.RegionsNamed(obj))
}
