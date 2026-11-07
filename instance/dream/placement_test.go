//go:build dreaming

package dream_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

func TestAPlacementThisBuildDoesNotServeIsRefused(t *testing.T) {
	c, err := schema.New(schema.WithLocal(projectDir("with/placement")), schema.WithBranch("main"))
	require.NoError(t, err)
	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err)

	want := []string{
		`project/placement: allows regions "region-1", "region-2", which this build does not serve; everything under it runs in "default"`,
		`namespace/eu: allows region "region-1", which this build does not serve; everything under it runs in "default"`,
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
