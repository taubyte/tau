package schema_test

import (
	"context"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// compileSourceIn compiles one package whose code lives in its own repository, read
// for the environment named.
func compileSourceIn(t *testing.T, env, body string) map[string]any {
	t.Helper()

	fs := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(fs, "/project.yaml",
		[]byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nenvironments:\n    prod:\n        branches: [\"*\"]\nname: shop\nregions: [region-1]\n"), 0o644))
	assert.NilError(t, afero.WriteFile(fs, "/package/docs.yaml",
		[]byte("id: QmRgVLmWTBpgTbCPnHRUvKGnHqHf7Yd7t8ZffKeRXPkRLL\ntype: assets\n"+body), 0o644))

	opts := []schema.Option{schema.WithVirtual(fs, "/"), schema.WithBranch("main"),
		schema.WithEnvironments("production", "staging")}
	if env != "" {
		opts = append(opts, schema.WithEnvironment(env))
	}
	c, err := schema.New(opts...)
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	return only(t, resources(t, obj.Flat(), "package"))
}

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

func TestAnUnsuffixedBranchServesEveryEnvironment(t *testing.T) {
	body := "source:\n" +
		"    git: https://github.com/acme/docs\n" +
		"    branch: main\n"

	for _, env := range []string{"production", "staging"} {
		got := compileSourceIn(t, env, body)
		assert.Equal(t, nested(got, "source", "branch"), "main", "env %s", env)
	}
}

func TestAnEnvironmentWithNoBranchOfItsOwnTakesNone(t *testing.T) {
	body := "source:\n" +
		"    git: https://github.com/acme/docs\n" +
		"    branch_production: main\n"

	staging := compileSourceIn(t, "staging", body)
	_, present := nestedOK(staging, "source", "branch")
	assert.Assert(t, !present,
		"staging said nothing, so it takes the repository default — not production's branch")
}
