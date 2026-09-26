package storekey_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/storekey"
)

const prefix = "shopA/prod/kv/cache-id/"

// TestAKeyCannotLeaveItsTenant — the whole point. Every one of these reached
// another project's entries before this existed, because the stores put the
// joined string through ds.NewKey and path.Clean collapsed the traversal.
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

// TestAnOrdinaryKeyIsUntouched, including the hierarchy List depends on: a key
// may contain "/" and must, or a prefix scan has nothing to scan.
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

// TestAKeyBuiltFromAFixedPrefixHasToBeCanonical — the register's shape, where
// there is no tenant prefix to stay inside and the question is whether the key
// is the one it looks like.
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
