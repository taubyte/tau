package raft

import (
	"path"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	taupeer "github.com/taubyte/tau/p2p/peer"
)

// offering answers Offer from what it was given, and holds a peer it was told to
// until the test ends: one that never answers.
type offering struct {
	internalClient
	offers  map[peer.ID]offer
	silent  map[peer.ID]bool
	asked   []peer.ID
	release chan struct{}
}

func (o *offering) Offer(target peer.ID) (offer, error) {
	o.asked = append(o.asked, target)
	if o.silent[target] {
		<-o.release
	}
	answer, ok := o.offers[target]
	if !ok {
		return offer{}, ErrNoLeader
	}
	return answer, nil
}

func formationMember(t *testing.T, node taupeer.Node, namespace string, client internalClient) (*cluster, *member) {
	t.Helper()
	c := &cluster{Node: node, namespace: namespace}
	return c, &member{raft: onEngine(t, node, namespace, quickTimeouts), raftClient: client}
}

func TestAMemberPrefersTheFormationThatNamesIt(t *testing.T) {
	node := newMockNode(t)
	c := &cluster{Node: node}
	other := strangerID(t)
	low, high := make([]byte, formationSize), make([]byte, formationSize)
	high[0] = 1
	namesMe := offer{formation: high, members: []peer.ID{other, node.ID()}}
	led := offer{formation: low, leader: other}
	lowest := offer{formation: low}
	higher := offer{formation: high}

	require.True(t, c.prefers(namesMe, led))
	require.False(t, c.prefers(led, namesMe))
	require.True(t, c.prefers(led, lowest))
	require.True(t, c.prefers(lowest, higher))
	require.False(t, c.prefers(higher, lowest))
}

// A MEMBER BOUND TO NO FORMATION BINDS TO ONE ITS PEERS OFFER, picking the same
// one every node would, and waits for no peer that does not answer.
func TestAMemberBindsToAFormationItsPeersOffer(t *testing.T) {
	node := newMockNode(t)
	named, quiet, unbound, gone := strangerID(t), strangerID(t), strangerID(t), strangerID(t)
	theirs := newFormation()
	client := &offering{
		offers: map[peer.ID]offer{
			named:   {formation: theirs, members: []peer.ID{named, node.ID()}},
			unbound: {},
		},
		silent:  map[peer.ID]bool{quiet: true},
		release: make(chan struct{}),
	}
	t.Cleanup(func() { close(client.release) })
	c, m := formationMember(t, node, "binds", client)

	require.False(t, c.bindVia(m, []peer.ID{node.ID(), unbound, gone}), "bound with no formation offered")
	require.Nil(t, m.raft.Formation())
	require.NotContains(t, client.asked, node.ID(), "it asked itself")

	began := time.Now()
	require.True(t, c.bindVia(m, []peer.ID{quiet, unbound, named}))
	require.Less(t, time.Since(began), offerWait+time.Second, "it waited past its time for a peer that never answers")
	require.Equal(t, theirs, m.raft.Formation())

	asked := len(client.asked)
	require.True(t, c.bindVia(m, []peer.ID{named}))
	require.Len(t, client.asked, asked, "a bound member asked for offers")
}

// A BINDING THE STORE REFUSES IS NO BINDING: the group stays bound to none, and a
// bootstrap whose formation cannot be written writes nothing.
func TestABindingTheStoreRefusesIsNone(t *testing.T) {
	const namespace = "bind-refused"
	store := newFaultStore()
	store.failPut(path.Join(RaftStoragePrefix, namespace, "engine", "formation"), errNoSpace)
	node := storeNode{Node: newMockNode(t), store: store}
	theirs := newFormation()
	c, m := formationMember(t, node, namespace, &offering{offers: map[peer.ID]offer{node.ID(): {}}})
	g := m.raft

	require.ErrorIs(t, g.Bind(theirs), errNoSpace)
	require.Nil(t, g.Formation())
	require.ErrorIs(t, g.Bootstrap([]peer.ID{node.ID()}), errNoSpace)
	require.Nil(t, g.Formation())
	require.True(t, g.Pristine(), "a bootstrap whose formation was refused wrote entries")

	other := strangerID(t)
	m.raftClient = &offering{offers: map[peer.ID]offer{other: {formation: theirs, members: []peer.ID{node.ID()}}}}
	require.False(t, c.bindVia(m, []peer.ID{other}), "a refused binding counted as bound")
}

