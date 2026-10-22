package schema_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mr-tron/base58"
	"github.com/spf13/afero"
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

// relocates is a build that places everything in DefaultRegion and says, without
// refusing, where a placement named elsewhere — the shape RolePlaced exists for.
type relocates struct{}

func (relocates) Serves() []string                { return []string{schema.DefaultRegion} }
func (relocates) Allow([]string) []schema.Concern { return nil }
func (relocates) Place(p schema.Placement) schema.Answer {
	a := schema.Answer{In: []string{schema.DefaultRegion}}
	if len(p.Named()) > 0 {
		a.Concerns = []schema.Concern{{Severity: schema.Check, Title: "relocated"}}
	}
	return a
}

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
	views, findings, err := viewsOf(t, several{"region-1", "region-2", "region-3"}, map[string]string{
		"/project.yaml":          regionsProject,
		"/function/api.yaml":     viewFunction("api", "regions:\n    pinned: [region-1]\n"),
		"/cache/ha.yaml":         viewCache("ha", "regions:\n    failover:\n        main: region-1\n        standby: region-2\n"),
		"/cache/edge.yaml":       viewCache("edge", "regions:\n    cdn: [region-2]\n"),
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
	}, rolesByName(t, views["region-1"]))
	require.Equal(t, map[string]schema.Role{
		"cache/ha":         schema.RoleStandby,
		"cache/edge":       schema.RoleSeed,
		"cache/free":       schema.RoleAnywhere,
		"zone/example_com": schema.RoleFollows,
	}, rolesByName(t, views["region-2"]))
	require.Equal(t, map[string]schema.Role{
		"cache/edge":       schema.RoleDemand,
		"cache/free":       schema.RoleAnywhere,
		"zone/example_com": schema.RoleFollows,
	}, rolesByName(t, views["region-3"]))

	// THE INDEX OF A VIEW NAMES ONLY WHAT IT HOLDS.
	idx := views["region-2"].Object["indexes"].(map[string]any)
	for key := range idx {
		require.NotContains(t, key, "/function/api/")
	}
}

// A CDN NOTHING SEEDS HERE IS SEEDED BY THE PLATFORM, wherever it runs.
func TestACdnWithNoServedSeedIsSeededEverywhere(t *testing.T) {
	views, _, err := viewsOf(t, several{"region-1", "region-2"}, map[string]string{
		"/project.yaml":    regionsProject,
		"/cache/edge.yaml": viewCache("edge", "regions:\n    cdn: []\n"),
	})
	require.NoError(t, err)
	require.Equal(t, map[string]schema.Role{"cache/edge": schema.RoleSeed}, rolesByName(t, views["region-1"]))
	require.Equal(t, map[string]schema.Role{"cache/edge": schema.RoleSeed}, rolesByName(t, views["region-2"]))
}

