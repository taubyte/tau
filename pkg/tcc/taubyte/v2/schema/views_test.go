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
// multi-region build composes: a named region it lacks is Broken, and nothing said
// is everywhere.
type several []string

// relocates is a build that places everything in DefaultRegion and says, without
// refusing, where a namespace named elsewhere — the shape RolePlaced exists for.
type relocates struct{}

func (relocates) Serves() []string                { return []string{schema.DefaultRegion} }
func (relocates) Allow([]string) []schema.Concern { return nil }
func (relocates) Place(p schema.Placement) schema.Answer {
	a := schema.Answer{In: []string{schema.DefaultRegion}}
	if len(p.In) > 0 {
		a.Concerns = []schema.Concern{{Severity: schema.Check, Title: "relocated"}}
	}
	return a
}

func (s several) Serves() []string                { return s }
func (s several) Allow([]string) []schema.Concern { return nil }
func (s several) Place(p schema.Placement) schema.Answer {
	var a schema.Answer
	for _, r := range p.In {
		if !slices.Contains(s, r) {
			a.Concerns = append(a.Concerns, schema.Concern{Severity: schema.Broken, Title: "unknown " + r})
		}
	}
	from := []string(s)
	if p.In != nil {
		from = p.In
	}
	for _, r := range from {
		if slices.Contains(s, r) && !slices.Contains(a.In, r) {
			a.In = append(a.In, r)
		}
	}
	return a
}

// viewNamespace is a namespace's meta document, at its path.
func viewNamespace(docs map[string]string, path, topology, in string) {
	name := path[strings.LastIndex(path, "/")+1:]
	body := "id: " + cidFor(name) + "\n"
	if topology != "" || in != "" {
		body += regionsBlock(topology, in)
	}
	docs["/namespace/"+strings.ReplaceAll(path, "/", "/namespace/")+"/meta.yaml"] = body
}

func viewFunction(name, block string) string {
	return "id: " + cidFor("function/"+name) + "\npackage:\n    name: " + name + "_code\n" +
		"execution:\n    call: handle\n    timeout: 15s\n    memory: 32MiB\n" + block
}

// viewPackage is what a viewFunction runs. withPackages adds one beside every function,
// so a test lists the workloads it is about.
func viewPackage(name string) string {
	return "id: " + cidFor("package/"+name+"_code") + "\ntype: wasm\nsource: services/" + name + "\noutput: x.wasm\n" +
		"build:\n    steps:\n        - name: build\n          image: rust:1.83\n          commands: [cargo build]\n"
}

func withPackages(docs map[string]string) map[string]string {
	out := map[string]string{}
	for path, body := range docs {
		out[path] = body
		if _, name, ok := strings.Cut(path, "/function/"); ok && !strings.Contains(name, "/") {
			name = strings.TrimSuffix(name, ".yaml")
			out["/package/"+name+"_code.yaml"] = viewPackage(name)
		}
	}
	return out
}

func viewCache(name, block string) string {
	return "id: " + cidFor("cache/"+name) + "\nsize: 1MB\nconsensus: crdt\n" + block
}

func viewGateway(handler, block string) string {
	return viewGatewayIn(handler, "", block)
}

// viewGatewayIn routes to a handler in the named namespace, the project root when "".
func viewGatewayIn(handler, namespace, block string) string {
	ns := ""
	if namespace != "" {
		ns = "            namespace: " + namespace + "\n"
	}
	return "id: " + cidFor("gateway/"+handler) + "\nhost:\n    zone:\n        in:\n            name: example_com\n        name: " + handler + "\n" +
		"route:\n    path: /\n    match: prefix\n    methods: [GET]\n    handler:\n        function:\n" + ns + "            name: " + handler + "\n" + block
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

func TestNoRegionsIsOneViewOfEverything(t *testing.T) {
	docs := map[string]string{
		"/project.yaml":          regionsProject + everyBranch,
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
		"gateway/api":      schema.RoleAnywhere,
		"zone/example_com": schema.RoleNeeded,
		"package/api_code": schema.RoleNeeded,
	}, rolesByName(t, v))
}

