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
