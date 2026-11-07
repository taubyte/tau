//go:build dreaming

package dream_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	nodeDream "github.com/taubyte/tau/instance/dream"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	buildDeploy "github.com/taubyte/tau/services/plm/deploy"
	"github.com/taubyte/tau/services/plm/run"
)

// projects/with/regions and projects/refused/regions: a namespace says where
// everything in it runs and how — single, failover or active — and a resource says
// nothing. A cloud of three regions is what the corpus is cut for.
var threeRegions = schema.Preview("region-1", "region-2", "region-3")

func compiledCorpus(t *testing.T, name string) map[string]any {
	t.Helper()
	c, err := schema.New(schema.WithLocal(projectDir(name)), schema.WithBranch("main"))
	require.NoError(t, err)
	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err, "%s compiles; any refusal is the cut's", name)
	return obj.Flat()
}

// rolesIn is every resource a view holds and how, by kind/name.
func rolesIn(v schema.View) map[string]schema.Role {
	out := map[string]schema.Role{}
	for _, res := range schema.Resources(v.Object) {
		out[res.Kind+"/"+res.Name] = v.Roles[res.ID]
	}
	return out
}

func titled(findings []schema.Finding, severity schema.Severity) []string {
	var out []string
	for _, f := range findings {
		if f.Severity == severity {
			out = append(out, f.Kind+"/"+f.Name+": "+f.Title)
		}
	}
	return out
}

func TestNamespacesNestTopologies(t *testing.T) {
	flat := compiledCorpus(t, "with/regions/topologies")
	views, findings, err := schema.Views(flat, schema.WithRegions(threeRegions))
	require.NoError(t, err, "%v", findings)
	require.Equal(t, []string{`namespace/reports: is failover above and runs in "region-2" alone`}, titled(findings, schema.Note))

	require.Equal(t, map[string]schema.Role{
		"function/storefront": schema.RoleActive,
		"package/hello_code":  schema.RoleNeeded,
		"function/charge":     schema.RolePrimary,
		"database/ledger":     schema.RolePrimary,
	}, rolesIn(views["region-1"]))
	require.Equal(t, map[string]schema.Role{
		"function/storefront": schema.RoleActive,
		"package/hello_code":  schema.RoleNeeded,
		"function/charge":     schema.RoleStandby,
		"database/ledger":     schema.RoleStandby,
		"cache/monthly":       schema.RoleSingle,
	}, rolesIn(views["region-2"]))
	require.Equal(t, map[string]schema.Role{
		"function/storefront": schema.RoleActive,
		"package/hello_code":  schema.RoleNeeded,
		"cache/events":        schema.RoleSingle,
	}, rolesIn(views["region-3"]))

	// A BUILD THAT IS ONE REGION says its standby serves: nothing moves a failover yet.
	_, findings, err = schema.Views(flat, schema.WithRegions(schema.WithRegion("region-2")))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{
		`function/charge: stands by in "region-2", and this build answers from a standby as from a primary`,
		`database/ledger: stands by in "region-2", and this build answers from a standby as from a primary`,
	}, titled(findings, schema.Check))
}

func TestARegionRunsWhatItsNamespacesPlaceThere(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	r, _ := inRegionAt(t, "region-3", "with/regions/topologies", "topologies.example.com")
	held, _ := viewHeld(t, r, "region-3")
	require.Equal(t, map[string]bool{"function/storefront": true, "package/hello_code": true, "cache/events": true}, held)

	// What an active namespace runs answers here, reached in its own scope.
	storefront := specs.Address{Project: r.project, Environment: "prod", Scope: []string{"web"}, Kind: "function", Name: "storefront"}
	require.Eventually(t, func() bool {
		res, err := r.on(t.Context(), storefront, http.MethodGet, "/")
		return err == nil && res.Status == http.StatusOK
	}, 30*time.Second, 200*time.Millisecond)

	// Billing does not run in region-3 at all.
	_, err := r.on(t.Context(), specs.Address{Project: r.project, Environment: "prod", Scope: []string{"billing"}, Kind: "function", Name: "charge"}, http.MethodGet, "/")
	require.Error(t, err)
}

// askHeaders is ask with request headers, as name, value pairs.
func askHeaders(t *testing.T, r *running, path string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://"+r.host+path, nil)
	req.Host = r.host
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	r.node.Handler().ServeHTTP(w, req)
	return w
}

