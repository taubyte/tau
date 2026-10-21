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
func (onlyNamed) Serves() []string                { return []string{"eu", "us"} }

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

// A BUILD THAT SERVES ONE REGION PLACES EVERYTHING THERE, and refuses whenever the
// config named another — once per resource, naming it.
func TestNoRegionsRefusesAndPlacesInDefault(t *testing.T) {
	r, severity := schema.WithNoRegions(), schema.Broken
	for _, tc := range []struct {
		name  string
		p     schema.Placement
		title string
	}{
		{"pinned", schema.Placement{Policy: "pinned", In: []string{"eu", "us"}},
			`is placed in regions "eu", "us", which this build does not serve; it runs in "default"`},
		{"cdn", schema.Placement{Policy: "cdn", In: []string{"eu"}},
			`is placed in region "eu", which this build does not serve; it runs in "default"`},
		{"failover", schema.Placement{Policy: "failover", Main: "eu", Standby: "us"},
			`is placed in regions "eu", "us", which this build does not serve; it runs in "default"`},
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
	for _, p := range []schema.Placement{{}, {Allowed: []string{"eu"}}, {Policy: "pinned", In: []string{"default"}}} {
		got := r.Place(p)
		require.Equal(t, []string{schema.DefaultRegion}, got.In)
		require.Empty(t, got.Concerns)
	}

	require.Empty(t, r.Allow(nil))
	require.Empty(t, r.Allow([]string{"default"}))
	allow := r.Allow([]string{"eu", "eu", "default"})
	require.Len(t, allow, 1)
	require.Equal(t, severity, allow[0].Severity)
	require.Equal(t, `allows region "eu", which this build does not serve; everything under it runs in "default"`, allow[0].Title)
}

// THE AUDIT FILES WHAT ONEREGION SAYS against what said it: the resource, the
// namespace, the project and the environment, each by name. It is the default, and
// passing a nil Regions is the default too.
func TestTheAuditHearsNoRegionsByDefault(t *testing.T) {
	docs := map[string]string{
		"/project.yaml": regionsProject + "regions: [eu]\n" +
			"environments:\n    production:\n        branches: [main]\n        regions: [eu]\n",
		"/namespace/shop/meta.yaml":           regionsShop + "regions: [eu]\n",
		"/namespace/shop/cache/sessions.yaml": regionsCache("regions:\n    pinned: [eu]\n"),
	}
	obj := compileDocs(t, "", docs)

	want := []string{
		`project/p: allows region "eu", which this build does not serve; everything under it runs in "default"`,
		`namespace/shop: allows region "eu", which this build does not serve; everything under it runs in "default"`,
		`environments/production: allows region "eu", which this build does not serve; everything under it runs in "default"`,
		`cache/sessions: is placed in region "eu", which this build does not serve; it runs in "default"`,
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
		"/project.yaml": regionsProject,
		"/cache/c.yaml": regionsCache(""),
	})
	require.Empty(t, schema.Place(clean))
}