// A BUILD SERVING FEWER REGIONS THAN THE PROJECT NAMES HOLDS IT WHERE IT PUT IT, and the
// role says that is not where the project asked.
func TestARelocatedResourceIsPlaced(t *testing.T) {
	views, findings, err := viewsOf(t, relocates{}, map[string]string{
		"/project.yaml":    regionsProject,
		"/cache/ha.yaml":   viewCache("ha", "regions:\n    failover:\n        main: region-1\n        standby: region-2\n"),
		"/cache/pin.yaml":  viewCache("pin", "regions:\n    pinned: [region-1]\n"),
		"/cache/edge.yaml": viewCache("edge", "regions:\n    cdn: [region-1]\n"),
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
	views, findings, err := viewsOf(t, several{"region-1", "region-2"}, map[string]string{
		"/project.yaml":        regionsProject,
		"/function/api.yaml":   viewFunction("api", "regions:\n    pinned: [region-2]\ncache:\n    sessions:\n        ops: [get]\n"),
		"/cache/sessions.yaml": viewCache("sessions", "regions:\n    pinned: [region-1]\n"),
	})
	require.ErrorIs(t, err, schema.ErrUnviewable)
	require.Nil(t, views)
	require.Equal(t, []string{`function/api: reaches cache/sessions, which is not placed in "region-2" where this is`},
		findingsTitled(findings, schema.Broken))

	// THE SAME EDGE INSIDE ONE REGION IS FINE, and so is one to what has no footprint.
	_, findings, err = viewsOf(t, several{"region-1", "region-2"}, map[string]string{
		"/project.yaml":        regionsProject,
		"/function/api.yaml":   viewFunction("api", "regions:\n    pinned: [region-1]\ncache:\n    sessions:\n        ops: [get]\n"),
		"/cache/sessions.yaml": viewCache("sessions", "regions:\n    pinned: [region-1, region-2]\n"),
	})
	require.NoError(t, err)
	require.Empty(t, findings)
}

// WHAT HAS NO FOOTPRINT AND REACHES ACROSS REGIONS IS HELD NOWHERE, which is refused.
func TestAnUnplacedResourceReachingTwoRegionsIsRefused(t *testing.T) {
	_, findings, err := viewsOf(t, several{"region-1", "region-2"}, map[string]string{
		"/project.yaml":          regionsProject,
		"/function/api.yaml":     viewFunction("api", "regions:\n    pinned: [region-1]\n"),
		"/function/auth.yaml":    viewFunction("auth", "regions:\n    pinned: [region-2]\n"),
		"/gateway/api.yaml":      viewGateway("api", "authorizer:\n    name: auth\n"),
		"/zone/example_com.yaml": viewZone,
	})
	require.ErrorIs(t, err, schema.ErrUnviewable)
	require.Equal(t, []string{"gateway/api: reaches resources placed in no one region together"},
		findingsTitled(findings, schema.Broken))
}

// AN EMPTY VIEW IS A REGION THAT HOLDS NOTHING; AN UNKNOWN ONE IS NOT A REGION.
func TestAnEmptyViewIsNotAnUnknownOne(t *testing.T) {
	views, _, err := viewsOf(t, several{"region-1", "region-2"}, map[string]string{
		"/project.yaml":   regionsProject,
		"/cache/pin.yaml": viewCache("pin", "regions:\n    pinned: [region-1]\n"),
	})
	require.NoError(t, err)
	r2, err := schema.ViewIn(views, "region-2")
	require.NoError(t, err)
	require.True(t, r2.Empty())
	require.Equal(t, "region-2", r2.Region)
	require.Equal(t, "p", r2.Object["object"].(map[string]any)["name"], "an empty view is still the project")

	_, err = schema.ViewIn(views, "mars")
	require.ErrorIs(t, err, schema.ErrUnknownDestination)
}

// A PLACEMENT FINDING THAT IS BROKEN STOPS THE VIEWS. A region the Regions answers but
// does not render is placed there all the same, and is simply not a view here.
func TestBrokenPlacementStopsTheViews(t *testing.T) {
	_, findings, err := viewsOf(t, several{"region-1"}, map[string]string{
		"/project.yaml":   regionsProject,
		"/cache/pin.yaml": viewCache("pin", "regions:\n    pinned: [region-3]\n"),
	})
	require.ErrorIs(t, err, schema.ErrUnviewable)
	require.Contains(t, findingsTitled(findings, schema.Broken), "cache/pin: unknown region-3")

	plan := schema.PlanViews(compileDocs(t, "", map[string]string{
		"/project.yaml":   regionsProject,
		"/cache/pin.yaml": viewCache("pin", "regions:\n    pinned: [region-1]\n"),
	}), schema.WithRegions(liar{}))
	require.Equal(t, []string{"region-2"}, plan.Destinations)
	require.Empty(t, findingsTitled(plan.Findings, schema.Broken))
	require.Empty(t, plan.Roles["region-2"])
}

type liar struct{}

func (liar) Serves() []string                       { return []string{"region-2", "region-2"} }
func (liar) Allow([]string) []schema.Concern        { return nil }
func (liar) Place(p schema.Placement) schema.Answer { return schema.Answer{In: p.Named()} }

// A SCOPE IS KEPT FOR WHAT IS IN IT, and a resource that is a scope says so.
func TestAScopeIsKeptForWhatIsInIt(t *testing.T) {
	views, _, err := viewsOf(t, several{"region-1", "region-2"}, map[string]string{
		"/project.yaml":                regionsProject,
		"/namespace/shop/meta.yaml":    regionsShop,
		"/namespace/shop/cache/a.yaml": viewCache("a", "regions:\n    pinned: [region-1]\n"),
		"/namespace/shop/cache/b.yaml": viewCache("b", "regions:\n    pinned: [region-2]\n"),
	})
	require.NoError(t, err)
	for region, want := range map[string]string{"region-1": "a", "region-2": "b"} {
		var names []string
		for _, res := range schema.Resources(views[region].Object) {
			names = append(names, res.Scope+"/"+res.Name)
		}
		require.Equal(t, []string{"shop/" + want}, names)
		require.Len(t, schema.Scopes(views[region].Object), 1)
	}
	require.False(t, errors.Is(err, schema.ErrUnviewable))
}

// THE WHOLE CORPUS FIXTURE, AS ITS region-1 PREVIEW SEES IT: every kind, lowerings included,
// and the one view is the object. A build with no regions refuses it, since it pins region-1.
func TestTheBasicFixtureIsOneViewInRegion1(t *testing.T) {
	flat := basicFixture(t)

	_, _, err := schema.Views(flat)
	require.ErrorIs(t, err, schema.ErrUnviewable)

	views, _, err := schema.Views(flat, schema.WithRegions(schema.Preview("region-1")))
	require.NoError(t, err)
	require.Equal(t, flat, views["region-1"].Object)
	require.Len(t, views["region-1"].Roles, len(schema.Resources(flat)))
}

// AS ONE REGION OF THE CLOUD IT IS WRITTEN FOR, each build renders its own view.
func TestTheBasicFixtureRendersPerRegion(t *testing.T) {
	flat := basicFixture(t)

	r1, findings, err := schema.Views(flat, schema.WithRegions(schema.WithRegion("region-1")))
	require.NoError(t, err, "%v", findings)
	require.Equal(t, []string{"region-1"}, keysOf(r1))
	require.Equal(t, flat, r1["region-1"].Object)

	r2, findings, err := schema.Views(flat, schema.WithRegions(schema.WithRegion("region-2")))
	require.NoError(t, err, "%v", findings)
	require.Equal(t, []string{"region-2"}, keysOf(r2))
	roles := r2["region-2"].Roles
	require.Equal(t, schema.RoleStandby, roles[idOfName(t, flat, "application", "api")])
	for _, shell := range []string{"workflow/checkout", "agent/support"} {
		kind, name, _ := strings.Cut(shell, "/")
		id := idOfName(t, flat, kind, name)
		produced, _ := schema.Became(flat, id)
		require.NotEmpty(t, produced, shell)
		for _, held := range append(produced, id) {
			role, ok := roles[held]
			require.False(t, ok, "%s is %s in region-2", held, role)
		}
	}
}

func basicFixture(t *testing.T) map[string]any {
	t.Helper()
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	require.NoError(t, err)
	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err)
	return obj.Flat()
}

func idOfName(t *testing.T, flat map[string]any, kind, name string) string {
	t.Helper()
	for _, res := range schema.Resources(flat) {
		if res.Kind == kind && res.Name == name {
			return res.ID
		}
	}
	t.Fatalf("no %s/%s", kind, name)
	return ""
}

func keysOf(views map[string]schema.View) []string {
	out := make([]string, 0, len(views))
	for k := range views {
		out = append(out, k)
	}
	return out
}

// ENVIRONMENTS FIRST, THEN PLACEMENT: a view is (environment, region), and the
// environment's own regions narrow where what says nothing goes.
func TestAnEnvironmentNarrowsItsViews(t *testing.T) {
	docs := map[string]string{
		"/project.yaml": regionsProject +
			"environments:\n    production:\n        branches: [main]\n        regions: [region-2]\n" +
			"    preview:\n        branches: [\"*\"]\n",
		"/cache/c.yaml": regionsCache(""),
	}
	for env, want := range map[string]map[string]int{"production": {"region-1": 0, "region-2": 1}, "preview": {"region-1": 1, "region-2": 1}} {
		views, _, err := schema.Views(compileDocs(t, env, docs), schema.WithRegions(several{"region-1", "region-2"}), schema.AuditIn(env))
		require.NoError(t, err)
		for region, n := range want {
			require.Len(t, views[region].Roles, n, "%s in %s", env, region)
		}
	}
}

// THROUGH THE SESSION, which is how a CLI, a console and the explorer ask: the same
// views, the canonical object beside them, and the environment bound the way a deploy
// binds it.
func TestTheSessionAsksForTheSameViews(t *testing.T) {
	fs := afero.NewMemMapFs()
	for path, body := range map[string]string{
		"/project.yaml": regionsProject +
			"environments:\n    production:\n        branches: [main]\n        regions: [region-2]\n" +
			"    preview:\n        branches: [\"*\"]\n",
		"/cache/c.yaml": regionsCache("size_preview: 2MB\n"),
	} {
		require.NoError(t, afero.WriteFile(fs, path, []byte(body), 0o644))
	}

	s, err := schema.NewSession(fs, "/")
	require.NoError(t, err)
	_, err = s.Views(context.Background(), schema.CompileOptions{Branch: "main"})
	require.ErrorIs(t, err, schema.ErrUnviewable, "the default build has no regions, and production names one")

	s, err = schema.NewSessionIn(fs, "/", schema.WithRegion("region-2"))
	require.NoError(t, err)
	got, err := s.Views(context.Background(), schema.CompileOptions{Branch: "main"})
	require.NoError(t, err)
	require.Len(t, got.Views, 1)
	require.Equal(t, "region-2", got.Views[0].Destination)
	require.Equal(t, got.Canonical.Flat(), got.Views[0].Object)

	s, err = schema.NewSessionIn(fs, "/", several{"region-1", "region-2"})
	require.NoError(t, err)
	got, err = s.Views(context.Background(), schema.CompileOptions{Branch: "main", Environment: "production"})
	require.NoError(t, err)
	require.Equal(t, []string{"region-1", "region-2"}, []string{got.Views[0].Destination, got.Views[1].Destination})
	require.True(t, got.Views[0].Empty())
	require.Equal(t, map[string]string{cidOfCache: "anywhere"}, got.Views[1].Roles)

	got, err = s.Views(context.Background(), schema.CompileOptions{Branch: "main", Environment: "preview"})
	require.NoError(t, err)
	size := got.Views[0].Object["object"].(map[string]any)["cache"].(map[string]any)[cidOfCache].(map[string]any)["size"]
	require.EqualValues(t, 2000000, size, "compiled for preview")

	require.NoError(t, afero.WriteFile(fs, "/cache/c.yaml", []byte(regionsCache("regions:\n    pinned: [region-3]\n")), 0o644))
	s, err = schema.NewSessionIn(fs, "/", several{"region-1"})
	require.NoError(t, err)
	got, err = s.Views(context.Background(), schema.CompileOptions{Branch: "main"})
	require.ErrorIs(t, err, schema.ErrUnviewable)
	require.NotEmpty(t, got.Notes)
	require.Equal(t, "broken", got.Notes[0].Severity)
}

const cidOfCache = "QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB"

// A PREVIEW SHOWS THE REGIONS IT IS GIVEN AND REFUSES NOTHING: what a tool offers
// before any cloud is chosen, from the regions the project itself names.
func TestAPreviewOfTheRegionsTheProjectNames(t *testing.T) {
	flat := compileDocs(t, "", map[string]string{
		"/project.yaml":    regionsProject + "regions: [region-1, region-2, region-3]\n",
		"/cache/pin.yaml":  viewCache("pin", "regions:\n    pinned: [region-1]\n"),
		"/cache/ha.yaml":   viewCache("ha", "regions:\n    failover:\n        main: region-2\n        standby: mars\n"),
		"/cache/edge.yaml": viewCache("edge", "regions:\n    cdn: [region-3]\n"),
	})
	named := schema.RegionsNamed(flat)
	require.Equal(t, []string{"mars", "region-1", "region-2", "region-3"}, named)

	views, findings, err := schema.Views(flat, schema.WithRegions(schema.Preview("region-1", "region-2", "region-3", "region-1", "")))
	require.NoError(t, err)
	require.Equal(t, []string{`cache/ha: is placed in region "mars", which this preview does not show`}, findingsTitled(findings, schema.Check))
	require.Equal(t, schema.RoleSeed, views["region-3"].Roles[cidFor("cache/edge")])
	require.Equal(t, schema.RoleDemand, views["region-1"].Roles[cidFor("cache/edge")])
	require.Equal(t, schema.RoleMain, views["region-2"].Roles[cidFor("cache/ha")])
	require.Len(t, views, 3)

	require.Equal(t, []schema.Concern{{schema.Check, `allows region "mars", which this preview does not show`, "This preview shows region-1."}},
		schema.Preview("region-1").Allow([]string{"region-1", "mars"}))
	require.Nil(t, schema.Preview("region-1").Allow([]string{"region-1"}))
	require.Equal(t, "This preview shows no regions.", schema.Preview().Allow([]string{"x"})[0].Detail)
	require.Equal(t, []string{"region-2"}, schema.Preview("region-1", "region-2").Place(schema.Placement{Allowed: []string{"region-2"}}).In)
}

// WHAT A PINNED SHELL LOWERS TO IS PINNED THE SAME, in the compiled object itself:
// workflow/checkout and agent/support are pinned to region-1, and so is every function,
// sync and database they became.
func TestALoweredResourceIsPlacedWithItsShell(t *testing.T) {
	flat := basicFixture(t)
	byID := map[string]schema.Instance{}
	for _, res := range schema.Resources(flat) {
		byID[res.ID] = res
	}
	seen := map[string]bool{}
	for _, shell := range []string{"workflow/checkout", "agent/support"} {
		kind, name, _ := strings.Cut(shell, "/")
		produced, _ := schema.Became(flat, idOfName(t, flat, kind, name))
		require.NotEmpty(t, produced, shell)
		for _, id := range produced {
			res := byID[id]
			seen[res.Kind+"/"+res.Name] = true
			regions, _ := res.Fields["regions"].(map[string]any)
			require.Equal(t, "pinned", regions["policy"], "%s/%s", res.Kind, res.Name)
			require.ElementsMatch(t, []any{"region-1"}, regions["in"], "%s/%s", res.Kind, res.Name)
		}
	}
	for _, name := range []string{"function/checkout_charge", "function/checkout_reserve", "function/checkout_settle",
		"sync/checkout_reserve", "database/support_memory", "function/support_infer"} {
		require.True(t, seen[name], name)
	}
}
