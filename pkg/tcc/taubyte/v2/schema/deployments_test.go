package schema_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/spf13/afero"
	"gotest.tools/v3/assert"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// A PUSH IS ALREADY AN ANSWER TO "DEPLOY WHERE". The table says which branches
// feed which deployments; this is the read of it that a push makes.
func TestABranchResolvesToItsDeployments(t *testing.T) {
	envs := envsOfFixture(t)

	// main feeds production AND the shadow written inside it. A caller taking
	// the first match would quietly stop deploying one of the two.
	//
	// The order is by name — the document's own is a Go map's and is not
	// recoverable — with each shadow kept right after the parent it shadows.
	assert.DeepEqual(t, schema.DeploymentNames(envs, "main"), []string{"preview", "production", "ab"})

	// master is production's other branch, and the shadow does not take it.
	assert.DeepEqual(t, schema.DeploymentNames(envs, "master"), []string{"preview", "production"})

	// release/* is staging's, and preview's "*" takes it too.
	assert.DeepEqual(t, schema.DeploymentNames(envs, "release/1.4"), []string{"preview", "staging"})

	// Anything else is a preview, which is what `branches: ["*"]` says.
	assert.DeepEqual(t, schema.DeploymentNames(envs, "feature/checkout"), []string{"preview"})
}

// `*` CROSSES `/`, and path.Match's does not — which is the whole reason this
// matcher exists. Under path.Match, `preview: ["*"]` would match none of the
// branches anybody pushes and `release/*` would miss `release/1/0`.
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

// A project with no table declares one deployment, and this names none: there
// is no name in the document to return, and inventing one here would be the
// schema naming a deployment its author did not.
func TestNoTableFeedsNothing(t *testing.T) {
	assert.Assert(t, schema.Deployments(nil, "main") == nil)
}

// THE TWO READERS ANSWER THE SAME SHAPE. One reads a document before a compile
// and one reads what the compile produced; neither is a special case of the
// other, and a drift between them would mean a config repository's deploy and a
// code repository's build disagreed about which deployments a branch feeds.
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

// An object with no table, and one whose table is not one, are both "no
// deployments declared" rather than a panic — the input is a decoded JSON map
// and nothing has vetted its shape.
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
