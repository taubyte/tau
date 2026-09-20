package database

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func addressed(t *testing.T, s Spec) string {
	t.Helper()
	prefix, err := s.Prefix()
	require.NoError(t, err)
	return prefix
}

// TestOneIdUnderTwoModelsIsTwoStores.
//
// THIS IS THE COLLISION THE MODEL IS IN THE PATH FOR. A resource id survives an
// edit — that is what lets a rotated credential arrive under the same key — so
// an author who rewrites `kv:` as `vector:` keeps it, and the engines share one
// store on a node. Without the model in the path the vector store opened onto
// the kv store's bytes, and a change of mind read back as corruption.
func TestOneIdUnderTwoModelsIsTwoStores(t *testing.T) {
	same := func(model Type) Spec {
		return Spec{Type: model, Project: "shop", Environment: "prod", Name: "QmSessions"}
	}

	seen := map[string]Type{}
	for _, model := range []Type{KV, SQL, Document, Vector} {
		prefix := addressed(t, same(model))
		if other, held := seen[prefix]; held {
			t.Fatalf("%s and %s address the same bytes at %s", other, model, prefix)
		}
		seen[prefix] = model
	}

	// AND NONE OF THEM REACHES INTO ANOTHER, which is a stronger statement than
	// "they differ": every read here is a List over a prefix, so one prefix
	// being the start of another would list the other's rows as its own.
	for mine := range seen {
		for theirs := range seen {
			if mine != theirs {
				require.False(t, strings.HasPrefix(theirs, mine), "%s reaches into %s", mine, theirs)
			}
		}
	}
}

// TestASeparatorInAnySegmentIsRefused, which is the isolation: without it a
// database literally named "../production/ledger" in staging would be a
// readable path into production's.
func TestASeparatorInAnySegmentIsRefused(t *testing.T) {
	for _, s := range []Spec{
		{Type: KV, Project: "shop", Environment: "staging", Name: "../production/ledger"},
		{Type: KV, Project: "shop", Environment: "a/b", Name: "ledger"},
		{Type: KV, Project: "a/b", Environment: "prod", Name: "ledger"},
		{Type: Type("kv/../vector"), Project: "shop", Environment: "prod", Name: "ledger"},
	} {
		_, err := s.Prefix()
		require.ErrorIs(t, err, ErrSeparator, "%v", s)
	}
}

// TestASpecWithAHoleAddressesNothing. The registry's probe is one of these — it
// opens with no project and no environment to prove the SURFACE — so this is a
// refusal the engines report on use rather than on open.
func TestASpecWithAHoleAddressesNothing(t *testing.T) {
	for _, s := range []Spec{
		{Type: KV, Environment: "prod", Name: "ledger"},
		{Type: KV, Project: "shop", Name: "ledger"},
		{Type: KV, Project: "shop", Environment: "prod"},
		{Project: "shop", Environment: "prod", Name: "ledger"},
	} {
		_, err := s.Prefix()
		require.ErrorIs(t, err, ErrUnaddressable, "%v", s)
	}
}
