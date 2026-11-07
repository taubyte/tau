package stats

import (
	"context"
	"slices"
	"testing"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/libp2p/go-libp2p/p2p/protocol/identify"
	"github.com/stretchr/testify/require"
)

// THE BUS IS THE ONE PIECE THAT NEEDS A REAL SWARM. Everything else in this
// package is a state machine over a map; this is a gossipsub topic, and what it
// gets wrong — a topic joined per publish, a subscription nobody cancels, a
// slow reader stalling the mesh — is invisible without one.

// newSwarm is n peers on one mocknet, handed over once every one has a gossip
// stream to every other.
//
// A GOSSIPSUB OPENS ITS STREAM TO A PEER WHEN IDENTIFY SAYS THE PEER GOSSIPS,
// and never retries one that failed. Every peer gossips before it is connected,
// but that is not enough: identify answers from a snapshot of the host's
// protocols that it refreshes on its own goroutine, so a peer connected before
// the refresh is told the other does not gossip. The correction is an identify
// push, sent once and limited per subnet, which every mocknet peer shares. So a
// pair whose identify left out gossip connects again, and identifies again.
func newSwarm(t *testing.T, n int) []member {
	t.Helper()

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	hosts := make([]host.Host, n)
	out := make([]member, n)
	for i := range out {
		h, err := mn.GenPeer()
		require.NoError(t, err)
		ps, err := pubsub.NewGossipSub(t.Context(), h)
		require.NoError(t, err)
		hosts[i] = h
		out[i] = member{ps, h.ID()}
	}
	require.NoError(t, mn.LinkAll())
	require.NoError(t, mn.ConnectAllButSelf())

	for i, h := range hosts {
		for j, other := range hosts {
			if i == j {
				continue
			}
			deadline := time.Now().Add(30 * time.Second)
			for !saysItGossips(t, h, other.ID()) {
				require.True(t, time.Now().Before(deadline), "peer %d never heard that peer %d gossips", i, j)
				require.NoError(t, mn.DisconnectPeers(h.ID(), other.ID()))
				_, err := mn.ConnectPeers(h.ID(), other.ID())
				require.NoError(t, err)
			}
		}
	}

	for i, m := range out {
		for j, other := range out {
			if i == j {
				continue
			}
			require.Eventually(t, func() bool {
				return slices.Contains(m.ListPeers(""), other.id)
			}, 30*time.Second, 10*time.Millisecond, "peer %d never opened a stream to peer %d", i, j)
		}
	}
	return out
}

// saysItGossips waits until h has identified every connection to p, then
// reports whether p said it gossips.
func saysItGossips(t *testing.T, h host.Host, p peer.ID) bool {
	ids := h.(interface{ IDService() identify.IDService }).IDService()
	for _, c := range h.Network().ConnsToPeer(p) {
		<-ids.IdentifyWait(c)
	}
	supported, err := h.Peerstore().SupportsProtocols(p, pubsub.GossipSubDefaultProtocols...)
	require.NoError(t, err)
	return len(supported) > 0
}

// member is a swarm peer's gossipsub and the id the other peers know it by.
type member struct {
	*pubsub.PubSub
	id peer.ID
}

// TestEveryPeerOfALargeSwarmHasAStreamToEveryOther when the swarm is handed
// over. A host takes ten identify pushes in a burst from one subnet, then one
// every five seconds, so sixteen mocknet peers are past it: a peer identified
// from a snapshot taken before it gossiped was never sent to by the other.
func TestEveryPeerOfALargeSwarmHasAStreamToEveryOther(t *testing.T) {
	swarm := newSwarm(t, 16)

	for i, ps := range swarm {
		require.Len(t, ps.ListPeers(""), len(swarm)-1, "peer %d has no stream to some other peer", i)
	}
}

