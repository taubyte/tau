package schema_test

import (
	"context"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// onlyNamed places a resource exactly where it says, and nowhere when it says nothing:
// the smallest Regions that can leave something unplaced. It records every placement it
// is asked about, so a test can see what the audit computed.
type onlyNamed struct{ asked *[]schema.Placement }

func (o onlyNamed) Place(p schema.Placement) schema.Answer {
	if o.asked != nil {
		*o.asked = append(*o.asked, p)
	}
	return schema.Answer{In: p.Named()}
}

func (onlyNamed) Allow([]string) []schema.Concern { return nil }
func (onlyNamed) Serves() []string                { return []string{"region-1", "region-2"} }

func compileDocs(t *testing.T, env string, docs map[string]string) map[string]any {
	t.Helper()
	fs := afero.NewMemMapFs()
	for path, body := range docs {
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
)

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
	r, severity := schema.WithNoRegions(), schema.Broken
	type testCase struct {
		name  string
		p     schema.Placement
		title string
	}
	for _, tc := range []testCase{
		{"pinned", schema.Placement{Policy: "pinned", In: []string{"region-1", "region-2"}},
			`is placed in regions "region-1", "region-2", which this build does not serve; it runs in "default"`},
		{"cdn", schema.Placement{Policy: "cdn", In: []string{"region-1"}},
			`is placed in region "region-1", which this build does not serve; it runs in "default"`},
		{"failover", schema.Placement{Policy: "failover", Main: "region-1", Standby: "region-2"},
			`is placed in regions "region-1", "region-2", which this build does not serve; it runs in "default"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := r.Place(tc.p)
			require.Equal(t, []string{schema.DefaultRegion}, got.In)
			require.Len(t, got.Concerns, 1)
			require.Equal(t, severity, got.Concerns[0].Severity)
			require.Equal(t, tc.title, got.Concerns[0].Title)
			require.Contains(t, got.Concerns[0].Detail, "serves one region")
		})
	}

	// Saying nothing, or naming the one region served, is nothing to warn about.
	for _, p := range []schema.Placement{{}, {Allowed: []string{"region-1"}}, {Policy: "pinned", In: []string{"default"}}} {
		got := r.Place(p)
		require.Equal(t, []string{schema.DefaultRegion}, got.In)
		require.Empty(t, got.Concerns)
	}

	require.Empty(t, r.Allow(nil))
	require.Empty(t, r.Allow([]string{"default"}))
	allow := r.Allow([]string{"region-1", "region-1", "default"})
	require.Len(t, allow, 1)
	require.Equal(t, severity, allow[0].Severity)
	require.Equal(t, `allows region "region-1", which this build does not serve; everything under it runs in "default"`, allow[0].Title)
}

func TestTheAuditHearsNoRegionsByDefault(t *testing.T) {
	docs := map[string]string{
		"/project.yaml": regionsProject + "regions: [region-1]\n" +
			"environments:\n    production:\n        branches: [main]\n        regions: [region-1]\n",
		"/namespace/shop/meta.yaml":           regionsShop + "regions: [region-1]\n",
		"/namespace/shop/cache/sessions.yaml": regionsCache("regions:\n    pinned: [region-1]\n"),
	}
	obj := compileDocs(t, "", docs)

	want := []string{
		`project/p: allows region "region-1", which this build does not serve; everything under it runs in "default"`,
		`namespace/shop: allows region "region-1", which this build does not serve; everything under it runs in "default"`,
		`environments/production: allows region "region-1", which this build does not serve; everything under it runs in "default"`,
		`cache/sessions: is placed in region "region-1", which this build does not serve; it runs in "default"`,
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
		"/project.yaml": regionsProject + "regions: [region-1, region-2]\n" +
			"environments:\n    production:\n        branches: [main]\n        regions: [region-2]\n" +
			"    preview:\n        branches: [\"*\"]\n",
		"/namespace/shop/meta.yaml":       regionsShop + "regions: [region-1, region-2]\n",
		"/namespace/shop/cache/c.yaml":    regionsCache("regions:\n    failover:\n        main: region-1\n        standby: region-2\n"),
		"/cache/root.yaml":                "id: QmVs4sQjPFqJZ7Ug5MPpFxPTNiZ1CT8xY4XPKUbnDNbNAY\nsize: 1MB\nconsensus: crdt\n",
		"/namespace/shop/secret/s.yaml":   "id: QmWATWQ7fVPP2EFGu71UkfnqhYXDYH566qy47CnJDgvs8u\nsource: value\nrotation: 720h\n",
		"/namespace/shop/cache/open.yaml": "id: QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG\nsize: 1MB\nconsensus: crdt\n",
	}

	var asked []schema.Placement
	schema.Audit(compileDocs(t, "production", docs), nil, schema.AuditIn("production"),
		schema.WithRegions(onlyNamed{&asked}))

	byPolicy := map[string]schema.Placement{}
	for _, p := range asked {
		byPolicy[p.Policy] = p
	}
	require.Len(t, asked, 3, "three caches are placed; a secret is not")
	require.Equal(t, schema.Placement{Policy: "failover", Main: "region-1", Standby: "region-2", Allowed: []string{"region-2"}},
		byPolicy["failover"], "shop's region-1+region-2, narrowed by production's region-2")
	require.Equal(t, []string{"region-2"}, byPolicy[""].Allowed)

	// Told no environment, the ceiling is the chain alone; an environment with no
	// regions of its own narrows nothing.
	for _, env := range []string{"", "preview"} {
		asked = nil
		schema.Audit(compileDocs(t, env, docs), nil, schema.AuditIn(env), schema.WithRegions(onlyNamed{&asked}))
		for _, p := range asked {
			if p.Policy == "failover" {
				require.Equal(t, []string{"region-1", "region-2"}, p.Allowed, env)
			}
		}
	}

	// A chain and an environment that share nothing allow nothing — an empty
	// ceiling, not a missing one.
	asked = nil
	schema.Audit(compileDocs(t, "production", map[string]string{
		"/project.yaml": regionsProject + "regions: [region-1]\n" +
			"environments:\n    production:\n        branches: [main]\n        regions: [region-2]\n" +
			"    preview:\n        branches: [\"*\"]\n",
		"/cache/c.yaml": regionsCache(""),
	}), nil, schema.AuditIn("production"), schema.WithRegions(onlyNamed{&asked}))
	require.Equal(t, []schema.Placement{{Allowed: []string{}}}, asked)

	// A project whose only ceiling is its environment's is bounded by that.
	asked = nil
	schema.Audit(compileDocs(t, "production", map[string]string{
		"/project.yaml": regionsProject +
			"environments:\n    production:\n        branches: [main]\n        regions: [region-2]\n" +
			"    preview:\n        branches: [\"*\"]\n",
		"/cache/c.yaml": regionsCache(""),
	}), nil, schema.AuditIn("production"), schema.WithRegions(onlyNamed{&asked}))
	require.Equal(t, []schema.Placement{{Allowed: []string{"region-2"}}}, asked)
}

// A REGIONS THAT REFUSES IS HEARD AS IT SPOKE: its severity, against the resource.
type refuses struct{}

func (refuses) Place(p schema.Placement) schema.Answer {
	return schema.Answer{In: p.Named(), Concerns: []schema.Concern{{schema.Broken, "is refused", "because"}}}
}
func (refuses) Serves() []string { return []string{"region-1"} }
func (refuses) Allow([]string) []schema.Concern {
	return []schema.Concern{{schema.Broken, "allowance refused", "because"}}
}

func TestTheAuditFilesWhatTheRegionsSays(t *testing.T) {
	found := auditWith(t, refuses{}, map[string]string{
		"/project.yaml": regionsProject + "regions: [region-1]\n" + everyBranch,
		"/cache/c.yaml": regionsCache("regions:\n    pinned: [region-1]\n"),
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
		{schema.Placement{Allowed: []string{"region-2"}}, []string{"region-2"}},
		{schema.Placement{Policy: "pinned", In: []string{"region-2"}}, []string{"region-2"}},
		{schema.Placement{Policy: "failover", Main: "region-1", Standby: "region-2"}, []string{"region-1", "region-2"}},
		{schema.Placement{Policy: "cdn", In: []string{"region-2"}}, []string{"region-2", "region-1"}},
		{schema.Placement{Policy: "cdn", In: []string{"region-2"}, Allowed: []string{"region-2"}}, []string{"region-2"}},
	} {
		got := r.Place(tc.p)
		require.Equal(t, tc.want, got.In, "%+v", tc.p)
		require.Empty(t, got.Concerns)
	}
}