func TestEachTopologyIsHeldInItsRole(t *testing.T) {
	docs := map[string]string{
		"/project.yaml":                   regionsProject + everyBranch,
		"/namespace/eu/function/api.yaml": viewFunction("api", ""),
		"/namespace/ha/cache/ha.yaml":     viewCache("ha", ""),
		"/namespace/edge/cache/edge.yaml": viewCache("edge", ""),
		"/cache/free.yaml":                viewCache("free", ""),
		"/zone/example_com.yaml":          viewZone,
	}
	viewNamespace(docs, "eu", "", "[region-1]")
	viewNamespace(docs, "ha", "failover", "[region-1, region-2]")
	viewNamespace(docs, "edge", "active", "[region-2, region-3]")
	docs["/package/api_code.yaml"] = viewPackage("api")
	views, findings, err := viewsOf(t, several{"region-1", "region-2", "region-3"}, docs)
	require.NoError(t, err)
	require.Empty(t, findings)
	require.Equal(t, map[string]schema.Role{
		"function/api":     schema.RoleSingle,
		"cache/ha":         schema.RolePrimary,
		"cache/free":       schema.RoleAnywhere,
		"zone/example_com": schema.RoleFollows,
		"package/api_code": schema.RoleNeeded,
	}, rolesByName(t, views["region-1"]))
	require.Equal(t, map[string]schema.Role{
		"cache/ha":         schema.RoleStandby,
		"cache/edge":       schema.RoleActive,
		"cache/free":       schema.RoleAnywhere,
		"zone/example_com": schema.RoleFollows,
	}, rolesByName(t, views["region-2"]), "the package goes with the function that runs it, and nowhere else")
	require.Equal(t, map[string]schema.Role{
		"cache/edge":       schema.RoleActive,
		"cache/free":       schema.RoleAnywhere,
		"zone/example_com": schema.RoleFollows,
	}, rolesByName(t, views["region-3"]))
	require.False(t, schema.RoleStandby.Serves())
	require.True(t, schema.RolePrimary.Serves())

	// THE INDEX OF A VIEW NAMES ONLY WHAT IT HOLDS.
	idx := views["region-2"].Object["indexes"].(map[string]any)
	for key := range idx {
		require.NotContains(t, key, "/function/api/")
	}
}

func TestWhatASingleRegionWorkloadReadsStaysInIt(t *testing.T) {
	docs := map[string]string{
		"/project.yaml":                   regionsProject + everyBranch,
		"/namespace/eu/function/api.yaml": viewFunction("api", "secret:\n    stripe:\n        namespace: /\n        ops: [get]\n"),
		"/package/api_code.yaml":          viewPackage("api"),
		"/secret/stripe.yaml":             "id: " + cidFor("secret/stripe") + "\nrotation: 720h\n",
		"/secret/spare.yaml":              "id: " + cidFor("secret/spare") + "\nrotation: 720h\n",
	}
	viewNamespace(docs, "eu", "single", "[region-1]")
	views, findings, err := viewsOf(t, several{"region-1", "region-2"}, docs)
	require.NoError(t, err)
	require.Empty(t, findings)
	require.Equal(t, map[string]schema.Role{
		"function/api":     schema.RoleSingle,
		"package/api_code": schema.RoleNeeded,
		"secret/stripe":    schema.RoleNeeded,
		"secret/spare":     schema.RoleFollows,
	}, rolesByName(t, views["region-1"]))
	require.Equal(t, map[string]schema.Role{
		"secret/spare": schema.RoleFollows,
	}, rolesByName(t, views["region-2"]), "what nothing reads still follows everywhere; what the eu function reads does not")
}

