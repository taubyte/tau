package discovery

import (
	"context"
	"slices"
	"testing"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"
)

// THE BUS IS THE ONE PIECE THAT NEEDS A REAL SWARM. Everything else in this
// package is a state machine over a map; this is a gossipsub topic, and what it
// gets wrong — a topic joined per publish, a subscription nobody cancels, a
// slow reader stalling the mesh — is invisible without one.

// newSwarm is n peers on one mocknet, handed over once every one has a gossip
// stream to every other.
//
// EVERY PEER GOSSIPS BEFORE IT IS CONNECTED. A gossipsub opens its stream to a
// peer when it starts or when identify says the peer gossips, and never retries
// one that failed. A peer connected before the other gossiped hears of it only
// from an identify push: sent once, and limited per subnet, which every mocknet
// peer shares. Miss it and that peer never sends the other anything.
func newSwarm(t *testing.T, n int) []*pubsub.PubSub {
	t.Helper()

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	hosts := make([]host.Host, n)
	out := make([]*pubsub.PubSub, n)
	for i := range hosts {
		h, err := mn.GenPeer()
		require.NoError(t, err)
		ps, err := pubsub.NewGossipSub(t.Context(), h)
		require.NoError(t, err)
		hosts[i], out[i] = h, ps
	}
	require.NoError(t, mn.LinkAll())
	require.NoError(t, mn.ConnectAllButSelf())

	for i, ps := range out {
		for j, h := range hosts {
			if i == j {
				continue
			}
			require.Eventually(t, func() bool {
				return slices.Contains(ps.ListPeers(""), h.ID())
			}, 30*time.Second, 10*time.Millisecond, "peer %d never opened a stream to peer %d", i, j)
		}
	}
	return out
}

// TestEveryPeerOfALargeSwarmHasAStreamToEveryOther when the swarm is handed
// over. A host takes ten identify pushes in a burst from one subnet, then one
// every five seconds, so sixteen mocknet peers are past it: a swarm that
// connected before it gossiped left some peers never sending to others.
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
func TestTwoCloudsDoNotMeet(t *testing.T) {
	swarm := newSwarm(t, 2)

	acme, err := NewBus(swarm[0], "acme")
	require.NoError(t, err)

	other, err := NewBus(swarm[1], "somebody-else")
	require.NoError(t, err)

	reports, err := other.Subscribe(t.Context())
	require.NoError(t, err)

	for range 5 {
		require.NoError(t, acme.Publish(t.Context(), []byte("acme's business")))
	}

	select {
	case got := <-reports:
		t.Fatalf("a report crossed clouds: %q", got)
	case <-time.After(500 * time.Millisecond):
	}
}

// TestTheTopicIsScopedAndNeverEmpty. A cloud nobody named still has to have a
// topic, or every unnamed deployment would share one.
func TestTopicOf(t *testing.T) {
	require.Equal(t, "/tau/discovery/v2/acme", TopicOf("acme"))
	require.Equal(t, "/tau/discovery/v2/default", TopicOf(""))
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
