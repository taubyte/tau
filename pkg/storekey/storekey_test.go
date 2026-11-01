package storekey_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/storekey"
)

const prefix = "shopA/prod/kv/cache-id/"

func TestAKeyCannotLeaveItsPrefix(t *testing.T) {
	for _, key := range []string{
		"../../../victim/prod/kv/their-id/session",
		"a/../../../../etc",
		"../",
		"..",
		"sub/../../../..",
	} {
		require.ErrorIs(t, storekey.Check(prefix, key), storekey.ErrEscapes, "key %q", key)
	}
}

func TestAKeyThatStaysIsAllowed(t *testing.T) {
	for _, key := range []string{
		"session",
		"users/42/name",
		"a.b.c",
		"..hidden",
		"x..y",
		"deep/../shallow",
		"",
	} {
		require.NoError(t, storekey.Check(prefix, key), "key %q", key)
	}
}

func TestCanonicalRefusesWhatCleanWouldChange(t *testing.T) {
	for _, key := range []string{
		"h/../p/victim/prod",
		"h/..",
		"p/./shop/prod",
		"h//doubled",
		"o/a/../../b",
	} {
		require.ErrorIs(t, storekey.Canonical(key), storekey.ErrNotCanonical, "key %q", key)
	}
	for _, key := range []string{
		"h/shop.example.com",
		"p/shop/prod",
		"o/QmSomeObjectId",
		"c/shop/prod",
		"",
	} {
		require.NoError(t, storekey.Canonical(key), "key %q", key)
	}
}
