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
