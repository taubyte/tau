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

	// Listening, because a node that serves anything is, and because what its
	// own peerstore holds about it is part of what the guards below are for.
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
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

	// Our own record is what our own advertisement looks like coming back out
	// of routing, and it answers everything else admissible asks: libp2p
	// leaves the local host's protocols out of its own peerstore — measured,
	// GetProtocols on our own id is empty — which is precisely the case an
	// advertisement is allowed to carry, and our own addresses are reachable
	// by every test this package can make.
	require.NotEmpty(t, h.Addrs())
	h.Peerstore().AddAddrs(h.ID(), h.Addrs(), time.Hour)
	protos, err := h.Peerstore().GetProtocols(h.ID())
	require.NoError(t, err)
	require.Empty(t, protos)

	d.mu.Lock()
	defer d.mu.Unlock()

	assert.False(t, d.admissible("", true))
	// Advertising is not evidence enough to talk to yourself: the node
	// running the service does not reach it over the network.
	assert.False(t, d.admissible(h.ID(), true))
}

// TestTheQuarantineIsSweptRatherThanKept is about the map, not the peer. Every
// Failed and every lost connection writes an entry keyed by peer id, and
// admissible only ever reads them: the entry for a peer that comes back is
// cleared by its own reconnection, and the entry for a peer that does not come
// back is cleared by nothing. On a long-lived node in a swarm with churn that
// is one entry per peer it ever failed to reach, held for the life of the
// process. The reap is what forgets them, so this runs at the interval the
// reap runs at in production.
func TestTheQuarantineIsSweptRatherThanKept(t *testing.T) {
	d, _ := guarded(t, WithRetryInterval(DefaultRetryInterval))

	for range 3 {
		d.Failed(someone(t))
	}

	d.mu.Lock()
	held := len(d.quarantine)
	d.mu.Unlock()
	require.Equal(t, 3, held, "Failed did not hold the peers out at all")

	require.Eventually(t, func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return len(d.quarantine) == 0
	}, 5*time.Second, 10*time.Millisecond, "an expired quarantine is kept for good")
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

// TestAnEventDoesNotEraseWhyAPeerWasAdmitted is about the memory in the entry.
// A peer found through routing is admitted on the advertisement alone —
// nothing has identified it, so the peerstore's protocol list for it is empty
// — and entry.advertised is the only place that fact is kept.
//
// Every event then arrives as reconsider(pid, false), because an event carries
// a peer id and this package reads the peerstore for everything else. One of
// them arriving before identify does is ordinary: the connectedness change
// when the caller finally dials the peer beats the identify that follows it by
// a round trip. Reading the entry back is what stops that event dropping the
// peer it is announcing.
func TestAnEventDoesNotEraseWhyAPeerWasAdmitted(t *testing.T) {
	d, h := guarded(t)
	pid := someone(t)

	// What a routing result leaves behind: an address, no protocols, and the
	// fact that something advertised it. The address is the guard host's own
	// because nothing here dials anything; it only has to be reachable-looking.
	require.NotEmpty(t, h.Addrs())
	h.Peerstore().AddAddrs(pid, h.Addrs(), time.Hour)
	protos, err := h.Peerstore().GetProtocols(pid)
	require.NoError(t, err)
	require.Empty(t, protos, "identify got there first, so this proves nothing")

	d.reconsider(pid, true)
	require.Equal(t, []peer.ID{pid}, d.Known())

	// What every event looks like from here.
	d.reconsider(pid, false)
	assert.Equal(t, []peer.ID{pid}, d.Known(),
		"an event carrying no protocol news dropped a peer routing had named")
}
