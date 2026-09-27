package worker

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/specs"
)

func fixture(t *testing.T) *specs.Project {
	t.Helper()
	p, err := specs.Load(compiled(t))
	require.NoError(t, err)
	return p
}

// TestFailoverIsMainThenStandby, bounded by the deployment's allowance — the
// fixture's `api` fails over from eu to us inside a project allowed eu and us.
func TestFailoverIsMainThenStandby(t *testing.T) {
	p := fixture(t)
	api, _, err := p.Resource(nil, "application", "api")
	require.NoError(t, err)

	got := RegionsOf(p, "prod", api)
	require.Equal(t, [][]string{{"eu"}, {"us"}}, got.Tiers)
	require.Equal(t, []string{"eu", "us"}, got.Allowance)
}

// TestAResourceThatAsksNothingIsNotBoundedByItsAllowance. NOT TRUE of what the
// vocabulary says a scope's `regions` means, and asserted so it cannot change
// quietly: an undeclared workload runs wherever placement puts it, even in a
// project that lists regions. See RegionsOf.
func TestAResourceThatAsksNothingIsNotBoundedByItsAllowance(t *testing.T) {
	p := fixture(t)
	require.NotEmpty(t, p.Allowance("prod"), "the fixture's project lists regions")

	fn, _, err := p.Resource(nil, "function", "checkout")
	require.NoError(t, err)
	require.Equal(t, Regions{}, RegionsOf(p, "prod", fn))
}

func TestEachPolicyIsRead(t *testing.T) {
	p := fixture(t)
	for _, c := range []struct {
		name  string
		block map[string]any
		want  [][]string
	}{
		{"pinned", map[string]any{"policy": "pinned", "in": []any{"eu", "", "us"}}, [][]string{{"eu", "us"}}},
		{"failover with no standby", map[string]any{"policy": "failover", "main": "eu"}, [][]string{{"eu"}}},
		{"cdn with homes", map[string]any{"policy": "cdn", "in": []any{"ap"}}, [][]string{{"ap"}}},
		{"cdn with none is anywhere", map[string]any{"policy": "cdn"}, nil},
		{"a pin with only holes asks for nothing it can say", map[string]any{"policy": "pinned", "in": []any{""}}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := RegionsOf(p, "prod", specs.Resource{"regions": c.block})
			require.Equal(t, c.want, got.Tiers)
		})
	}
}

// TestARegionBlockNothingPlacesIsRefusedByName. A cache pinned to eu is held
// wherever the node holding it is, so the grant is refused rather than served
// outside the pin; a database and a kind a tier runs are placed, and are not.
func TestARegionBlockNothingPlacesIsRefusedByName(t *testing.T) {
	pinned := specs.Resource{"regions": map[string]any{"policy": "pinned", "in": []any{"eu"}}}

	for _, kind := range []string{cacheKind, storageKind, volumeKind, syncKind, workflowKind} {
		why := unservable(specs.Reach{Kind: kind, Label: "x", Ops: []string{"get"}}, pinned)
		require.Contains(t, why, "does not place a "+kind+" by region", kind)
	}
	for _, kind := range []string{databaseKind, functionKind} {
		require.Empty(t, unservable(specs.Reach{Kind: kind, Label: "x", Ops: []string{"get"}}, pinned), kind)
	}
	require.Empty(t, unservable(specs.Reach{Kind: cacheKind, Label: "x", Ops: []string{"get"}}, specs.Resource{}),
		"a cache that asks for no region is served")
}
