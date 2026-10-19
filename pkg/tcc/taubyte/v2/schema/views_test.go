package schema_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"testing"

	"github.com/mr-tron/base58"
	"github.com/stretchr/testify/require"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// cidFor is a real CIDv0 for a bare name, the way the corpus mints them.
func cidFor(name string) string {
	sum := sha256.Sum256([]byte(name))
	return base58.Encode(append([]byte{0x12, 0x20}, sum[:]...))
}

// several is a Regions of a cloud serving several regions, shaped like the one a
// multi-region build composes: a named region it lacks is Broken, nothing said is
// anywhere allowed, and a cdn runs where it is seeded and wherever else it may go.
type several []string

func (s several) Serves() []string                { return s }
func (s several) Allow([]string) []schema.Concern { return nil }
func (s several) Place(p schema.Placement) schema.Answer {
	var a schema.Answer
	for _, r := range p.Named() {
		if !slices.Contains(s, r) {
			a.Concerns = append(a.Concerns, schema.Concern{Severity: schema.Broken, Title: "unknown " + r})
		}
	}
	from := []string(s)
	if p.Allowed != nil {
		from = p.Allowed
	}
	switch {
	case p.Policy == "cdn":
		from = append(slices.Clone(p.In), from...)
	case len(p.Named()) > 0:
		from = p.Named()
	}
	for _, r := range from {
		if slices.Contains(s, r) && !slices.Contains(a.In, r) {
			a.In = append(a.In, r)
		}
	}
	return a
}

func viewFunction(name, block string) string {
	return "id: " + cidFor("function/"+name) + "\nsource: services/" + name + "\n" +
		"build:\n    commands:\n        image: rust:1.83\n        run: cargo build\n        output: x.wasm\n" +
		"execution:\n    call: handle\n    timeout: 15s\n    memory: 32MiB\n" + block
}

func viewCache(name, block string) string {
	return "id: " + cidFor("cache/"+name) + "\nsize: 1MB\nconsensus: crdt\n" + block
}

func viewGateway(handler, block string) string {
	return "id: " + cidFor("gateway/"+handler) + "\nhost:\n    zone:\n        in:\n            name: example_com\n        name: " + handler + "\n" +
		"route:\n    path: /\n    match: prefix\n    methods: [GET]\n    handler:\n        function:\n            name: " + handler + "\n" + block
}

var viewZone = "id: " + cidFor("zone/example_com") + "\nfqdn: example.com\n"

func viewsOf(t *testing.T, r schema.Regions, docs map[string]string) (map[string]schema.View, []schema.Finding, error) {
	t.Helper()
	return schema.Views(compileDocs(t, "", docs), schema.WithRegions(r))
}

func rolesByName(t *testing.T, v schema.View) map[string]schema.Role {
	t.Helper()
	out := map[string]schema.Role{}
	for _, res := range schema.Resources(v.Object) {
		role, ok := v.Roles[res.ID]
		require.True(t, ok, "%s/%s is in the view with no role", res.Kind, res.Name)
		out[res.Kind+"/"+res.Name] = role
	}
	require.Len(t, v.Roles, len(out), "a role for something the view does not hold")
	return out
}

// THE OSS BUILD HAS ONE VIEW, AND IT IS THE PROJECT. Nothing is cut away from a build
// that has no regions: the one view is the whole compiled object, ids and index intact.
func TestNoRegionsIsOneViewOfEverything(t *testing.T) {
	docs := map[string]string{
		"/project.yaml":          regionsProject,
		"/function/api.yaml":     viewFunction("api", "cache:\n    sessions:\n        ops: [get]\n"),
		"/cache/sessions.yaml":   viewCache("sessions", ""),
		"/gateway/api.yaml":      viewGateway("api", ""),
		"/zone/example_com.yaml": viewZone,
	}
	flat := compileDocs(t, "", docs)
	views, findings, err := schema.Views(flat)
	require.NoError(t, err)
	require.Empty(t, findings)
	require.Len(t, views, 1)
	v, err := schema.ViewIn(views, schema.DefaultRegion)
	require.NoError(t, err)
	require.Equal(t, flat, v.Object)
	require.False(t, v.Empty())
	require.Equal(t, map[string]schema.Role{
		"function/api":     schema.RoleAnywhere,
		"cache/sessions":   schema.RoleAnywhere,
		"gateway/api":      schema.RoleFollows,
		"zone/example_com": schema.RoleNeeded,
	}, rolesByName(t, v))
}