// TestAReportReachesTheOtherSideOfTheMesh.
func TestAReportCrossesTheMesh(t *testing.T) {
	swarm := newSwarm(t, 2)

	sender, err := NewBus(swarm[0], "acme")
	require.NoError(t, err)

	receiver, err := NewBus(swarm[1], "acme")
	require.NoError(t, err)

	reports, err := receiver.Subscribe(t.Context())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		if err := sender.Publish(t.Context(), []byte("a report")); err != nil {
			return false
		}
		select {
		case got := <-reports:
			return string(got) == "a report"
		case <-time.After(200 * time.Millisecond):
			return false
		}
	}, 30*time.Second, 100*time.Millisecond)
}

// TestTwoCloudsDoNotPoolTheirLoadFigures. Two universes on one machine, or two
// clouds bridged by a stray peer, would otherwise route each other's traffic.
//
// A report that could cross is given every chance to: the publisher knows the
// other cloud's subscriber before it publishes, and a subscriber of its own
// cloud hears the report before the other one is checked.
func TestTwoCloudsDoNotMeet(t *testing.T) {
	swarm := newSwarm(t, 3)
	publisher, stranger, neighbour := swarm[0], swarm[1], swarm[2]

	other, err := NewBus(stranger, "somebody-else")
	require.NoError(t, err)
	crossed, err := other.Subscribe(t.Context())
	require.NoError(t, err)

	same, err := NewBus(neighbour, "acme")
	require.NoError(t, err)
	heard, err := same.Subscribe(t.Context())
	require.NoError(t, err)

	acme, err := NewBus(publisher, "acme")
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return slices.Contains(publisher.ListPeers(TopicOf("somebody-else")), stranger.id) &&
			slices.Contains(publisher.ListPeers(TopicOf("acme")), neighbour.id)
	}, 30*time.Second, 10*time.Millisecond, "the publisher never learned who subscribes to each topic")

	require.Eventually(t, func() bool {
		if err := acme.Publish(t.Context(), []byte("acme's business")); err != nil {
			return false
		}
		select {
		case <-heard:
			return true
		case <-time.After(200 * time.Millisecond):
			return false
		}
	}, 30*time.Second, 100*time.Millisecond, "acme's own subscriber never heard acme")

	select {
	case got := <-crossed:
		t.Fatalf("a report crossed clouds: %q", got)
	case <-time.After(500 * time.Millisecond):
	}
}

// TestTheTopicIsScopedAndNeverEmpty. A cloud nobody named still has to have a
// topic, or every unnamed deployment would share one.
func TestTopicOf(t *testing.T) {
	require.Equal(t, "/tau/stats/v2/acme", TopicOf("acme"))
	require.Equal(t, "/tau/stats/v2/default", TopicOf(""))
	require.NotEqual(t, TopicOf("a"), TopicOf("b"))
}

// TestASubscriptionEndsWithItsContext, and closing the channel is how a reader
// knows. One that had to cancel AND unsubscribe would have two ways to get it
// wrong.
func TestASubscriptionEndsWithItsContext(t *testing.T) {
	swarm := newSwarm(t, 1)

	bus, err := NewBus(swarm[0], "acme")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	reports, err := bus.Subscribe(ctx)
	require.NoError(t, err)

	cancel()

	require.Eventually(t, func() bool {
		_, open := <-reports
		return !open
	}, 5*time.Second, 50*time.Millisecond, "the channel was never closed")
}

func TestABusNeedsSomewhereToGossip(t *testing.T) {
	_, err := NewBus(nil, "acme")
	require.Error(t, err)
}

// TestFreshnessIsTheEffectiveTolerance, not the raw field.
//
// A CONSUMER THAT READ Config.Fresh DIRECTLY GOT ZERO whenever an operator left
// it unset — every row is older than nothing, so every lookup found nothing and
// the service it was looking for appeared not to exist. That is what this
// answers once.
func TestFreshness(t *testing.T) {
	require.Equal(t, freshIntervals*DefaultInterval, Config{}.Freshness())
	require.Equal(t, 90*time.Second, Config{Interval: 30 * time.Second}.Freshness())
	require.Equal(t, time.Minute, Config{Fresh: time.Minute}.Freshness())

	// A config that does not validate still answers, because a caller asking
	// how stale is too stale is not the caller that should be refused.
	require.Equal(t, freshIntervals*DefaultInterval, Config{Interval: -time.Second}.Freshness())
}