// A GROUP WHOSE ENGINE HAS STOPPED binds to nothing and holds nothing it can say.
func TestAGroupOnAStoppedEngineBindsToNothing(t *testing.T) {
	node := newMockNode(t)
	e := engineFor(node)
	fsm := newKVFSM(t.Context(), node.Store(), path.Join(RaftStoragePrefix, "stopped"))
	g, err := e.open("stopped", groupConfig{timeouts: quickTimeouts, machine: fsm, store: node.Store()})
	require.NoError(t, err)
	require.NoError(t, g.Close())
	e.release()

	require.ErrorIs(t, g.Bind(newFormation()), ErrShutdown)
	require.False(t, g.Pristine())
}

func TestAPeerIsExchangedWithOnceAtATime(t *testing.T) {
	self, other := strangerID(t), strangerID(t)
	pt := newPeerTracker(self)
	require.True(t, pt.startExchange(other))
	require.False(t, pt.startExchange(other), "a second exchange started while one was under way")
	pt.endExchange(other)
	require.True(t, pt.startExchange(other))
}

func TestAnOfferFromAPeerOutsideTheGroupFails(t *testing.T) {
	_, cli := callerOf(t, "offer-outside", sameStore)
	outside := newMockNode(t)
	require.NoError(t, taupeer.LinkAllPeers())
	_, err := cli.Offer(outside.ID())
	require.Error(t, err)
}

// A PEER WHOSE PROBE FOUND NOTHING IS PROBED AGAIN, ever less often: what failed
// may have been the moment and not the peer.
func TestAPeerIsProbedAgainLessOften(t *testing.T) {
	self, other := strangerID(t), strangerID(t)
	pt := newPeerTracker(self)
	require.True(t, pt.probeDue(other))
	require.False(t, pt.probeDue(other), "probed again before its wait")

	waits := []time.Duration{2 * probeFirstWait, 4 * probeFirstWait}
	for _, want := range waits {
		pt.mu.Lock()
		last := pt.probed[other]
		last.next = time.Now().Add(-time.Millisecond)
		pt.probed[other] = last
		pt.mu.Unlock()
		require.True(t, pt.probeDue(other), "never probed again")
		require.Equal(t, want, pt.probed[other].wait)
	}

	pt.mu.Lock()
	pt.probed[other] = probeWait{next: time.Now().Add(-time.Millisecond), wait: probeMaxWait}
	pt.mu.Unlock()
	require.True(t, pt.probeDue(other))
	require.Equal(t, probeMaxWait, pt.probed[other].wait, "the wait grew past its ceiling")
}

// A CANDIDATE NOBODY REACHES IS DROPPED in time, and one reached lately is kept.
func TestACandidateNobodyReachesIsDropped(t *testing.T) {
	self, gone, kept, heardOf := strangerID(t), strangerID(t), strangerID(t), strangerID(t)
	pt := newPeerTracker(self)
	pt.addPeer(gone)
	pt.addPeer(heardOf)
	pt.heardFrom(kept)

	pt.mu.Lock()
	pt.startTime = pt.startTime.Add(-2 * candidateTTL)
	pt.heard[gone] = time.Now().Add(-2 * candidateTTL)
	pt.mu.Unlock()
	pt.prune()

	require.ElementsMatch(t, []peer.ID{kept}, pt.allPeers())
	require.Equal(t, 2, pt.lastPeerCount)
}
