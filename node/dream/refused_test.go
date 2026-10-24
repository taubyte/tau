//go:build dreaming

package dream_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	pkgregister "github.com/taubyte/tau/pkg/register"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/services/build/backend"
	buildDeploy "github.com/taubyte/tau/services/build/deploy"
)

// refused_test holds the projects under projects/refused: each is something an
// author can write, and the vocabulary claims to refuse. A claim nobody asks the
// vocabulary to keep is one nothing notices it stopped keeping.
//
// THE REFUSAL IS THE AUDIT'S: these compile. A container with no image and no
// build is a statement the config itself can answer, so it is checkBuild's
// (pkg/tcc/taubyte/v2/schema/checks.go) to call broken, and the deploy cuts views
// through that same audit, so nothing is published.
//
// IT IS PAIRED WITH THE PROJECT THAT WORKS. The refused one is also unrouted,
// which the audit says too; that is not what it is about. Neither writes any
// `regions`, and both are placed — in the one region a build that serves one
// places everything in — so being unplaced is not a reason either is broken. The
// same audit over projects/with/application finds nothing broken at all, so the
// finding asserted here is the one thing the two differ in.
func TestAnApplicationWithNothingToRunIsBroken(t *testing.T) {
	broken := brokenIn(t, "refused/application/noartifact")
	require.Contains(t, broken, "application: names no image and declares no build")
	require.NotContains(t, broken, "application: is not placed anywhere")

	require.Empty(t, brokenIn(t, "with/application"),
		"the project that runs has nothing broken, so the refusal above is about the image")

	const id = "QmZc65DUedgswsy18Ljq3GKmbX86Y7RWPJbosfPCdAVLWy"
	r, err := pkgregister.New(pkgregister.WithStore(pkgregister.NewMemStore()))
	require.NoError(t, err)
	d, err := buildDeploy.New(
		buildDeploy.WithProjects(configures{}),
		buildDeploy.WithCheckout(corpus{filepath.Join("projects", "refused/application/noartifact")}),
		buildDeploy.WithLocalRegister(r),
		buildDeploy.WithCloudFqdn("tau.dream.test"),
	)
	require.NoError(t, err)
	err = d.Run(t.Context(), backend.Work{JobID: "j", RepositoryKey: "github.com/acme/" + id,
		Ref: "refs/heads/main", CommitSHA: "c", ProjectID: id}, &buildLog{})
	require.ErrorIs(t, err, buildDeploy.ErrBroken)
	require.ErrorContains(t, err, "names no image and declares no build")

	envs, err := r.Environments(t.Context(), id)
	require.NoError(t, err)
	require.Empty(t, envs, "nothing is published for a refused project")
}

// A PROJECT THAT SAYS NO BRANCH DEPLOYS ANYWHERE IS REFUSED, by the compiler and
// so by the deploy, and nothing is published. It is with/function/wasm minus its
// table.
func TestAProjectWithNoEnvironmentsIsRefused(t *testing.T) {
	const id = "QmZDqydqrb5sFAbuqHeNPuQQpAHWbBHLUcPkM9gDeuSY6D"
	dir := filepath.Join("projects", "refused/environments/missing")

	c, err := schema.New(schema.WithLocal(dir), schema.WithBranch("main"))
	require.NoError(t, err)
	_, _, err = c.Compile(context.Background())
	require.ErrorContains(t, err, "required group 'environments'")

	r, err := pkgregister.New(pkgregister.WithStore(pkgregister.NewMemStore()))
	require.NoError(t, err)
	d, err := buildDeploy.New(
		buildDeploy.WithProjects(configures{}),
		buildDeploy.WithCheckout(corpus{dir}),
		buildDeploy.WithLocalRegister(r),
		buildDeploy.WithCloudFqdn("tau.dream.test"),
	)
	require.NoError(t, err)
	err = d.Run(t.Context(), backend.Work{JobID: "j", RepositoryKey: "github.com/acme/" + id,
		Ref: "refs/heads/main", CommitSHA: "c", ProjectID: id}, &buildLog{})
	require.ErrorIs(t, err, buildDeploy.ErrNoDeployment)
	require.ErrorContains(t, err, "declares no environments")

	envs, err := r.Environments(t.Context(), id)
	require.NoError(t, err)
	require.Empty(t, envs, "nothing is published for a refused project")
}

// A FUNCTION SAYS WHICH TIER RUNS IT. wasm and js are both functions, so the runtime
// is the author's to write, and a value the kind does not run is refused naming the
// ones it does. Both are the compiler's refusals, so the deploy makes them too and
// nothing is published. Each project is with/function/wasm with one line changed.
func TestAFunctionWithoutATierItRunsOnIsRefused(t *testing.T) {
	for _, tc := range []struct{ project, id, reason string }{
		{"refused/function/noruntime", "QmckTZTPqB6a5thN1vZNAjoXUBdEUXWL3vA1gFxcx3xBk9", "required attribute 'runtime'"},
		{"refused/function/unknownruntime", "QmTSpD9SVJriUVp9EHdp1okVF71Ua9FvmeCYXwFABkQiD7", `invalid value "nodejs"; valid values here: wasm, js`},
	} {
		t.Run(tc.project, func(t *testing.T) {
			dir := filepath.Join("projects", tc.project)
			c, err := schema.New(schema.WithLocal(dir), schema.WithBranch("main"))
			require.NoError(t, err)
			_, _, err = c.Compile(context.Background())
			require.ErrorContains(t, err, tc.reason)

			r, err := pkgregister.New(pkgregister.WithStore(pkgregister.NewMemStore()))
			require.NoError(t, err)
			d, err := buildDeploy.New(
				buildDeploy.WithProjects(configures{}),
				buildDeploy.WithCheckout(corpus{dir}),
				buildDeploy.WithLocalRegister(r),
				buildDeploy.WithCloudFqdn("tau.dream.test"),
			)
			require.NoError(t, err)
			err = d.Run(t.Context(), backend.Work{JobID: "j", RepositoryKey: "github.com/acme/" + tc.id,
				Ref: "refs/heads/main", CommitSHA: "c", ProjectID: tc.id}, &buildLog{})
			require.ErrorContains(t, err, tc.reason)

			envs, err := r.Environments(t.Context(), tc.id)
			require.NoError(t, err)
			require.Empty(t, envs, "nothing is published for a refused project")
		})
	}
}

// brokenIn is every Broken finding the audit makes about one corpus project.
func brokenIn(t *testing.T, name string) []string {
	t.Helper()
	c, err := schema.New(schema.WithLocal(filepath.Join("projects", name)), schema.WithBranch("main"))
	require.NoError(t, err)
	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err, "%s compiles; any refusal is the audit's", name)

	var out []string
	for _, f := range schema.Audit(obj.Flat(), schema.StubResolver()) {
		if f.Severity == schema.Broken {
			out = append(out, f.Kind+": "+f.Title)
		}
	}
	return out
}
