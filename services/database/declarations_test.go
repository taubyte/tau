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
			"projects/" + projectID + "/database/knowledge/links": []any{
				"branches/main/projects/" + projectID + "/database/" + vectorID},
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
		out["regions"] = region
	}
	return out
}

func sessions(t *testing.T, d *declarations) declared {
	t.Helper()
	got, err := d.of(context.Background(), address{project: projectID, environment: "prod", id: kvID})
	require.NoError(t, err)
	return got
}

// TestADatabaseInheritsTheScopesRegions, which is how a project says where
// things go ONCE rather than on every resource. It is the SAME WORD a resource
// writes — one concept narrowing down, rather than an allowance and a placement
// that differ by a letter.
//
// NOTHING LOWERED IT INTO THE DATABASE: the compiler writes no placement a
// resource did not author, so this is the walk up. A copy would be a fact
// decompile could not give back.
func TestADatabaseInheritsTheProjectsPlacement(t *testing.T) {
	d := declaring(t, map[string]any{
		"id": projectID, "name": "shop",
		"regions":  []string{"eu"},
		"database": map[string]any{kvID: kvAt(nil)},
	})

	require.Equal(t, []string{"eu"}, sessions(t, d).regions)
}

// TestADatabasesOwnPlacementWins. The narrower statement decides; the project's
// is what everything that said nothing falls back to.
func TestADatabasesOwnPlacementWins(t *testing.T) {
	d := declaring(t, map[string]any{
		"id": projectID, "name": "shop",
		"regions": []string{"eu", "us", "ap"},
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

// TestAShadowKeepsStateWhereItWasWrittenInside. A shadow is a deployment over
// another environment's state, so placing that state was never its to do — the
// vocabulary gives it no regions field at all.
func TestAShadowTakesTheAllowanceItWasWrittenInside(t *testing.T) {
	d := declaring(t, map[string]any{
		"id": projectID, "name": "shop",
		"regions": []string{"eu", "us"},
		"environments": map[string]any{
			"production": map[string]any{
				"branches": []string{"main"},
				"regions":  []string{"eu"},
				"shadows":  map[string]any{"ab": map[string]any{"branches": []string{"main"}}},
			},
		},
		"database": map[string]any{kvID: kvAt(nil)},
	})

	for _, env := range []string{"production", "ab"} {
		got, err := d.of(context.Background(), address{project: projectID, environment: env, id: kvID})
		require.NoError(t, err, env)
		require.Equal(t, []string{"eu"}, got.regions, "%s did not keep state where production does", env)
	}
}

// TestAnEnvironmentThatNamesNoRegionsTakesTheProjects, because `regions` is one
// word at every level: a scope's is where everything under it may be AND where
// anything that does not say otherwise is placed.
func TestAnEnvironmentInheritsTheProjectsRegions(t *testing.T) {
	d := declaring(t, map[string]any{
		"id": projectID, "name": "shop",
		"regions": []string{"eu", "us"},
		"environments": map[string]any{
			"prod": map[string]any{"branches": []string{"main"}},
		},
		"database": map[string]any{kvID: kvAt(nil)},
	})

	require.Equal(t, []string{"eu", "us"}, sessions(t, d).regions)
}

// TestWHATONLYONEMODELUNDERSTANDSTRAVELSASANOPTION.
//
// A vector's width and its distance metric mean nothing to the other three
// models, so they do not get a field each on the spec — they are carried under
// the vocabulary's own attribute names, which is what lets a reader find the
// attribute by searching the schema for the string.
func TestPerModelDeclarationsTravelAsOptions(t *testing.T) {
	d := declaring(t, map[string]any{
		"id": projectID, "name": "shop",
		"regions": []string{"eu"},
		"database": map[string]any{
			vectorID: map[string]any{
				"name": "knowledge", "type": "vector",
				"vector": map[string]any{
					"engine": "distributed", "consensus": "crdt",
					"dimensions": float64(1536), "metric": "cosine",
				},
			},
		},
	})

	// READING A DECLARATION IS NOT AGREEING TO SERVE IT. `of` reads what the
	// author wrote; whether this cloud serves that model is asked separately,
	// so a model nothing here implements still resolves.
	got, err := d.of(context.Background(),
		address{project: projectID, environment: "prod", id: vectorID})
	require.NoError(t, err)

	require.Equal(t, "1536", got.spec.Options["dimensions"])
	require.Equal(t, "cosine", got.spec.Options["metric"])

	// AND IT IS REFUSED BY NAME when somebody asks this cloud to serve it.
	require.ErrorIs(t, got.serves(), ErrModelUnserved)
}

// TestACONSENSUSNOBODYWROTEISTHEDEFAULT, which is what a connector gets: it
// resolves nothing, because somebody else's server does.
func TestAnUnwrittenConsensusIsTheDefault(t *testing.T) {
	require.Equal(t, defaultConsensus, declared{}.wants())
	require.Equal(t, "raft", declared{consensus: "raft"}.wants())
}
