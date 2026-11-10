package secrets

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/p2p/peer/peertest"
)

func TestAtRestSealsWithTheConfiguredKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	node := peertest.Mock(t, ctx)
	t.Cleanup(func() { node.Close() })

	key, err := GenerateAtRestKey()
	require.NoError(t, err)
	require.NotEmpty(t, key)

	configured, err := newAtRest(node.Host(), key)
	require.NoError(t, err)
	assert.Contains(t, configured.posture(), "configured at-rest key")

	share := []byte("this stands in for a key share")

	sealed, err := configured.seal(share)
	require.NoError(t, err)
	require.NotContains(t, string(sealed), string(share), "the share is on disk in the clear")

	opened, err := configured.open(sealed)
	require.NoError(t, err)
	assert.Equal(t, share, opened)

	// A different key must not open it. This is the whole point: the material
	// lives outside the data directory, so taking the directory is not enough.
	other, err := GenerateAtRestKey()
	require.NoError(t, err)
	wrong, err := newAtRest(node.Host(), other)
	require.NoError(t, err)

	_, err = wrong.open(sealed)
	require.Error(t, err, "a share opened under the wrong key")
}

func TestAtRestFallsBackToTheNodeAndSaysSo(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	node := peertest.Mock(t, ctx)
	t.Cleanup(func() { node.Close() })

	fallback, err := newAtRest(node.Host(), "")
	require.NoError(t, err)

	// Weaker, and it must not be quiet about it: the key sits in the same
	// directory as the shares it protects.
	assert.Contains(t, fallback.posture(), "node-local material only")

	sealed, err := fallback.seal([]byte("share"))
	require.NoError(t, err)

	opened, err := fallback.open(sealed)
	require.NoError(t, err)
	assert.Equal(t, []byte("share"), opened)
}

func TestGeneratedKeysAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for range 32 {
		key, err := GenerateAtRestKey()
		require.NoError(t, err)
		require.False(t, seen[key], "GenerateAtRestKey repeated a key")
		seen[key] = true
	}
}
