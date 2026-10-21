package schema_test

import (
	"context"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// compileSourceIn compiles one website whose content lives in its own repository,
// read for the environment named.
func compileSourceIn(t *testing.T, env, body string) map[string]any {
	t.Helper()

	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nname: shop\nregions: [region-1]\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/website/docs.yaml",
		[]byte("id: QmRgVLmWTBpgTbCPnHRUvKGnHqHf7Yd7t8ZffKeRXPkRLL\n"+body), 0o644))

	opts := []schema.Option{schema.WithVirtual(fs, "/"), schema.WithBranch("main"),
		schema.WithEnvironments("production", "staging")}
	if env != "" {
		opts = append(opts, schema.WithEnvironment(env))
	}
	c, err := schema.New(opts...)
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	sites := resources(t, obj.Flat(), "website")
	return only(t, sites)
}

// TestAGitSourceReadsADifferentBranchPerEnvironment.
//
// `environments[].branches` already says which branches feed a deployment, and
// that rule stopped at the CONFIG repository. A resource whose content lives in
// its own repo had ONE branch for every environment, so a staging deployment
// built whatever production builds — which makes a staging deployment of a site
// change impossible to express.
//
// The environment is the axis this whole schema varies on (`Varying(EnvironmentVar)`
// on the root), so it is the axis this varies on too.
func TestAGitSourceReadsADifferentBranchPerEnvironment(t *testing.T) {
	body := "source:\n" +
		"    git: https://github.com/acme/docs\n" +
		"    branch_production: main\n" +
		"    branch_staging: develop\n"

	prod := compileSourceIn(t, "production", body)
	assert.Equal(t, nested(prod, "source", "branch"), "main")

	staging := compileSourceIn(t, "staging", body)
	assert.Equal(t, nested(staging, "source", "branch"), "develop",
		"a staging deployment has to be able to build a different commit than production")
}

// TestAnUnsuffixedBranchStillServesEveryEnvironment — the common case has to
// keep working: one branch, written once, read by every deployment.
func TestAnUnsuffixedBranchServesEveryEnvironment(t *testing.T) {
	body := "source:\n" +
		"    git: https://github.com/acme/docs\n" +
		"    branch: main\n"

	for _, env := range []string{"production", "staging"} {
		got := compileSourceIn(t, env, body)
		assert.Equal(t, nested(got, "source", "branch"), "main", "env %s", env)
	}
}

// TestAnEnvironmentWithNothingSaidAboutItTakesTheDefaultBranch.
//
// THE ABSENCE IS THE ANSWER AND NOT A FALLBACK TO ANOTHER ENVIRONMENT'S. An
// omitted branch means "the repository's default branch", which is what the
// field has always meant; it must not quietly mean "whatever production reads",
// because that would deploy production's code to staging on a typo.
func TestAnEnvironmentWithNoBranchOfItsOwnTakesNone(t *testing.T) {
	body := "source:\n" +
		"    git: https://github.com/acme/docs\n" +
		"    branch_production: main\n"

	staging := compileSourceIn(t, "staging", body)
	_, present := nestedOK(staging, "source", "branch")
	assert.Assert(t, !present,
		"staging said nothing, so it takes the repository default — not production's branch")
}