func TestARelocatedResourceIsPlaced(t *testing.T) {
	docs := map[string]string{
		"/project.yaml":               regionsProject + everyBranch,
		"/namespace/ha/cache/ha.yaml": viewCache("ha", ""),
		"/cache/free.yaml":            viewCache("free", ""),
	}
	viewNamespace(docs, "ha", "failover", "[region-1, region-2]")
	views, findings, err := viewsOf(t, relocates{}, docs)
	require.NoError(t, err)
	require.Equal(t, []string{"cache/ha: relocated"}, findingsTitled(findings, schema.Check))
	require.Equal(t, map[string]schema.Role{
		"cache/ha":   schema.RolePlaced,
		"cache/free": schema.RoleAnywhere,
	}, rolesByName(t, views[schema.DefaultRegion]))
}

func TestAnEdgeAcrossRegionsIsRefused(t *testing.T) {
	docs := map[string]string{
		"/project.yaml":                     regionsProject + everyBranch,
		"/namespace/us/function/api.yaml":   viewFunction("api", "cache:\n    sessions:\n        namespace: /eu\n        ops: [get]\n"),
		"/package/api_code.yaml":            viewPackage("api"),
		"/namespace/eu/cache/sessions.yaml": viewCache("sessions", ""),
	}
	viewNamespace(docs, "us", "", "[region-2]")
	viewNamespace(docs, "eu", "", "[region-1]")
	views, findings, err := viewsOf(t, several{"region-1", "region-2"}, docs)
	require.ErrorIs(t, err, schema.ErrUnviewable)
	require.Nil(t, views)
	require.Equal(t, []string{`function/api: reaches cache/sessions, which does not run in "region-2" where this does`},
		findingsTitled(findings, schema.Broken))

	// THE SAME EDGE INSIDE ONE REGION IS FINE: a cache active where the function is.
	viewNamespace(docs, "eu", "active", "[region-1, region-2]")
	_, findings, err = viewsOf(t, several{"region-1", "region-2"}, docs)
	require.NoError(t, err)
	require.Empty(t, findings)
}

// Where something serves, what it reaches serves too: a namespace that fails over
// answers from its first region alone.
func TestWhatServesReachesWhatServes(t *testing.T) {
	type testCase struct {
		name           string
		caller, target [2]string
		broken         []string
	}
	for _, tc := range []testCase{
		{name: "active reaching failover",
			caller: [2]string{"active", "[region-1, region-2]"}, target: [2]string{"failover", "[region-1, region-2]"},
			broken: []string{`function/api: reaches cache/sessions, which only stands by in "region-2" where this serves`}},
		{name: "failover reaching failover in another order",
			caller: [2]string{"failover", "[region-1, region-2]"}, target: [2]string{"failover", "[region-2, region-1]"},
			broken: []string{`function/api: reaches cache/sessions, which only stands by in "region-1" where this serves`}},
		{name: "failover reaching failover in the same order",
			caller: [2]string{"failover", "[region-1, region-2]"}, target: [2]string{"failover", "[region-1, region-2]"}},
		{name: "failover reaching active",
			caller: [2]string{"failover", "[region-1, region-2]"}, target: [2]string{"active", "[region-1, region-2]"}},
		{name: "single reaching active",
			caller: [2]string{"single", "[region-2]"}, target: [2]string{"active", "[region-1, region-2]"}},
		{name: "single reaching single elsewhere",
			caller: [2]string{"single", "[region-2]"}, target: [2]string{"single", "[region-1]"},
			broken: []string{`function/api: reaches cache/sessions, which does not run in "region-2" where this does`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs := map[string]string{
				"/project.yaml":                       regionsProject + everyBranch,
				"/namespace/app/function/api.yaml":    viewFunction("api", "cache:\n    sessions:\n        namespace: /data\n        ops: [get]\n"),
				"/package/api_code.yaml":              viewPackage("api"),
				"/namespace/data/cache/sessions.yaml": viewCache("sessions", ""),
			}
			viewNamespace(docs, "app", tc.caller[0], tc.caller[1])
			viewNamespace(docs, "data", tc.target[0], tc.target[1])
			_, findings, err := viewsOf(t, several{"region-1", "region-2"}, docs)
			require.Equal(t, tc.broken, findingsTitled(findings, schema.Broken))
			if tc.broken == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, schema.ErrUnviewable)
			}
		})
	}
}