func TestAnEdgeKeepsWhatItsOriginAnswered(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	flat := compiledCorpus(t, "with/regions/edge")
	_, findings, err := schema.Views(flat, schema.WithRegions(schema.Preview("region-1", "region-2")))
	require.NoError(t, err, "%v", findings)

	// THE EDGE KEEPS WHAT ITS ORIGIN ANSWERED: the handler echoes a probe header, so a
	// kept answer still shows the one that filled it.
	r1, _ := inRegionAt(t, "region-1", "with/regions/edge", "www.example.com")
	var first *httptest.ResponseRecorder
	require.Eventually(t, func() bool {
		first = askHeaders(t, r1, "/", "X-Probe", "one", "Accept-Language", "fr")
		return first.Code == http.StatusOK
	}, 30*time.Second, 200*time.Millisecond)
	require.Equal(t, "miss", first.Header().Get("X-Taubyte-Cache"))
	require.Equal(t, "one", first.Header().Get("x-echo-x-probe"))

	kept := askHeaders(t, r1, "/", "X-Probe", "two", "Accept-Language", "fr")
	require.Equal(t, http.StatusOK, kept.Code)
	require.Equal(t, "hit", kept.Header().Get("X-Taubyte-Cache"))
	require.Equal(t, "one", kept.Header().Get("x-echo-x-probe"), "answered from what was kept, not by the origin")

	varied := askHeaders(t, r1, "/", "X-Probe", "two", "Accept-Language", "en")
	require.Equal(t, "miss", varied.Header().Get("X-Taubyte-Cache"), "the route varies on the language")
	require.Equal(t, "two", varied.Header().Get("x-echo-x-probe"))

	// REGION-2 ANSWERS THE NAME FROM ITS OWN ORIGIN, and keeps its own answers: nothing
	// it holds came from region-1.
	r2, _ := inRegionAt(t, "region-2", "with/regions/edge", "www.example.com")
	var own *httptest.ResponseRecorder
	require.Eventually(t, func() bool {
		own = askHeaders(t, r2, "/", "X-Probe", "three", "Accept-Language", "fr")
		return own.Code == http.StatusOK
	}, 30*time.Second, 200*time.Millisecond)
	require.Equal(t, "miss", own.Header().Get("X-Taubyte-Cache"), "region-2 keeps its own answers")
	require.Equal(t, "three", own.Header().Get("x-echo-x-probe"))
}

// A REPLICATION SAYS WHERE COPIES ARE KEPT, and they travel through its namespace's
// tunnel: the ledger runs in both of billing's regions, and both hold the replication.
func TestAReplicationKeepsCopiesBetweenTheRegionsItNames(t *testing.T) {
	flat := compiledCorpus(t, "with/regions/replication")
	views, findings, err := schema.Views(flat, schema.WithRegions(threeRegions))
	require.NoError(t, err, "%v", findings)

	copies := idOf(t, flat, "replication", "ledger_copies")
	for _, region := range []string{"region-1", "region-2"} {
		require.Contains(t, views[region].Roles, copies, region)
	}
	require.True(t, views["region-3"].Empty(), "billing does not run in region-3")
}

// idOf is the compiled id of the one resource of kind called name.
func idOf(t *testing.T, flat map[string]any, kind, name string) string {
	t.Helper()
	for _, res := range schema.Resources(flat) {
		if res.Kind == kind && res.Name == name {
			return res.ID
		}
	}
	t.Fatalf("no %s/%s", kind, name)
	return ""
}

func TestARegionConstraintIsRefused(t *testing.T) {
	for name, want := range map[string][]string{
		"refused/regions/widen":       {`namespace/eu: runs in region "region-3", which the scope above it does not`},
		"refused/regions/unsaid":      {"project/unsaid: runs in 2 regions and does not say how"},
		"refused/regions/single":      {"project/single: is single and runs in 2 regions"},
		"refused/regions/failoverone": {"project/failoverone: fails over with one region"},
		"refused/regions/standby":     {`function/api: reaches cache/sessions, which only stands by in "region-2" where this serves`},
		"refused/regions/order":       {`function/api: reaches cache/sessions, which only stands by in "region-1" where this serves`},
		"refused/regions/guard":       {`gateway/www: reaches function/doorman, which does not run in "region-2" where this does`},
		"refused/regions/elsewhere":   {`gateway/www: reaches function/api, which does not run in "region-2" where this does`},
		"refused/regions/replicateone": {
			"namespace/billing: has a tunnel and runs in fewer than two regions, so there is nothing to mesh",
			"replication/ledger_copies: replicates database/ledger, which runs in fewer than two regions",
		},
		"refused/regions/replicatenotunnel": {"replication/ledger_copies: replicates database/ledger, whose namespace has no tunnel"},
	} {
		t.Run(name, func(t *testing.T) {
			_, findings, err := schema.Views(compiledCorpus(t, name), schema.WithRegions(threeRegions))
			require.ErrorIs(t, err, schema.ErrUnviewable)
			require.ElementsMatch(t, want, titled(findings, schema.Broken))
		})
	}
}

func TestADeployReachingAStandbyPublishesNothing(t *testing.T) {
	const id = "Qmf6Xf1BXXxm3chNWtkJvXWETQPfXKrsjSbQwvQ2kXQKHs"
	r, err := pkgregister.New(pkgregister.WithStore(pkgregister.NewMemStore()))
	require.NoError(t, err)
	d, err := buildDeploy.New(
		buildDeploy.WithProjects(configures{}),
		buildDeploy.WithCheckout(corpus{filepath.Join("projects", "refused/regions/standby")}),
		buildDeploy.WithLocalRegister(r),
		buildDeploy.WithCloudFqdn("tau.dream.test"),
		buildDeploy.WithRegions(schema.WithRegion("region-2")),
	)
	require.NoError(t, err)
	err = planThenRun(t.Context(), d, run.Work{JobID: "j", RepositoryKey: "github.com/acme/" + id,
		Ref: "refs/heads/main", CommitSHA: "c", ProjectID: id}, &buildLog{})
	require.ErrorIs(t, err, buildDeploy.ErrBroken)
	require.ErrorContains(t, err, `reaches cache/sessions, which only stands by in "region-2" where this serves`)

	envs, err := r.Environments(t.Context(), id)
	require.NoError(t, err)
	require.Empty(t, envs, "nothing is published for a refused project")
}
