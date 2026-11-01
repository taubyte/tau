package database

import (
	"testing"
	"time"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
)

// reaching is a real service on one host and this package's Client on another, the
// seam a worker uses.
func reaching(t *testing.T, holds *says) *Client {
	t.Helper()

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	server, err := mn.GenPeer()
	require.NoError(t, err)
	caller, err := mn.GenPeer()
	require.NoError(t, err)
	require.NoError(t, mn.LinkAll())
	require.NoError(t, mn.ConnectAllButSelf())

	registry := database.NewRegistry()
	require.NoError(t, registry.Register(t.Context(), &accounting{}))

	s, err := New(server,
		WithEngines(registry),
		WithRegister(&published{revision: 1, object: compiled()}),
		WithHoldings(holds),
		WithIdle(time.Hour))
	require.NoError(t, err)
	require.NoError(t, s.Start(t.Context()))
	t.Cleanup(func() { s.Close() })

	client, err := NewClient(caller, server.ID())
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	return client
}

func sessionsGrant() invoke.Grant {
	return invoke.Grant{Kind: "database", Label: "sessions", Handle: projectID + "/prod/" + kvID}
}

func TestAClientReadsAndWritesThroughTheService(t *testing.T) {
	client := reaching(t, &says{held: true})
	ctx := t.Context()

	kv, err := client.OpenKV(ctx, sessionsGrant())
	require.NoError(t, err)

	// A miss is the tier's miss and never an outage.
	_, err = kv.Get(ctx, "absent")
	require.ErrorIs(t, err, wasmtier.ErrNotFound)

	require.NoError(t, kv.Put(ctx, "session:1", []byte("alice")))
	value, err := kv.Get(ctx, "session:1")
	require.NoError(t, err)
	require.Equal(t, []byte("alice"), value)

	require.NoError(t, kv.Put(ctx, "session:2", []byte("bob")))
	keys, err := kv.Keys(ctx, "session:")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"session:1", "session:2"}, keys)

	require.NoError(t, kv.Delete(ctx, "session:1"))
	_, err = kv.Get(ctx, "session:1")
	require.ErrorIs(t, err, wasmtier.ErrNotFound)

	// DELETING WHAT IS NOT THERE SUCCEEDS: the caller wanted it gone.
	require.NoError(t, kv.Delete(ctx, "absent"))
}

func TestAMalformedHandleOpensNothing(t *testing.T) {
	client := reaching(t, &says{held: true})

	for _, handle := range []string{"", "shop", "shop/prod", "shop/prod/id/extra"} {
		_, err := client.OpenKV(t.Context(), invoke.Grant{Handle: handle})
		require.ErrorIs(t, err, ErrNoID, "%q", handle)
	}
}

func TestAClientAsksForADigestAndAHold(t *testing.T) {
	client := reaching(t, &says{held: true})
	ctx := t.Context()

	at := address{project: projectID, environment: "prod", id: kvID}

	held, err := client.Hold(ctx, at)
	require.NoError(t, err)
	require.True(t, held.held)

	first, err := client.Digest(ctx, at)
	require.NoError(t, err)
	require.True(t, first.held)

	// The digest follows the data, since equal digests are the release gate.
	kv, err := client.OpenKV(ctx, sessionsGrant())
	require.NoError(t, err)
	require.NoError(t, kv.Put(ctx, "session:1", []byte("alice")))

	second, err := client.Digest(ctx, at)
	require.NoError(t, err)
	require.NotEqual(t, first.sum, second.sum, "a write did not move the digest")
	require.Equal(t, first.keys+1, second.keys)
}

func TestAClientHearsHeldFalse(t *testing.T) {
	client := reaching(t, &says{held: false})

	answer, err := client.Digest(t.Context(), address{project: projectID, environment: "prod", id: kvID})
	require.NoError(t, err)
	require.False(t, answer.held)
	require.Empty(t, answer.sum)
}

func TestAClientSeesWhyItWasRefused(t *testing.T) {
	client := reaching(t, &says{held: true})
	ctx := t.Context()

	_, err := client.OpenKV(ctx, invoke.Grant{Handle: projectID + "/prod/QmNotDeclared"})
	require.NoError(t, err, "opening carries no call")

	kv, err := client.OpenKV(ctx, invoke.Grant{Handle: projectID + "/prod/QmNotDeclared"})
	require.NoError(t, err)
	_, err = kv.Get(ctx, "k")
	require.Error(t, err)

	// A sql database reached through kv is the caller's mistake and says so.
	wrong, err := client.OpenKV(ctx, invoke.Grant{Handle: projectID + "/prod/" + sqlID})
	require.NoError(t, err)
	_, err = wrong.Get(ctx, "k")
	require.Error(t, err)
}