// NOTHING REACHES INTO ANOTHER NAMESPACE'S REGIONS: a name answered everywhere, in
// front of a handler in one region, is refused where the handler does not run.
func TestAGatewayRouteDoesNotReachIntoAnotherNamespacesRegions(t *testing.T) {
	docs := map[string]string{
		"/project.yaml":                   regionsProject + regionsBlock("active", "[region-1, region-2, region-3]") + everyBranch,
		"/namespace/eu/function/api.yaml": viewFunction("api", ""),
		"/package/api_code.yaml":          viewPackage("api"),
		"/gateway/api.yaml":               viewGatewayIn("api", "/eu", ""),
		"/zone/example_com.yaml":          viewZone,
	}
	viewNamespace(docs, "eu", "", "[region-1]")
	_, findings, err := viewsOf(t, several{"region-1", "region-2", "region-3"}, docs)
	require.ErrorIs(t, err, schema.ErrUnviewable)
	require.ElementsMatch(t, []string{
		`gateway/api: reaches function/api, which does not run in "region-2" where this does`,
		`gateway/api: reaches function/api, which does not run in "region-3" where this does`,
	}, findingsTitled(findings, schema.Broken))

	// A HANDLER THAT FAILS OVER stands by in its other region, and answers nothing there.
	viewNamespace(docs, "eu", "failover", "[region-1, region-2]")
	docs["/project.yaml"] = regionsProject + regionsBlock("active", "[region-1, region-2]") + everyBranch
	_, findings, err = viewsOf(t, several{"region-1", "region-2"}, docs)
	require.ErrorIs(t, err, schema.ErrUnviewable)
	require.Equal(t, []string{`gateway/api: reaches function/api, which only stands by in "region-2" where this serves`},
		findingsTitled(findings, schema.Broken))
}

// A REPLICATION'S COPIES TRAVEL THROUGH ITS NAMESPACE'S TUNNEL, between regions that
// namespace runs in, and a tunnel meshes two regions or more.
func TestAReplicationTravelsThroughItsNamespacesTunnel(t *testing.T) {
	replication := func(of, between string) string {
		return "id: " + cidFor("replication/copies") + "\nof:\n    database:\n        name: " + of + "\nbetween: " + between + "\n"
	}
	for _, tc := range []struct {
		name, billing, replication string
		broken                     []string
	}{
		{name: "meshed", billing: "tunnel: true\n", replication: replication("ledger", "[region-1, region-2]")},
		{name: "one region", billing: "tunnel: true\n    in: [region-1]\n", replication: replication("ledger", "[region-1, region-2]"),
			broken: []string{
				"namespace/billing: has a tunnel and runs in fewer than two regions, so there is nothing to mesh",
				"replication/copies: replicates database/ledger, which runs in fewer than two regions",
			}},
		{name: "no tunnel", replication: replication("ledger", "[region-1, region-2]"),
			broken: []string{"replication/copies: replicates database/ledger, whose namespace has no tunnel"}},
		{name: "no such database", billing: "tunnel: true\n", replication: replication("nowhere", "[region-1, region-2]"),
			broken: []string{"replication/copies: replicates database/nowhere, which does not exist"}},
		{name: "a region it does not run in", billing: "tunnel: true\n", replication: replication("ledger", "[region-1, region-3]"),
			broken: []string{`replication/copies: replicates in "region-3", where database/ledger does not run`}},
		{name: "one region named", billing: "tunnel: true\n", replication: replication("ledger", "[region-1]"),
			broken: []string{"replication/copies: replicates between fewer than two regions"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs := map[string]string{
				"/project.yaml":                              regionsProject + regionsBlock("active", "[region-1, region-2]") + everyBranch,
				"/namespace/billing/meta.yaml":               "id: " + cidFor("billing") + "\n",
				"/namespace/billing/database/ledger.yaml":    "id: " + cidFor("database/ledger") + "\nkv:\n    distributed:\n        size: 64MB\n        consensus: crdt\n",
				"/namespace/billing/replication/copies.yaml": tc.replication,
			}
			if tc.billing != "" {
				docs["/namespace/billing/meta.yaml"] += "regions:\n    " + tc.billing
			}
			_, findings, err := viewsOf(t, several{"region-1", "region-2"}, docs)
			require.ElementsMatch(t, tc.broken, findingsTitled(findings, schema.Broken))
			if tc.broken == nil {
				require.NoError(t, err)
			}
		})
	}
}

