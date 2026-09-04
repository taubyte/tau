package importer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestElsewhereIsNotBlocked. The distinction is the point of the level: a value
// that lives in the source platform's API is not a gap in tau's vocabulary, and
// filing it as Blocked would put "connect your account" on the list of things to
// build.
func TestElsewhereIsNotBlocked(t *testing.T) {
	var r Result
	r.Add(Finding{Key: "healthcheck", Level: Blocked, Code: "NO_TAU_FEATURE:healthcheck",
		Needs: "a health contract"})
	r.Add(Finding{Key: "env", Level: Elsewhere, Code: "SOURCE_WRITE_ONLY:secret"})
	r.Add(Finding{Key: "labels", Level: Ignored, Code: "IGNORED:labels"})

	require.Len(t, r.Blocked(), 1)
	require.Equal(t, "healthcheck", r.Blocked()[0].Key)

	require.Len(t, r.Elsewhere(), 1)
	require.Equal(t, "env", r.Elsewhere()[0].Key)

	require.Len(t, r.AtLevel(Ignored), 1)
	require.Empty(t, r.AtLevel(Approximated))
}

// TestAnElsewhereFindingNamesNoContract, because there is nothing to build.
func TestAnElsewhereFindingNamesNoContract(t *testing.T) {
	f := Finding{Key: "env", Level: Elsewhere, Code: "SOURCE_WRITE_ONLY:secret",
		Detail: "values are write-only in the source"}
	require.Empty(t, f.Needs)
	require.Equal(t, "SOURCE_WRITE_ONLY:secret — values are write-only in the source", f.String())
}

func TestAFindingReadsAsASentence(t *testing.T) {
	require.Equal(t,
		"api: NO_TAU_FEATURE:healthcheck — no vocabulary (needs health.{test,interval})",
		Finding{Resource: "api", Key: "healthcheck", Level: Blocked,
			Code: "NO_TAU_FEATURE:healthcheck", Needs: "health.{test,interval}",
			Detail: "no vocabulary"}.String())
}

// TestFindingsSortStably, so two runs of one importer diff cleanly and a corpus
// report reads the same twice.
func TestFindingsSortStably(t *testing.T) {
	var r Result
	r.Add(Finding{Resource: "web", Key: "restart"})
	r.Add(Finding{Resource: "api", Key: "volumes"})
	r.Add(Finding{Resource: "api", Key: "restart"})
	r.Add(Finding{Key: "networks"})
	r.Sort()

	var got []string
	for _, f := range r.Findings {
		got = append(got, f.Resource+"/"+f.Key)
	}
	require.Equal(t, []string{"/networks", "api/restart", "api/volumes", "web/restart"}, got)
}

func TestSetCreatesTheMapOnDemand(t *testing.T) {
	var r Result
	r.Set("application", "api", map[string]any{"source": map[string]any{"image": "x"}})

	doc, ok := r.Doc("application", "api")
	require.True(t, ok)
	require.Equal(t, map[string]any{"image": "x"}, doc["source"])

	// The kind is per resource, so one result can hold both halves of a
	// project: the workload and the volume it mounts.
	r.Set("volume", "data", map[string]any{"size": "10GB"})
	require.Equal(t, []string{"api"}, r.Names("application"))
	require.Equal(t, []string{"data"}, r.Names("volume"))

	_, ok = r.Doc("application", "data")
	require.False(t, ok, "a name is only a name within a kind")

	// Setting the same kind and name again replaces rather than appends.
	r.Set("application", "api", map[string]any{"source": map[string]any{"image": "y"}})
	require.Len(t, r.Resources, 2)
	doc, _ = r.Doc("application", "api")
	require.Equal(t, map[string]any{"image": "y"}, doc["source"])
}
