package peer

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ipfs/go-cid"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	peercore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/keypair"
	"github.com/taubyte/tau/p2p/sectls"
)

func admitted(t *testing.T, ca *sectls.CA) ([]byte, *sectls.Credentials) {
	t.Helper()
	raw := keypair.NewRaw()
	key, err := crypto.UnmarshalPrivateKey(raw)
	require.NoError(t, err)
	cert, err := ca.Issue(key.GetPublic(), "cloud", time.Hour)
	require.NoError(t, err)
	creds, err := sectls.NewCredentials(ca.CertificatePEM(), cert, "cloud", key)
	require.NoError(t, err)
	return raw, creds
}

func admittedNode(t *testing.T, ca *sectls.CA) Node {
	t.Helper()
	raw, creds := admitted(t, ca)
	n, err := New(t.Context(), t.TempDir(), raw, creds, []string{"/ip4/127.0.0.1/tcp/0"}, nil, true, false)
	require.NoError(t, err)
	t.Cleanup(n.Close)
	return n
}

// TestAdmittedNodesMeetAndKeepEachOtherAlive: two nodes with certificates from
// one CA connect, and the keep-alive topic protects the connection.
func TestAdmittedNodesMeetAndKeepEachOtherAlive(t *testing.T) {
	ca, err := sectls.NewCA("cloud", time.Hour)
	require.NoError(t, err)
	a, b := admittedNode(t, ca), admittedNode(t, ca)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, b.Peer().Connect(ctx, peercore.AddrInfo{ID: a.ID(), Addrs: a.Peer().Addrs()}))

	keepCtx, keepCancel := context.WithCancel(t.Context())
	defer keepCancel()
	require.NoError(t, a.NewPubSubKeepAlive(keepCtx, keepCancel, "keep"))
	require.NoError(t, b.NewPubSubKeepAlive(keepCtx, keepCancel, "keep"))
	require.Eventually(t, func() bool {
		b.PubSubPublish(ctx, "keep", []byte("keep"))
		return a.Peer().ConnManager().IsProtected(b.ID(), "/keep/keep")
	}, 10*time.Second, 100*time.Millisecond)

	got := make(chan *pubsub.Message, 1)
	subCtx, subCancel := context.WithCancel(t.Context())
	require.NoError(t, a.PubSubSubscribeContext(subCtx, "ctx", func(m *pubsub.Message) {
		select {
		case got <- m:
		default:
		}
	}, func(error) {}))
	require.Eventually(t, func() bool {
		b.PubSubPublish(ctx, "ctx", []byte("hello"))
		select {
		case m := <-got:
			return string(m.Data) == "hello"
		default:
			return false
		}
	}, 10*time.Second, 100*time.Millisecond)
	subCancel()

	// A stranger from another CA is not admitted.
	other, err := sectls.NewCA("cloud", time.Hour)
	require.NoError(t, err)
	stranger := admittedNode(t, other)
	require.Error(t, stranger.Peer().Connect(ctx, peercore.AddrInfo{ID: a.ID(), Addrs: a.Peer().Addrs()}))
	require.NotEqual(t, network.Connected, a.Peer().Network().Connectedness(stranger.ID()))
}

func TestABackoffGrowsAndIsCapped(t *testing.T) {
	ph := &peerHandler{nextDelay: initialDelay}
	previous := ph.nextDelay
	for range 50 {
		d := ph.nextBackoff()
		require.LessOrEqual(t, d, maxBackoff)
		require.True(t, d >= previous || d >= maxBackoff-maxBackoff*maxBackoffJitter/100)
		previous = d
	}
	require.GreaterOrEqual(t, ph.nextDelay, maxBackoff-maxBackoff*maxBackoffJitter/100)
}

func TestAFileNobodyHasIsNotFound(t *testing.T) {
	ca, err := sectls.NewCA("cloud", time.Hour)
	require.NoError(t, err)
	n := admittedNode(t, ca)
	id, err := n.AddFileForCid(strings.NewReader("present"))
	require.NoError(t, err)
	f, err := n.GetFileFromCid(t.Context(), id)
	require.NoError(t, err)
	f.Close()

	missing, err := cid.Decode("bafkreigh2akiscaildcqabsyg3dfr6chu3fgpregiymsck7e7aqa4s52zy")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	_, err = n.GetFileFromCid(ctx, missing)
	require.Error(t, err)

	n.Close()
	_, err = n.GetFileFromCid(t.Context(), id)
	require.ErrorIs(t, err, errorClosed)
	_, err = n.AddFileForCid(strings.NewReader("x"))
	require.ErrorIs(t, err, errorClosed)
}