// EVERY POLICY HAS ITS ROLE, in every region it is held in.
func TestEachPolicyIsHeldInItsRole(t *testing.T) {
	views, findings, err := viewsOf(t, several{"eu", "us", "ap"}, map[string]string{
		"/project.yaml":          regionsProject,
		"/function/api.yaml":     viewFunction("api", "regions:\n    pinned: [eu]\n"),
		"/cache/ha.yaml":         viewCache("ha", "regions:\n    failover:\n        main: eu\n        standby: us\n"),
		"/cache/edge.yaml":       viewCache("edge", "regions:\n    cdn: [us]\n"),
		"/cache/free.yaml":       viewCache("free", ""),
		"/gateway/api.yaml":      viewGateway("api", ""),
		"/zone/example_com.yaml": viewZone,
	})
	require.NoError(t, err)
	require.Empty(t, findings)
	require.Equal(t, map[string]schema.Role{
		"function/api":     schema.RolePinned,
		"cache/ha":         schema.RoleMain,
		"cache/edge":       schema.RoleDemand,
		"cache/free":       schema.RoleAnywhere,
		"gateway/api":      schema.RoleFollows,
		"zone/example_com": schema.RoleNeeded,
	}, rolesByName(t, views["eu"]))
	require.Equal(t, map[string]schema.Role{
		"cache/ha":         schema.RoleStandby,
		"cache/edge":       schema.RoleSeed,
		"cache/free":       schema.RoleAnywhere,
		"zone/example_com": schema.RoleFollows,
	}, rolesByName(t, views["us"]))
	require.Equal(t, map[string]schema.Role{
		"cache/edge":       schema.RoleDemand,
		"cache/free":       schema.RoleAnywhere,
		"zone/example_com": schema.RoleFollows,
	}, rolesByName(t, views["ap"]))

	// THE INDEX OF A VIEW NAMES ONLY WHAT IT HOLDS.
	idx := views["us"].Object["indexes"].(map[string]any)
	for key := range idx {
		require.NotContains(t, key, "/function/api/")
	}
}

// A CDN NOTHING SEEDS HERE IS SEEDED BY THE PLATFORM, wherever it runs.
func TestACdnWithNoServedSeedIsSeededEverywhere(t *testing.T) {
	views, _, err := viewsOf(t, several{"eu", "us"}, map[string]string{
		"/project.yaml":    regionsProject,
		"/cache/edge.yaml": viewCache("edge", "regions:\n    cdn: []\n"),
	})
	require.NoError(t, err)
	require.Equal(t, map[string]schema.Role{"cache/edge": schema.RoleSeed}, rolesByName(t, views["eu"]))
	require.Equal(t, map[string]schema.Role{"cache/edge": schema.RoleSeed}, rolesByName(t, views["us"]))
}