// ONE EFFECTIVE PLACEMENT: what the resource states, its scope chain's ceiling, and
// the environment the audit reads narrowing it.
func TestThePlacementTheRegionsIsAsked(t *testing.T) {
	docs := map[string]string{
		"/project.yaml": regionsProject + "regions: [eu, us]\n" +
			"environments:\n    production:\n        branches: [main]\n        regions: [us]\n" +
			"    preview:\n        branches: [\"*\"]\n",
		"/namespace/shop/meta.yaml":       regionsShop + "regions: [eu, us]\n",
		"/namespace/shop/cache/c.yaml":    regionsCache("regions:\n    failover:\n        main: eu\n        standby: us\n"),
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
	require.Equal(t, schema.Placement{Policy: "failover", Main: "eu", Standby: "us", Allowed: []string{"us"}},
		byPolicy["failover"], "shop's eu+us, narrowed by production's us")
	require.Equal(t, []string{"us"}, byPolicy[""].Allowed)

	// Told no environment, the ceiling is the chain alone; an environment with no
	// regions of its own narrows nothing.
	for _, env := range []string{"", "preview"} {
		asked = nil
		schema.Audit(compileDocs(t, env, docs), nil, schema.AuditIn(env), schema.WithRegions(onlyNamed{&asked}))
		for _, p := range asked {
			if p.Policy == "failover" {
				require.Equal(t, []string{"eu", "us"}, p.Allowed, env)
			}
		}
	}

	// A chain and an environment that share nothing allow nothing — an empty
	// ceiling, not a missing one.
	asked = nil
	schema.Audit(compileDocs(t, "production", map[string]string{
		"/project.yaml": regionsProject + "regions: [eu]\n" +
			"environments:\n    production:\n        branches: [main]\n        regions: [us]\n" +
			"    preview:\n        branches: [\"*\"]\n",
		"/cache/c.yaml": regionsCache(""),
	}), nil, schema.AuditIn("production"), schema.WithRegions(onlyNamed{&asked}))
	require.Equal(t, []schema.Placement{{Allowed: []string{}}}, asked)

	// A project whose only ceiling is its environment's is bounded by that.
	asked = nil
	schema.Audit(compileDocs(t, "production", map[string]string{
		"/project.yaml": regionsProject +
			"environments:\n    production:\n        branches: [main]\n        regions: [us]\n" +
			"    preview:\n        branches: [\"*\"]\n",
		"/cache/c.yaml": regionsCache(""),
	}), nil, schema.AuditIn("production"), schema.WithRegions(onlyNamed{&asked}))
	require.Equal(t, []schema.Placement{{Allowed: []string{"us"}}}, asked)
}

// A REGIONS THAT REFUSES IS HEARD AS IT SPOKE: its severity, against the resource.
type refuses struct{}

func (refuses) Place(p schema.Placement) schema.Answer {
	return schema.Answer{In: p.Named(), Concerns: []schema.Concern{{schema.Broken, "is refused", "because"}}}
}
func (refuses) Serves() []string { return []string{"eu"} }
func (refuses) Allow([]string) []schema.Concern {
	return []schema.Concern{{schema.Broken, "allowance refused", "because"}}
}

func TestTheAuditFilesWhatTheRegionsSays(t *testing.T) {
	found := auditWith(t, refuses{}, map[string]string{
		"/project.yaml": regionsProject + "regions: [eu]\n",
		"/cache/c.yaml": regionsCache("regions:\n    pinned: [eu]\n"),
	})
	require.Contains(t, findingsTitled(found, schema.Broken), "cache/c: is refused")
	require.Contains(t, findingsTitled(found, schema.Broken), "project/p: allowance refused")
}

// AS A REGION, a build is one region of a cloud that has more: naming the others is
// not an error, and it answers where things run as the cloud would.
func TestWithRegionIsOneRegionOfMany(t *testing.T) {
	r := schema.WithRegion("eu")
	require.Equal(t, []string{"eu"}, r.Serves())
	require.Empty(t, r.Allow([]string{"eu", "us"}))

	for _, tc := range []struct {
		p    schema.Placement
		want []string
	}{
		{schema.Placement{}, []string{"eu"}},
		{schema.Placement{Allowed: []string{"us"}}, []string{"us"}},
		{schema.Placement{Policy: "pinned", In: []string{"us"}}, []string{"us"}},
		{schema.Placement{Policy: "failover", Main: "eu", Standby: "us"}, []string{"eu", "us"}},
		{schema.Placement{Policy: "cdn", In: []string{"us"}}, []string{"us", "eu"}},
		{schema.Placement{Policy: "cdn", In: []string{"us"}, Allowed: []string{"us"}}, []string{"us"}},
	} {
		got := r.Place(tc.p)
		require.Equal(t, tc.want, got.In, "%+v", tc.p)
		require.Empty(t, got.Concerns)
	}
}