func TestAnUnplacedResourceReachingTwoRegionsIsRefused(t *testing.T) {
	docs := map[string]string{
		"/project.yaml":                     regionsProject + everyBranch,
		"/namespace/eu/application/db.yaml": "id: " + cidFor("application/db") + "\npackage:\n    name: db_code\n    namespace: /\nports:\n    pg:\n        number: 5432\nexecution:\n    memory: 64MB\n",
		"/package/db_code.yaml":             "id: " + cidFor("package/db_code") + "\ntype: oci\nsource: services/db\nbuild:\n    dockerfile:\n        target: runtime\n",
		"/namespace/us/function/audit.yaml": viewFunction("audit", "implements: proxy-transformer\n"),
		"/package/audit_code.yaml":          viewPackage("audit"),
		"/proxy/db.yaml": "id: " + cidFor("proxy/db") + "\nhost:\n    generated:\n        name: db\nservice: postgres\n" +
			"transformers:\n    - name: audit\n      namespace: /us\nforward:\n    application:\n        namespace: /eu\n        name: db\n        port: pg\n",
	}
	viewNamespace(docs, "eu", "", "[region-1]")
	viewNamespace(docs, "us", "", "[region-2]")
	_, findings, err := viewsOf(t, several{"region-1", "region-2"}, docs)
	require.ErrorIs(t, err, schema.ErrUnviewable)
	require.Equal(t, []string{"proxy/db: reaches resources placed in no one region together"},
		findingsTitled(findings, schema.Broken))
}

// A REGION THAT IS ONE OF MANY holds nothing that only follows what runs elsewhere:
// a proxy in front of an application in region-1 is not region-2's, and is not
// broken for it.
func TestWhatFollowsSomethingElsewhereIsNotHeldHere(t *testing.T) {
	docs := map[string]string{
		"/project.yaml":                     regionsProject + everyBranch,
		"/namespace/eu/application/db.yaml": "id: " + cidFor("application/db") + "\npackage:\n    name: db_code\n    namespace: /\nports:\n    pg:\n        number: 5432\nexecution:\n    memory: 64MB\n",
		"/package/db_code.yaml":             "id: " + cidFor("package/db_code") + "\ntype: oci\nsource: services/db\nbuild:\n    dockerfile:\n        target: runtime\n",
		"/proxy/db.yaml": "id: " + cidFor("proxy/db") + "\nhost:\n    generated:\n        name: db\nservice: postgres\n" +
			"forward:\n    application:\n        namespace: /eu\n        name: db\n        port: pg\n",
	}
	viewNamespace(docs, "eu", "", "[region-1]")
	views, findings, err := viewsOf(t, schema.WithRegion("region-2"), docs)
	require.NoError(t, err, "%v", findings)
	require.Empty(t, findingsTitled(findings, schema.Broken))
	require.True(t, views["region-2"].Empty())

	views, _, err = viewsOf(t, schema.WithRegion("region-1"), docs)
	require.NoError(t, err)
	require.Equal(t, schema.RoleFollows, rolesByName(t, views["region-1"])["proxy/db"])
}

func TestAnEmptyViewIsNotAnUnknownOne(t *testing.T) {
	docs := map[string]string{
		"/project.yaml":                regionsProject + everyBranch,
		"/namespace/eu/cache/pin.yaml": viewCache("pin", ""),
	}
	viewNamespace(docs, "eu", "", "[region-1]")
	views, _, err := viewsOf(t, several{"region-1", "region-2"}, docs)
	require.NoError(t, err)
	r2, err := schema.ViewIn(views, "region-2")
	require.NoError(t, err)
	require.True(t, r2.Empty())
	require.Equal(t, "region-2", r2.Region)
	require.Equal(t, "p", r2.Object["object"].(map[string]any)["name"], "an empty view is still the project")

	_, err = schema.ViewIn(views, "mars")
	require.ErrorIs(t, err, schema.ErrUnknownDestination)
}