// A BUILD SERVING FEWER REGIONS THAN THE PROJECT NAMES HOLDS IT WHERE IT PUT IT, and the
// role says that is not where the project asked.
func TestARelocatedResourceIsPlaced(t *testing.T) {
	views, findings, err := viewsOf(t, schema.TryOneRegion(), map[string]string{
		"/project.yaml":    regionsProject,
		"/cache/ha.yaml":   viewCache("ha", "regions:\n    failover:\n        main: eu\n        standby: us\n"),
		"/cache/pin.yaml":  viewCache("pin", "regions:\n    pinned: [eu]\n"),
		"/cache/edge.yaml": viewCache("edge", "regions:\n    cdn: [eu]\n"),
	})
	require.NoError(t, err)
	require.NotEmpty(t, findingsTitled(findings, schema.Check))
	require.Equal(t, map[string]schema.Role{
		"cache/ha":   schema.RolePlaced,
		"cache/pin":  schema.RolePlaced,
		"cache/edge": schema.RoleSeed,
	}, rolesByName(t, views[schema.DefaultRegion]))
}

// AN EDGE ACROSS REGIONS IS REFUSED, by name, and nothing is projected.
func TestAnEdgeAcrossRegionsIsRefused(t *testing.T) {
	views, findings, err := viewsOf(t, several{"eu", "us"}, map[string]string{
		"/project.yaml":        regionsProject,
		"/function/api.yaml":   viewFunction("api", "regions:\n    pinned: [us]\ncache:\n    sessions:\n        ops: [get]\n"),
		"/cache/sessions.yaml": viewCache("sessions", "regions:\n    pinned: [eu]\n"),
	})
	require.ErrorIs(t, err, schema.ErrUnviewable)
	require.Nil(t, views)
	require.Equal(t, []string{`function/api: reaches cache/sessions, which is not placed in "us" where this is`},
		findingsTitled(findings, schema.Broken))

	// THE SAME EDGE INSIDE ONE REGION IS FINE, and so is one to what has no footprint.
	_, findings, err = viewsOf(t, several{"eu", "us"}, map[string]string{
		"/project.yaml":        regionsProject,
		"/function/api.yaml":   viewFunction("api", "regions:\n    pinned: [eu]\ncache:\n    sessions:\n        ops: [get]\n"),
		"/cache/sessions.yaml": viewCache("sessions", "regions:\n    pinned: [eu, us]\n"),
	})
	require.NoError(t, err)
	require.Empty(t, findings)
}

// WHAT HAS NO FOOTPRINT AND REACHES ACROSS REGIONS IS HELD NOWHERE, which is refused.
func TestAnUnplacedResourceReachingTwoRegionsIsRefused(t *testing.T) {
	_, findings, err := viewsOf(t, several{"eu", "us"}, map[string]string{
		"/project.yaml":          regionsProject,
		"/function/api.yaml":     viewFunction("api", "regions:\n    pinned: [eu]\n"),
		"/function/auth.yaml":    viewFunction("auth", "regions:\n    pinned: [us]\n"),
		"/gateway/api.yaml":      viewGateway("api", "authorizer:\n    name: auth\n"),
		"/zone/example_com.yaml": viewZone,
	})
	require.ErrorIs(t, err, schema.ErrUnviewable)
	require.Equal(t, []string{"gateway/api: reaches resources placed in no one region together"},
		findingsTitled(findings, schema.Broken))
}

// AN EMPTY VIEW IS A REGION THAT HOLDS NOTHING; AN UNKNOWN ONE IS NOT A REGION.
func TestAnEmptyViewIsNotAnUnknownOne(t *testing.T) {
	views, _, err := viewsOf(t, several{"eu", "us"}, map[string]string{
		"/project.yaml":   regionsProject,
		"/cache/pin.yaml": viewCache("pin", "regions:\n    pinned: [eu]\n"),
	})
	require.NoError(t, err)
	us, err := schema.ViewIn(views, "us")
	require.NoError(t, err)
	require.True(t, us.Empty())
	require.Equal(t, "us", us.Region)
	require.Equal(t, "p", us.Object["object"].(map[string]any)["name"], "an empty view is still the project")

	_, err = schema.ViewIn(views, "mars")
	require.ErrorIs(t, err, schema.ErrUnknownDestination)
}

