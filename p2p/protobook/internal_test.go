package protobook

import (
	"io"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Timings no production value can reach, for what only a slow peer shows.

const slow = protocol.ID("/tau/test/protobook/slow")

func pair(t *testing.T) (server, client host.Host) {
	t.Helper()
	for _, h := range []*host.Host{&server, &client} {
		var err error
		*h, err = libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
		require.NoError(t, err)
		t.Cleanup(func() { (*h).Close() })
	}
	require.NoError(t, client.Connect(t.Context(), peer.AddrInfo{ID: server.ID(), Addrs: server.Addrs()}))
	require.Eventually(t, func() bool {
		protos, err := client.Peerstore().GetProtocols(server.ID())
		return err == nil && len(protos) > 0
	}, 5*time.Second, 10*time.Millisecond)
	return server, client
}

// stalling makes h serve slow, holding each of its first stall negotiations for d,
// and counts every one.
func stalling(h host.Host, stall int32, d time.Duration) *atomic.Int32 {
	var n atomic.Int32
	h.Mux().AddHandlerWithFunc(slow, func(p protocol.ID) bool {
		if p != slow {
			return false
		}
		if n.Add(1) <= stall {
			time.Sleep(d)
		}
		return true
	}, func(protocol.ID, io.ReadWriteCloser) error { return nil })
	return &n
}

func tell(t *testing.T, h host.Host, about peer.ID, protos []protocol.ID) {
	t.Helper()
	em, err := h.EventBus().Emitter(new(event.EvtPeerIdentificationCompleted))
	require.NoError(t, err)
	defer em.Close()
	require.NoError(t, em.Emit(event.EvtPeerIdentificationCompleted{Peer: about, Conn: h.Network().ConnsToPeer(about)[0], Protocols: protos}))
}

func listsSlow(h host.Host, about peer.ID) bool {
	protos, err := h.Peerstore().GetProtocols(about)
	return err == nil && slices.Contains(protos, slow)
}

func TestAnAskThatGetsNoAnswerIsAskedAgain(t *testing.T) {
	server, client := pair(t)
	asked := stalling(server, 2, 200*time.Millisecond)
	k, err := keep(t.Context(), client, 50*time.Millisecond, 20*time.Millisecond)
	require.NoError(t, err)
	t.Cleanup(k.Close)

	protos, err := client.Peerstore().GetProtocols(server.ID())
	require.NoError(t, err)
	tell(t, client, server.ID(), append(protos, slow))

	require.Eventually(t, func() bool { return listsSlow(client, server.ID()) }, 5*time.Second, 10*time.Millisecond,
		"a peer slow to answer was never asked again")
	assert.Equal(t, int32(3), asked.Load())
}

func TestClosingEndsAnAskAtOnce(t *testing.T) {
	server, client := pair(t)
	asked := stalling(server, 1, 5*time.Second)
	k, err := keep(t.Context(), client, askTimeout, retryInterval)
	require.NoError(t, err)

	protos, err := client.Peerstore().GetProtocols(server.ID())
	require.NoError(t, err)
	tell(t, client, server.ID(), append(protos, slow))
	require.Eventually(t, func() bool { return asked.Load() == 1 }, 5*time.Second, 10*time.Millisecond)

	began := time.Now()
	k.Close()
	assert.Less(t, time.Since(began), time.Second, "Close waited out a negotiation")
	assert.False(t, listsSlow(client, server.ID()))
}

func TestAPeerThatLeavesIsForgotten(t *testing.T) {
	server, client := pair(t)
	k, err := keep(t.Context(), client, askTimeout, retryInterval)
	require.NoError(t, err)

	protos, err := client.Peerstore().GetProtocols(server.ID())
	require.NoError(t, err)
	tell(t, client, server.ID(), protos)
	require.NoError(t, client.Network().ClosePeer(server.ID()))
	require.Eventually(t, func() bool { return client.Network().Connectedness(server.ID()) != network.Connected },
		5*time.Second, 10*time.Millisecond)
	time.Sleep(100 * time.Millisecond)

	k.Close()
	assert.Empty(t, k.heard)
}
