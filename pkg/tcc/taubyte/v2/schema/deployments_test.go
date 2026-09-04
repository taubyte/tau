package schema_test

import (
	"testing"

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
