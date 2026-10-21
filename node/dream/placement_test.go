//go:build dreaming

package dream_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// TestAPlacementThisBuildDoesNotServeIsRefused holds projects/with/placement: a
// project that says where it runs, on a build with no regions. The project's
// allowance and its cache's pin are each Broken by name, so a deploy is refused
// before it publishes rather than running somewhere it was told not to be.
func TestAPlacementThisBuildDoesNotServeIsRefused(t *testing.T) {
	c, err := schema.New(schema.WithLocal(projectDir("with/placement")), schema.WithBranch("main"))
	require.NoError(t, err)
	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err)

	want := []string{
		`project/placement: allows regions "region-1", "region-2", which this build does not serve; everything under it runs in "default"`,
		`cache/sessions: is placed in region "region-1", which this build does not serve; it runs in "default"`,
	}
	for _, found := range [][]schema.Finding{
		schema.Audit(obj.Flat(), schema.StubResolver()),
		schema.Audit(obj.Flat(), schema.StubResolver(), schema.WithRegions(schema.WithNoRegions())),
	} {
		var broken []string
		for _, f := range found {
			if f.Severity == schema.Broken {
				broken = append(broken, f.Kind+"/"+f.Name+": "+f.Title)
			}
		}
		require.ElementsMatch(t, want, broken)
	}
}

// A DREAM TEST CAN BE ONE REGION OF A LARGER CLOUD: as region-1, the cache pinned to region-1 is
// placed there and naming region-2 is not an error — region-2 exists, this build is not it.
func TestDreamCanMockARegion(t *testing.T) {
	c, err := schema.New(schema.WithLocal(projectDir("with/placement")), schema.WithBranch("main"))
	require.NoError(t, err)
	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err)

	var broken []string
	for _, f := range schema.Audit(obj.Flat(), schema.StubResolver(), schema.WithRegions(schema.WithRegion("region-1"))) {
		if f.Severity == schema.Broken {
			broken = append(broken, f.Kind+"/"+f.Name+": "+f.Title)
		}
	}
	require.Empty(t, broken)
}
