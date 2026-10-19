//go:build dreaming

package dream_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// TestAPlacementThisBuildDoesNotServeIsKeptAndSaid holds projects/with/placement:
// a project that says where it runs, on a build that serves one region.
//
// THE STATEMENT IS KEPT AND SAID, not dropped and not refused. The project's
// allowance and its cache's pin both name regions this build does not serve, so
// both compile with a Check naming them, the cache is placed in the default
// region, and nothing is broken — the project deploys here and means the same
// thing on a cloud that serves eu.
//
// IT IS THE AUDIT'S ANSWER, read off the compiled corpus project; deploying it
// says the same lines in the build log (services/build/deploy), and the cache
// it pins runs nothing, so there is no request that would show more.
func TestAPlacementThisBuildDoesNotServeIsKeptAndSaid(t *testing.T) {
	c, err := schema.New(schema.WithLocal(projectDir("with/placement")), schema.WithBranch("main"))
	require.NoError(t, err)
	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err)

	var checks []string
	for _, f := range schema.Audit(obj.Flat(), schema.StubResolver()) {
		require.NotEqual(t, schema.Broken, f.Severity, f.String())
		if f.Severity == schema.Check {
			checks = append(checks, f.Kind+"/"+f.Name+": "+f.Title)
		}
	}
	require.ElementsMatch(t, []string{
		`project/placement: allows regions "eu", "us", which this build does not serve; everything under it runs in "default"`,
		`cache/sessions: is placed in region "eu", which this build does not serve; it runs in "default"`,
	}, checks)
}
