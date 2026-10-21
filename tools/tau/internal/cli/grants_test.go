package cli

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGrants(t *testing.T) {
	x := newTester(t)
	x.login()
	x.link("proj-1")
	require.Contains(t, x.ok("grants", "list", "--dir", x.dir), "nobody else")
	require.Equal(t, "[]\n", x.ok("grants", "list", "--dir", x.dir, "-o", "json"))

	require.Contains(t, x.ok("grants", "add", "u-ada", "viewer", "--dir", x.dir), "u-ada is viewer on proj-1 (grant g-1)")
	require.Contains(t, x.run("grants", "add", "u-ada", "emperor", "--dir", x.dir).stderr, "not a role")
	var gs []map[string]any
	require.NoError(t, json.Unmarshal([]byte(x.ok("grants", "list", "--dir", x.dir, "-o", "json")), &gs))
	require.Equal(t, "viewer", gs[0]["role"])
	require.Contains(t, x.ok("grants", "list", "--dir", x.dir), "u-ada")

	require.Contains(t, x.run("grants", "revoke", "g-1", "--dir", x.dir).stderr, "--yes")
	require.Contains(t, x.ok("grants", "revoke", "g-1", "--dir", x.dir, "--yes"), "revoked g-1")
	require.Contains(t, x.run("grants", "revoke", "g-1", "--dir", x.dir, "--yes").stderr, "not found")
	require.Contains(t, x.run("grants", "list", "--project", "proj-other").stderr, "not found")

	for _, args := range [][]string{{"grants", "list"}, {"grants", "add", "u", "viewer"}, {"grants", "revoke", "g", "--yes"}} {
		require.Contains(t, x.run(append(args, "--dir", t.TempDir())...).stderr, "not linked")
		require.NotZero(t, x.run(append(args, "--project", "proj-1", "--cloud", "::")...).code)
	}
}