// A PLACEMENT FINDING THAT IS BROKEN STOPS THE VIEWS, and a Regions answering a region
// it does not serve is the build's fault, said as such.
func TestBrokenPlacementStopsTheViews(t *testing.T) {
	_, findings, err := viewsOf(t, several{"eu"}, map[string]string{
		"/project.yaml":   regionsProject,
		"/cache/pin.yaml": viewCache("pin", "regions:\n    pinned: [ap]\n"),
	})
	require.ErrorIs(t, err, schema.ErrUnviewable)
	require.Contains(t, findingsTitled(findings, schema.Broken), "cache/pin: unknown ap")

	plan := schema.PlanViews(compileDocs(t, "", map[string]string{
		"/project.yaml":   regionsProject,
		"/cache/pin.yaml": viewCache("pin", "regions:\n    pinned: [eu]\n"),
	}), schema.WithRegions(liar{}))
	require.Equal(t, []string{"us"}, plan.Destinations)
	require.Contains(t, findingsTitled(plan.Findings, schema.Broken), `cache/pin: is placed in "eu", which this build does not serve`)
}

type liar struct{}

func (liar) Serves() []string                       { return []string{"us", "us"} }
func (liar) Allow([]string) []schema.Concern        { return nil }
func (liar) Place(p schema.Placement) schema.Answer { return schema.Answer{In: p.Named()} }

// A SCOPE IS KEPT FOR WHAT IS IN IT, and a resource that is a scope says so.
func TestAScopeIsKeptForWhatIsInIt(t *testing.T) {
	views, _, err := viewsOf(t, several{"eu", "us"}, map[string]string{
		"/project.yaml":                regionsProject,
		"/namespace/shop/meta.yaml":    regionsShop,
		"/namespace/shop/cache/a.yaml": viewCache("a", "regions:\n    pinned: [eu]\n"),
		"/namespace/shop/cache/b.yaml": viewCache("b", "regions:\n    pinned: [us]\n"),
	})
	require.NoError(t, err)
	for region, want := range map[string]string{"eu": "a", "us": "b"} {
		var names []string
		for _, res := range schema.Resources(views[region].Object) {
			names = append(names, res.Scope+"/"+res.Name)
		}
		require.Equal(t, []string{"shop/" + want}, names)
		require.Len(t, schema.Scopes(views[region].Object), 1)
	}
	require.False(t, errors.Is(err, schema.ErrUnviewable))
}

// THE WHOLE CORPUS FIXTURE, AS ONE BUILD SEES IT: every kind, lowerings included, and the
// one view is the object.
func TestTheBasicFixtureIsOneViewUnderOneRegion(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	require.NoError(t, err)
	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err)
	flat := obj.Flat()

	views, _, err := schema.Views(flat, schema.WithRegions(schema.TryOneRegion()))
	require.NoError(t, err)
	require.Equal(t, flat, views[schema.DefaultRegion].Object)
	require.Len(t, views[schema.DefaultRegion].Roles, len(schema.Resources(flat)))
}

// ENVIRONMENTS FIRST, THEN PLACEMENT: a view is (environment, region), and the
// environment's own regions narrow where what says nothing goes.
func TestAnEnvironmentNarrowsItsViews(t *testing.T) {
	docs := map[string]string{
		"/project.yaml": regionsProject +
			"environments:\n    production:\n        branches: [main]\n        regions: [us]\n" +
			"    preview:\n        branches: [\"*\"]\n",
		"/cache/c.yaml": regionsCache(""),
	}
	for env, want := range map[string]map[string]int{"production": {"eu": 0, "us": 1}, "preview": {"eu": 1, "us": 1}} {
		views, _, err := schema.Views(compileDocs(t, env, docs), schema.WithRegions(several{"eu", "us"}), schema.AuditIn(env))
		require.NoError(t, err)
		for region, n := range want {
			require.Len(t, views[region].Roles, n, "%s in %s", env, region)
		}
	}
}
