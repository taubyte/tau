package schema_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/spf13/afero"
	"gotest.tools/v3/assert"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

func TestABranchResolvesToItsDeployments(t *testing.T) {
	envs := envsOfFixture(t)

	// main feeds production and its shadow. Ordered by name (the document's order is
	// a map's), each shadow right after its parent.
	assert.DeepEqual(t, schema.DeploymentNames(envs, "main"), []string{"preview", "production", "ab"})

	// master is production's other branch, and the shadow does not take it.
	assert.DeepEqual(t, schema.DeploymentNames(envs, "master"), []string{"preview", "production"})

	// release/* is staging's, and preview's "*" takes it too.
	assert.DeepEqual(t, schema.DeploymentNames(envs, "release/1.4"), []string{"preview", "staging"})

	// Anything else is a preview, which is what `branches: ["*"]` says.
	assert.DeepEqual(t, schema.DeploymentNames(envs, "feature/checkout"), []string{"preview"})
}

func TestTheGlobCrossesSlashes(t *testing.T) {
	assert.Assert(t, schema.MatchBranch("*", "feature/checkout"))
	assert.Assert(t, schema.MatchBranch("release/*", "release/1/0"))
	assert.Assert(t, schema.MatchBranch("release/*", "release/1.4"))
	assert.Assert(t, !schema.MatchBranch("release/*", "hotfix/1.4"))

	assert.Assert(t, schema.MatchBranch("main", "main"))
	assert.Assert(t, !schema.MatchBranch("main", "mainline"))
	assert.Assert(t, !schema.MatchBranch("main", "release/main"))

	assert.Assert(t, schema.MatchBranch("v?", "v2"))
	assert.Assert(t, !schema.MatchBranch("v?", "v12"))

	// A branch name cannot contain a regexp metacharacter that is not also a
	// glob one, but the literal half is quoted anyway: a dot is a dot.
	assert.Assert(t, !schema.MatchBranch("release/1.4", "release/1x4"))

	// An empty glob matches nothing rather than everything, which is the safe
	// direction for a value that decides where code is deployed.
	assert.Assert(t, !schema.MatchBranch("", ""))
	assert.Assert(t, !schema.MatchBranch("", "main"))
}

func TestNoTableFeedsNothing(t *testing.T) {
	assert.Assert(t, schema.Deployments(nil, "main") == nil)
}

func TestTheTableReadsTheSameBeforeAndAfterACompile(t *testing.T) {
	before := envsOfFixture(t)

	c, err := schema.New(
		schema.WithLocal(envFixture),
		schema.WithProjectEnvironments(afero.NewOsFs(), envFixture),
		schema.WithEnvironment("production"),
		schema.WithBranch("main"),
	)
	assert.NilError(t, err)

	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	raw, err := json.Marshal(obj.Flat())
	assert.NilError(t, err)

	var decoded map[string]any
	assert.NilError(t, json.Unmarshal(raw, &decoded))

	after := schema.EnvironmentsIn(decoded)
	assert.DeepEqual(t, before, after)

	// And so does the branch resolution that reads them.
	for _, branch := range []string{"main", "master", "release/1.4", "feature/x"} {
		assert.DeepEqual(t,
			schema.DeploymentNames(before, branch),
			schema.DeploymentNames(after, branch))
	}
}

func TestATableThatIsNotOne(t *testing.T) {
	assert.Assert(t, schema.EnvironmentsIn(nil) == nil)
	assert.Assert(t, schema.EnvironmentsIn(map[string]any{}) == nil)
	assert.Assert(t, schema.EnvironmentsIn(map[string]any{"environments": "nope"}) == nil)

	// Entries that are not objects are skipped; the ones beside them survive.
	got := schema.EnvironmentsIn(map[string]any{"environments": map[string]any{
		"broken": "nope",
		"real":   map[string]any{"branches": []any{"main", 7, "next"}},
	}})
	assert.Equal(t, len(got), 1)
	assert.Equal(t, got[0].Name, "real")
	// A non-string in a branch list is dropped rather than stringified into a
	// branch nobody will ever push.
	assert.DeepEqual(t, got[0].Branches, []string{"main", "next"})

	// It reads an object with or without its wrapper, like ReachableNames.
	wrapped := schema.EnvironmentsIn(map[string]any{"object": map[string]any{
		"environments": map[string]any{"prod": map[string]any{"branches": []any{"main"}}},
	}})
	assert.Equal(t, len(wrapped), 1)
	assert.Equal(t, wrapped[0].Name, "prod")
}
