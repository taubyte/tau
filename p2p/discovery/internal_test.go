package discovery

import (
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The branches below are guards rather than paths: a peer id that is empty or
// our own, an event of a type nobody subscribed to, a set already at its cap.
// Nothing outside can produce them today, which is exactly why they are worth
// pinning — the day one of them fires it should be a no-op, not a surprise.

const guardProtocol = "/tau/test/discovery/guards"

func guarded(t *testing.T, opts ...Option) (*Discovery, host.Host) {
	t.Helper()

	h, err := libp2p.New(libp2p.NoListenAddrs)
	require.NoError(t, err)
	t.Cleanup(func() { h.Close() })

	d, err := New(t.Context(), h, guardProtocol,
		append([]Option{WithRetryInterval(time.Hour)}, opts...)...)
	require.NoError(t, err)
	t.Cleanup(d.Close)

	return d, h
}

func someone(t *testing.T) peer.ID {
	t.Helper()
	h, err := libp2p.New(libp2p.NoListenAddrs)
	require.NoError(t, err)
	t.Cleanup(func() { h.Close() })
	return h.ID()
}

func TestAnEventOfSomeOtherKind(t *testing.T) {
	d, _ := guarded(t)

	d.onEvent(struct{ Peer peer.ID }{Peer: someone(t)})

	assert.Empty(t, d.Known())
}

func TestNobodyAndOurselvesAreNotPeers(t *testing.T) {
	d, h := guarded(t)

	d.mu.Lock()
	defer d.mu.Unlock()

	assert.False(t, d.admissible("", true))
	// Advertising is not evidence enough to talk to yourself: the node
	// running the service does not reach it over the network.
	assert.False(t, d.admissible(h.ID(), true))
}

func TestTheCapHoldsWhereverAPeerArrives(t *testing.T) {
	d, _ := guarded(t, WithMaxPeers(1))
	first, second := someone(t), someone(t)

	d.mu.Lock()
	defer d.mu.Unlock()

	d.admitLocked(first, false)
	d.admitLocked(second, true)
	assert.Len(t, d.peers, 1)
	assert.Contains(t, d.peers, first)

	// Arriving again is not arriving: it upgrades what is known, and does not
	// take a second place in a full set.
	require.False(t, d.peers[first].advertised)
	d.admitLocked(first, true)
	assert.Len(t, d.peers, 1)
	assert.True(t, d.peers[first].advertised)
}
