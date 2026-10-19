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
// IN DREAM THE STATEMENT IS KEPT AND SAID, not dropped and not refused: the
// harness runs with TryOneRegion, so the project's allowance and its cache's
// pin each compile with a Check naming them, the cache is placed in the default
// region, and nothing is broken.
//
// A BUILD IS STRICT. The default validator answers the same statements Broken,
// so a deploy of this project to a one-region cloud is refused before it
// publishes rather than running somewhere it was told not to be.
func TestAPlacementThisBuildDoesNotServeIsKeptAndSaid(t *testing.T) {
	c, err := schema.New(schema.WithLocal(projectDir("with/placement")), schema.WithBranch("main"))
	require.NoError(t, err)
	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err)

	want := []string{
		`project/placement: allows regions "eu", "us", which this build does not serve; everything under it runs in "default"`,
		`cache/sessions: is placed in region "eu", which this build does not serve; it runs in "default"`,
	}

	var checks []string
	for _, f := range schema.Audit(obj.Flat(), schema.StubResolver(), schema.WithRegions(schema.TryOneRegion())) {
		require.NotEqual(t, schema.Broken, f.Severity, f.String())
		if f.Severity == schema.Check {
			checks = append(checks, f.Kind+"/"+f.Name+": "+f.Title)
		}
	}
	require.ElementsMatch(t, want, checks)

	var broken []string
	for _, f := range schema.Audit(obj.Flat(), schema.StubResolver()) {
		if f.Severity == schema.Broken {
			broken = append(broken, f.Kind+"/"+f.Name+": "+f.Title)
		}
	}
	require.ElementsMatch(t, want, broken, "the default validator refuses what this build cannot serve")
}

// A DREAM TEST CAN STAND IN FOR ONE REGION: as eu, the cache pinned to eu is placed
// there and clean, and only the allowance's "us" — a region this stand-in does not
// serve — is refused.
func TestDreamCanMockARegion(t *testing.T) {
	c, err := schema.New(schema.WithLocal(projectDir("with/placement")), schema.WithBranch("main"))
	require.NoError(t, err)
	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err)

	var broken []string
	for _, f := range schema.Audit(obj.Flat(), schema.StubResolver(), schema.WithRegions(schema.AsRegion("eu"))) {
		if f.Severity == schema.Broken {
			broken = append(broken, f.Kind+"/"+f.Name+": "+f.Title)
		}
	}
	require.Equal(t, []string{
		`project/placement: allows region "us", which this build does not serve; everything under it runs in "eu"`,
	}, broken)
}