func TestBrokenPlacementStopsTheViews(t *testing.T) {
	_, findings, err := viewsOf(t, several{"region-1"}, map[string]string{
		"/project.yaml":   regionsProject + regionsBlock("", "[region-3]") + everyBranch,
		"/cache/pin.yaml": viewCache("pin", ""),
	})
	require.ErrorIs(t, err, schema.ErrUnviewable)
	require.Contains(t, findingsTitled(findings, schema.Broken), "cache/pin: unknown region-3")

	plan := schema.PlanViews(compileDocs(t, "", map[string]string{
		"/project.yaml":   regionsProject + regionsBlock("", "[region-1]") + everyBranch,
		"/cache/pin.yaml": viewCache("pin", ""),
	}), schema.WithRegions(liar{}))
	require.Equal(t, []string{"region-2"}, plan.Destinations)
	require.Empty(t, findingsTitled(plan.Findings, schema.Broken))
	require.Empty(t, plan.Roles["region-2"])
}

type liar struct{}

func (liar) Serves() []string                       { return []string{"region-2", "region-2"} }
func (liar) Allow([]string) []schema.Concern        { return nil }
func (liar) Place(p schema.Placement) schema.Answer { return schema.Answer{In: p.In} }

func TestAScopeIsKeptForWhatIsInIt(t *testing.T) {
	docs := map[string]string{
		"/project.yaml": regionsProject + everyBranch,
		"/namespace/shop/namespace/a/cache/a.yaml": viewCache("a", ""),
		"/namespace/shop/namespace/b/cache/b.yaml": viewCache("b", ""),
	}
	viewNamespace(docs, "shop", "active", "[region-1, region-2]")
	viewNamespace(docs, "shop/a", "", "[region-1]")
	viewNamespace(docs, "shop/b", "", "[region-2]")
	views, _, err := viewsOf(t, several{"region-1", "region-2"}, docs)
	require.NoError(t, err)
	for region, want := range map[string]string{"region-1": "a", "region-2": "b"} {
		var names []string
		for _, res := range schema.Resources(views[region].Object) {
			names = append(names, res.Scope+"/"+res.Name)
		}
		require.Equal(t, []string{"shop/" + want + "/" + want}, names)
		require.Len(t, schema.Scopes(views[region].Object), 2)
	}
	require.False(t, errors.Is(err, schema.ErrUnviewable))
}

func TestTheBasicFixtureIsOneViewInRegion1(t *testing.T) {
	flat := basicFixture(t)

	_, _, err := schema.Views(flat)
	require.ErrorIs(t, err, schema.ErrUnviewable)

	views, _, err := schema.Views(flat, schema.WithRegions(schema.Preview("region-1")))
	require.NoError(t, err)
	require.Equal(t, flat, views["region-1"].Object)
	require.Len(t, views["region-1"].Roles, len(schema.Resources(flat)))
}

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
	// THE PROJECT FAILS OVER: region-2 holds what it needs to take over, and nothing
	// in it answers yet.
	for id, role := range roles {
		require.Contains(t, []schema.Role{schema.RoleStandby, schema.RoleNeeded, schema.RoleFollows, schema.RoleScope}, role, id)
	}
	for _, shell := range []string{"workflow/checkout", "agent/support"} {
		kind, name, _ := strings.Cut(shell, "/")
		id := idOfName(t, flat, kind, name)
		produced, _ := schema.Became(flat, id)
		require.NotEmpty(t, produced, shell)
		require.Equal(t, schema.RoleStandby, roles[id], shell)
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

	require.NoError(t, afero.WriteFile(fs, "/project.yaml", []byte(regionsProject+regionsBlock("", "[region-3]")+
		"environments:\n    production:\n        branches: [main]\n    preview:\n        branches: [\"*\"]\n"), 0o644))
	s, err = schema.NewSessionIn(fs, "/", several{"region-1"})
	require.NoError(t, err)
	got, err = s.Views(context.Background(), schema.CompileOptions{Branch: "main"})
	require.ErrorIs(t, err, schema.ErrUnviewable)
	require.NotEmpty(t, got.Notes)
	require.Equal(t, "broken", got.Notes[0].Severity)
}

