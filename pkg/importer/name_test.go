package importer_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/importer"
)

func TestASourceNameBecomesAV2Name(t *testing.T) {
	for _, c := range []struct {
		in      string
		want    string
		renamed bool
	}{
		{"api", "api", false},
		{"my_api", "my_api", false},
		{"_internal", "_internal", false},
		{"api2", "api2", false},

		{"my-api", "my_api", true},
		{"web-1", "web_1", true},
		{"api/users/[id]", "api_users__id_", true},
		{"api.heavy.ts", "api_heavy_ts", true},
		{"UPPER-case", "UPPER_case", true},

		// A LEADING DIGIT IS PREFIXED AND NOT DROPPED. Dropping it makes
		// "1api" and "api" the same name, and one of them then silently
		// replaces the other.
		{"1api", "n1api", true},
		{"2", "n2", true},

		{"", "", false},
	} {
		got, renamed := importer.Name(c.in)
		require.Equal(t, c.want, got, c.in)
		require.Equal(t, c.renamed, renamed, c.in)
	}
}

func TestNameIsDeterministic(t *testing.T) {
	for range 10 {
		got, _ := importer.Name("my-api.v2")
		require.Equal(t, "my_api_v2", got)
	}
}

func TestRenamingSaysWhatChanged(t *testing.T) {
	f := importer.Renamed("my_api", "services", "my-api", "my_api")

	require.Equal(t, importer.Approximated, f.Level)
	require.Equal(t, "NAME_NOT_A_V2_NAME", f.Code)
	require.Equal(t, "my_api", f.Resource)
	require.Equal(t, "services", f.Key)
	require.Contains(t, f.Detail, "my-api")
	require.Contains(t, f.Detail, "my_api")
	require.Contains(t, f.Detail, "runbook",
		"the cost of a rename is everything outside this config that still says the old name")
}
