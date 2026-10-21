package mesh

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	peercore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/keypair"
	"github.com/taubyte/tau/p2p/peer"
)

// TestAMeshAdmitsOnlyItsOwn: every mesh has its own CA, so a peer of one
// cannot join another even on the same loopback.
func TestAMeshAdmitsOnlyItsOwn(t *testing.T) {
	a, err := New(t.Context(), "A")
	require.NoError(t, err)
	defer a.Close()
	b, err := New(t.Context(), "b")
	require.NoError(t, err)
	defer b.Close()
	require.Equal(t, "a", a.Name())
	require.NotNil(t, a.CA())

	first, err := a.Add(nil)
	require.NoError(t, err)
	second, err := a.Add(func(peer.Node) error { return nil })
	require.NoError(t, err)
	require.Equal(t, network.Connected, second.Peer().Network().Connectedness(first.ID()))
	require.Len(t, a.Peers(), 2)

	stranger, err := b.Add(nil)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.Error(t, stranger.Peer().Connect(ctx, peercore.AddrInfo{ID: first.ID(), Addrs: first.Peer().Addrs()}))

	_, err = a.Credentials([]byte("not a key"))
	require.Error(t, err)
	creds, err := a.Credentials(keypair.NewRaw())
	require.NoError(t, err)
	require.Equal(t, "a", creds.Cloud)

	require.DirExists(t, a.Root())
	require.NoError(t, a.Context().Err())

	closed := make(chan struct{})
	a.OnClose(closer(func() error { close(closed); return nil }))
	_, err = a.Add(func(peer.Node) error { return context.Canceled })
	require.ErrorIs(t, err, context.Canceled)
	a.Close()
	<-closed
	require.Error(t, a.Context().Err())
	require.NoDirExists(t, a.Root())
}

type closer func() error

func (c closer) Close() error { return c() }
