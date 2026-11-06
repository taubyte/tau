//go:build dreaming

package dream_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	pkgregister "github.com/taubyte/tau/pkg/register"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	buildDeploy "github.com/taubyte/tau/services/plm/deploy"
	"github.com/taubyte/tau/services/plm/run"
)

func TestAnApplicationWithNothingToRunIsBroken(t *testing.T) {
	broken := brokenIn(t, "refused/application/noartifact")
	require.Contains(t, broken, "package: is of type oci and names no image and no build")
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
	err = planThenRun(t.Context(), d, run.Work{JobID: "j", RepositoryKey: "github.com/acme/" + id,
		Ref: "refs/heads/main", CommitSHA: "c", ProjectID: id}, &buildLog{})
	require.ErrorIs(t, err, buildDeploy.ErrBroken)
	require.ErrorContains(t, err, "is of type oci and names no image and no build")

	envs, err := r.Environments(t.Context(), id)
	require.NoError(t, err)
	require.Empty(t, envs, "nothing is published for a refused project")
}

func TestAWebsiteOfSomethingThatRunsIsRefusedWhereItWasWritten(t *testing.T) {
	broken := brokenIn(t, "refused/website/notfiles")
	require.Equal(t, []string{"website: serves package module, which is of type wasm"}, broken,
		"said once, on the website, not again on the gateway it became")
	require.Empty(t, brokenIn(t, "with/website"), "the websites that serve have nothing broken")

	const id = "QmXfNESr4a7hHHAHtb2xrV7QVEYkcnv67kUe6ozCfPTyUX"
	r, err := pkgregister.New(pkgregister.WithStore(pkgregister.NewMemStore()))
	require.NoError(t, err)
	d, err := buildDeploy.New(
		buildDeploy.WithProjects(configures{}),
		buildDeploy.WithCheckout(corpus{filepath.Join("projects", "refused/website/notfiles")}),
		buildDeploy.WithLocalRegister(r),
		buildDeploy.WithCloudFqdn("tau.dream.test"),
	)
	require.NoError(t, err)
	err = planThenRun(t.Context(), d, run.Work{JobID: "j", RepositoryKey: "github.com/acme/" + id,
		Ref: "refs/heads/main", CommitSHA: "c", ProjectID: id}, &buildLog{})
	require.ErrorIs(t, err, buildDeploy.ErrBroken)
	require.ErrorContains(t, err, "serves package module, which is of type wasm")

	envs, err := r.Environments(t.Context(), id)
	require.NoError(t, err)
	require.Empty(t, envs, "nothing is published for a refused project")
}

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
	err = planThenRun(t.Context(), d, run.Work{JobID: "j", RepositoryKey: "github.com/acme/" + id,
		Ref: "refs/heads/main", CommitSHA: "c", ProjectID: id}, &buildLog{})
	require.ErrorIs(t, err, buildDeploy.ErrNoDeployment)
	require.ErrorContains(t, err, "declares no environments")

	envs, err := r.Environments(t.Context(), id)
	require.NoError(t, err)
	require.Empty(t, envs, "nothing is published for a refused project")
}

func TestAFunctionWithoutATierItRunsOnIsRefused(t *testing.T) {
	refusedAtCompile(t, []refusal{
		{"refused/function/noruntime", "QmckTZTPqB6a5thN1vZNAjoXUBdEUXWL3vA1gFxcx3xBk9", "required attribute 'type'"},
		{"refused/function/unknownruntime", "QmTSpD9SVJriUVp9EHdp1okVF71Ua9FvmeCYXwFABkQiD7", `invalid value "nodejs"; valid values here: wasm, warp, oci, assets`},
	})
}

func TestAStreamDeclaredWronglyIsRefused(t *testing.T) {
	refusedAtCompile(t, []refusal{
		{"refused/stream/local", "QmXrKNgp7fzmMJjcfxqMVpjXaMvgFYwayNrRNVJbDvCdW3", "unknown key 'local'"},
		{"refused/stream/sasl", "QmR9yqy2F319XxtpgNdx6191DcoPEQb2w2iQ3ZNj6EGgrh", `invalid value "gssapi"; valid values here: scram-sha-512, scram-sha-256, plain`},
		{"refused/stream/nocredentials", "QmZ8SdAvGi93BYwtUz4WewTzGrpzFGUWg5yDwevBDv3ghf", "required attribute 'kafka/credentials/name'"},
	})
}

// refusal is a project that must not compile, and why.
type refusal struct{ project, id, reason string }

// refusedAtCompile compiles each project, then runs it through the build as a push
// would, and holds both to refusing it with the reason and publishing nothing.
func refusedAtCompile(t *testing.T, refusals []refusal) {
	for _, tc := range refusals {
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
			err = planThenRun(t.Context(), d, run.Work{JobID: "j", RepositoryKey: "github.com/acme/" + tc.id,
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
