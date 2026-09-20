package database

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
)

// declaring builds a declarations over one compiled project, so a test can ask
// what a database IS without standing up a service to ask through.
func declaring(t *testing.T, root map[string]any) *declarations {
	t.Helper()

	doc := map[string]any{
		"object": root,
		"indexes": map[string]any{
			"projects/" + projectID + "/database/sessions/links": []any{
				"branches/main/projects/" + projectID + "/database/" + kvID},
		},
	}
	raw, err := json.Marshal(doc)
	require.NoError(t, err)

	projects, err := register.NewCache(&published{revision: 1, object: raw}, specs.Load)
	require.NoError(t, err)
	return &declarations{projects: projects}
}

// kvAt is one kv database, with whatever region block the case is about.
func kvAt(region map[string]any) map[string]any {
	out := map[string]any{
		"name": "sessions", "type": "kv",
		"kv": map[string]any{"engine": "distributed", "consensus": "crdt"},
	}
	if region != nil {
		out["region"] = region
	}
	return out
}

func sessions(t *testing.T, d *declarations) declared {
	t.Helper()
	got, err := d.of(context.Background(), address{project: projectID, environment: "prod", id: kvID})
	require.NoError(t, err)
	return got
}

// TestADatabaseInheritsTheProjectsPlacement, which is how a project says where
// things go ONCE rather than on every resource.
//
// NOTHING LOWERED IT INTO THE DATABASE: the compiler writes no region a resource
// did not author, so this is the walk up. A copy would be a fact decompile could
// not give back.
func TestADatabaseInheritsTheProjectsPlacement(t *testing.T) {
	d := declaring(t, map[string]any{
		"id": projectID, "name": "shop",
		"region":   map[string]any{"policy": "pinned", "name": "eu"},
		"database": map[string]any{kvID: kvAt(nil)},
	})

	require.Equal(t, []string{"eu"}, sessions(t, d).regions)
}

// TestADatabasesOwnPlacementWins. The narrower statement decides; the project's
// is what everything that said nothing falls back to.
func TestADatabasesOwnPlacementWins(t *testing.T) {
	d := declaring(t, map[string]any{
		"id": projectID, "name": "shop",
		"region": map[string]any{"policy": "pinned", "name": "eu"},
		"database": map[string]any{kvID: kvAt(
			map[string]any{"policy": "failover", "main": "us", "standby": "ap"})},
	})

	require.Equal(t, []string{"ap", "us"}, sessions(t, d).regions,
		"the project's default overrode what the resource itself said")
}

// TestNeitherIsNowhere. The audit calls this Broken at compile; if one reaches a
// runtime anyway it is placed across the allowance rather than guessed at.
func TestNeitherIsNowhere(t *testing.T) {
	d := declaring(t, map[string]any{
		"id": projectID, "name": "shop",
		"database": map[string]any{kvID: kvAt(nil)},
	})

	require.Equal(t, []string{anyRegion}, sessions(t, d).regions)
}