const cidOfCache = "QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioEB"

func TestAPreviewOfTheRegionsTheProjectNames(t *testing.T) {
	docs := map[string]string{
		"/project.yaml":               regionsProject + regionsBlock("active", "[region-1, region-2, region-3, mars]") + everyBranch,
		"/cache/edge.yaml":            viewCache("edge", ""),
		"/namespace/ha/cache/ha.yaml": viewCache("ha", ""),
	}
	viewNamespace(docs, "ha", "failover", "[region-2, mars]")
	flat := compileDocs(t, "", docs)
	named := schema.RegionsNamed(flat)
	require.Equal(t, []string{"mars", "region-1", "region-2", "region-3"}, named)

	views, findings, err := schema.Views(flat, schema.WithRegions(schema.Preview("region-1", "region-2", "region-3", "region-1", "")))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{`project/p: allows region "mars", which this preview does not show`, `namespace/ha: allows region "mars", which this preview does not show`},
		findingsTitled(findings, schema.Check))
	for _, region := range []string{"region-1", "region-2", "region-3"} {
		require.Equal(t, schema.RoleActive, views[region].Roles[cidFor("cache/edge")], region)
	}
	require.Equal(t, schema.RolePrimary, views["region-2"].Roles[cidFor("cache/ha")])
	_, held := views["region-1"].Roles[cidFor("cache/ha")]
	require.False(t, held)
	require.Len(t, views, 3)

	require.Equal(t, []schema.Concern{{schema.Check, `allows region "mars", which this preview does not show`, "This preview shows region-1."}},
		schema.Preview("region-1").Allow([]string{"region-1", "mars"}))
	require.Nil(t, schema.Preview("region-1").Allow([]string{"region-1"}))
	require.Equal(t, "This preview shows no regions.", schema.Preview().Allow([]string{"x"})[0].Detail)
	require.Equal(t, []string{"region-2"}, schema.Preview("region-1", "region-2").Place(schema.Placement{In: []string{"region-2", "mars"}}).In)
	require.Equal(t, []string{"region-1", "region-2"}, schema.Preview("region-1", "region-2").Place(schema.Placement{}).In)
}

// A lowered resource is produced in its shell's scope, so it runs where the shell's
// namespace does.
func TestALoweredResourceIsPlacedWithItsShell(t *testing.T) {
	flat := basicFixture(t)
	byID := map[string]schema.Instance{}
	for _, res := range schema.Resources(flat) {
		byID[res.ID] = res
	}
	seen := map[string]bool{}
	for _, shell := range []string{"workflow/checkout", "agent/support"} {
		kind, name, _ := strings.Cut(shell, "/")
		id := idOfName(t, flat, kind, name)
		produced, _ := schema.Became(flat, id)
		require.NotEmpty(t, produced, shell)
		// Inside the shell's own scope, or beside it: either way under the namespace
		// that holds the shell.
		own := strings.TrimPrefix(byID[id].Scope+"/"+name, "/")
		for _, p := range produced {
			res := byID[p]
			seen[res.Kind+"/"+res.Name] = true
			require.Contains(t, []string{byID[id].Scope, own}, res.Scope, "%s/%s", res.Kind, res.Name)
		}
	}
	for _, name := range []string{"function/checkout_charge", "function/checkout_reserve", "function/checkout_settle",
		"sync/checkout_reserve", "database/support_memory", "function/support_infer"} {
		require.True(t, seen[name], name)
	}
}
