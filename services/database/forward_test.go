package database

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	libp2ptest "github.com/libp2p/go-libp2p/core/test"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/database"
	kvengine "github.com/taubyte/tau/pkg/database/engines/kv"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/placement"
)

// Who answers for a raft database: real routing over real services, with an engine
// refusing the way a follower's store does.

// ledgers is n database services on one mocknet, each with its own engine, all
// reading the same deployment and all able to keep raft.
func ledgers(t *testing.T, n int) ([]*Service, []*accounting) {
	t.Helper()

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	hosts := make([]host.Host, 0, n)
	for range n {
		h, err := mn.GenPeer()
		require.NoError(t, err)
		hosts = append(hosts, h)
	}
	require.NoError(t, mn.LinkAll())
	require.NoError(t, mn.ConnectAllButSelf())

	services := make([]*Service, 0, n)
	engines := make([]*accounting, 0, n)
	for _, h := range hosts {
		engine := &accounting{}
		registry := database.NewRegistry()
		require.NoError(t, registry.Register(t.Context(), engine))
		engine.forgetProbe()

		s, err := New(h,
			WithEngines(registry),
			WithRegister(&published{revision: 1, object: compiled()}),
			WithConsensus(database.CRDT, database.Raft),
			WithIdle(time.Hour))
		require.NoError(t, err)
		require.NoError(t, s.Start(t.Context()))
		t.Cleanup(func() { s.Close() })

		services = append(services, s)
		engines = append(engines, engine)
	}
	return services, engines
}

// ledgerGrant names the raft database the fixture declares, and ledgerAt is the
// same database as an address — the SAME project, which is what makes a write
// through one and a read through the other be about one store.
func ledgerGrant() invoke.Grant {
	return invoke.Grant{Handle: projectID + "/prod/" + raftID}
}

func ledgerAt() address {
	return address{project: projectID, environment: "prod", id: raftID}
}

func TestALedgerIsAnsweredByTheNodeThatLeadsItsGroup(t *testing.T) {
	services, engines := ledgers(t, 2)
	follower, leader := services[0], services[1]

	// The first node's store is a follower's: it refuses and names who leads.
	engines[0].refusing(NotLeading{Leader: leader.peer})

	kvs, err := follower.Local().OpenKV(t.Context(), ledgerGrant())
	require.NoError(t, err)
	require.NoError(t, kvs.Put(t.Context(), "entry-1", []byte("one")))

	// AND IT LANDED ON THE LEADER, which is the assertion: a write accepted by
	// the node that was asked would be an entry nothing ordered.
	on := leader.Local()
	there, err := on.OpenKV(t.Context(), ledgerGrant())
	require.NoError(t, err)
	value, err := there.Get(t.Context(), "entry-1")
	require.NoError(t, err)
	require.Equal(t, []byte("one"), value)

	// The read takes the same route, so a caller reads what it just wrote
	// through the node it wrote through.
	back, err := kvs.Get(t.Context(), "entry-1")
	require.NoError(t, err)
	require.Equal(t, []byte("one"), back)

	keys, err := kvs.Keys(t.Context(), "entry")
	require.NoError(t, err)
	require.Equal(t, []string{"entry-1"}, keys)

	require.NoError(t, kvs.Delete(t.Context(), "entry-1"))
	_, err = kvs.Get(t.Context(), "entry-1")
	require.Error(t, err, "the delete was answered by a node that then had nothing to delete")
}

func TestACallAlreadyForwardedTwiceIsRefusedRatherThanSentOnAgain(t *testing.T) {
	services, engines := ledgers(t, 3)

	// Every node points at the next, and the last points back at the first.
	for i := range services {
		engines[i].refusing(NotLeading{Leader: services[(i+1)%len(services)].peer})
	}

	kvs, err := services[0].Local().OpenKV(t.Context(), ledgerGrant())
	require.NoError(t, err)

	err = kvs.Put(t.Context(), "entry-1", []byte("one"))
	require.Error(t, err, "a cycle of nodes each naming the next answered a write")
	require.ErrorContains(t, err, "already come",
		"the call was stopped by something other than the hop count")
}

func TestANodePlacementDidNotChooseNeverOpensTheGroup(t *testing.T) {
	services, engines := ledgers(t, 2)
	outsider, chosen := services[0], services[1]

	// A fleet of exactly one node, and it is not this one.
	outsider.members = func() []placement.Member {
		return []placement.Member{{ID: chosen.peer, Accepts: true}}
	}

	kvs, err := outsider.Local().OpenKV(t.Context(), ledgerGrant())
	require.NoError(t, err)
	require.NoError(t, kvs.Put(t.Context(), "entry-1", []byte("one")))

	opened, _ := engines[0].counts()
	require.Zero(t, opened, "the node placement did not choose became a voter by being asked")

	there, err := chosen.Local().OpenKV(t.Context(), ledgerGrant())
	require.NoError(t, err)
	value, err := there.Get(t.Context(), "entry-1")
	require.NoError(t, err)
	require.Equal(t, []byte("one"), value)
}

func TestANodeThatCanNameNobodyRefusesRatherThanOpening(t *testing.T) {
	services, engines := ledgers(t, 1)
	alone := services[0]

	// A fleet whose one member is a node that is not here and cannot be
	// reached, which is what a membership view mid-change looks like.
	alone.members = func() []placement.Member {
		return []placement.Member{{ID: "12D3KooWnobodyhomeQmNotAPeerAtAll", Accepts: true}}
	}

	kvs, err := alone.Local().OpenKV(t.Context(), ledgerGrant())
	require.NoError(t, err)

	err = kvs.Put(t.Context(), "entry-1", []byte("one"))
	require.ErrorIs(t, err, ErrNotHere)

	opened, _ := engines[0].counts()
	require.Zero(t, opened)
}

func TestAGroupThatHasNotFormedIsARetryAndNeverAMiss(t *testing.T) {
	services, engines := ledgers(t, 1)
	engines[0].refusing(ErrGroupForming)

	kvs, err := services[0].Local().OpenKV(t.Context(), ledgerGrant())
	require.NoError(t, err)

	_, err = kvs.Get(t.Context(), "entry-1")
	require.ErrorIs(t, err, ErrGroupForming)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(wire(err)),
		"a group that has not formed is not something to give up on")

	for _, err := range []error{
		ErrGroupForming,
		NotLeading{},
		NotLeading{Leader: "12D3KooWsomebody"},
		ErrNotHere,
	} {
		require.Equal(t, connect.CodeUnavailable, connect.CodeOf(wire(err)), "%v", err)
	}

	// AND NotLeading IS ErrNotLeading WHEREVER IT IS CAUGHT, so a caller that
	// asks by sentinel and one that reads the leader off it agree.
	require.ErrorIs(t, NotLeading{Leader: "x"}, ErrNotLeading)
	require.Contains(t, NotLeading{Leader: "x"}.Error(), "x")
	require.Contains(t, NotLeading{}.Error(), "no node leads it yet")
}

func TestARefusalSurvivesBeingForwardedBack(t *testing.T) {
	for _, err := range []error{
		ErrNoKey,
		ErrWrongModel,
		ErrModelUnserved,
		ErrConsensusUnserved,
		ErrNotDeclared,
		ErrNotSet,
		kvengine.ErrNotFound,
		ErrTooManyResident,
		ErrReleasing,
		ErrGroupForming,
		ErrNotHere,
		ErrUnreachable,
		ErrGroupClosed,
		errors.New("the disk is on fire"),
	} {
		sent := wire(err)
		back := refusal(sent)
		require.Equal(t, connect.CodeOf(sent), connect.CodeOf(wire(back)),
			"%v came back as a different instruction than it went out as", err)
		require.ErrorContains(t, back, err.Error(),
			"the answering node's own sentence did not travel with the code")
	}

	require.NoError(t, refusal(nil))
}

func TestAnUnnamedConsensusIsRefusedAtConstruction(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h, err := mn.GenPeer()
	require.NoError(t, err)

	registry := database.NewRegistry()
	require.NoError(t, registry.Register(t.Context(), &accounting{}))

	_, err = New(h,
		WithEngines(registry),
		WithRegister(&published{revision: 1, object: compiled()}),
		WithConsensus(database.CRDT, ""))
	require.ErrorContains(t, err, "unnamed consensus")
}

func TestANodeAnswersALedgerAsAVoterAndNotAsBytesOnDisk(t *testing.T) {
	services, engines := ledgers(t, 2)
	asked, elsewhere := services[0], services[1]

	at := ledgerAt()
	declaration, err := asked.declared(t.Context(), at)
	require.NoError(t, err)

	// With no fleet in view a node is the whole fleet, so it votes.
	holding := asked.voting(t.Context(), at, declaration)
	require.True(t, holding.GetHeld())
	require.True(t, holding.GetKeeps())

	opened, _ := engines[0].counts()
	require.Equal(t, 1, opened, "answering as a voter did not open the group")

	// And a node placement did not choose answers no without opening.
	elsewhere.members = func() []placement.Member {
		return []placement.Member{{ID: asked.peer, Accepts: true}}
	}

	holding = elsewhere.voting(t.Context(), at, declaration)
	require.False(t, holding.GetHeld())

	opened, _ = engines[1].counts()
	require.Zero(t, opened, "a node that should not vote opened the group anyway")

	again, _ := engines[0].counts()
	require.Equal(t, 1, again, "one voter opened its group twice")
}

func TestANodeWhoseGroupHasNoLeaderAsksTheOtherVoters(t *testing.T) {
	services, engines := ledgers(t, 2)
	asking, other := services[0], services[1]

	fleet := func() []placement.Member {
		return []placement.Member{
			{ID: asking.peer, Accepts: true},
			{ID: other.peer, Accepts: true},
		}
	}
	asking.members, other.members = fleet, fleet

	// This node's own group has not formed, which is what a node that has just
	// opened one looks like for as long as raft looks for peers.
	engines[0].refusing(ErrGroupForming)

	kvs, err := asking.Local().OpenKV(t.Context(), ledgerGrant())
	require.NoError(t, err)
	require.ErrorIs(t, kvs.Put(t.Context(), "entry-1", []byte("one")), ErrGroupForming,
		"a write into a group with no leader was accepted")

	require.Eventually(t, func() bool {
		opened, _ := engines[1].counts()
		return opened == 1
	}, 10*time.Second, 20*time.Millisecond,
		"the other voter was never asked, so its group would never form")
}

func TestOneRoundOfAskingAtATime(t *testing.T) {
	services, _ := ledgers(t, 1)
	s := services[0]
	at := ledgerAt()

	// Nothing to ask is not a round at all.
	s.warm(t.Context(), at, nil)
	require.Empty(t, s.warming)

	// A round already in flight is not started again: a group forms in tens of
	// seconds and every request in that window would otherwise start its own.
	s.warmMu.Lock()
	s.warming = map[string]bool{at.key(): true}
	s.warmMu.Unlock()

	s.warm(t.Context(), at, []string{"12D3KooWnobody"})

	s.warmMu.Lock()
	still := s.warming[at.key()]
	s.warmMu.Unlock()
	require.True(t, still, "a second round was started over the first")
}

func TestARouteToNowhereRefusesRatherThanAnswering(t *testing.T) {
	services, _ := ledgers(t, 1)
	nowhere := &routed{service: services[0], at: ledgerAt()}

	require.ErrorIs(t, nowhere.Put(t.Context(), "k", []byte("v")), ErrNotHere)
	require.ErrorIs(t, nowhere.Delete(t.Context(), "k"), ErrNotHere)

	_, err := nowhere.Get(t.Context(), "k")
	require.ErrorIs(t, err, ErrNotHere)
	_, err = nowhere.List(t.Context(), "")
	require.ErrorIs(t, err, ErrNotHere)
	_, err = nowhere.Watch(t.Context(), "")
	require.ErrorIs(t, err, ErrNotHere)

	// AND CLOSING IT RELEASES NOTHING: the resident table owns what it routes
	// to, and closing here would close a database another caller is holding.
	require.NoError(t, nowhere.Close())
}

func TestAVoterThatHasGoneDoesNotTakeTheLedgerWithIt(t *testing.T) {
	services, _ := ledgers(t, 2)
	outsider, chosen := services[0], services[1]

	gone, err := libp2ptest.RandPeerID()
	require.NoError(t, err)

	at := ledgerAt()
	through := &routed{
		service: outsider, at: at,
		others: []string{gone.String(), chosen.peer},
	}

	require.NoError(t, through.Put(t.Context(), "entry-1", []byte("one")))

	there, err := chosen.Local().OpenKV(t.Context(), ledgerGrant())
	require.NoError(t, err)
	value, err := there.Get(t.Context(), "entry-1")
	require.NoError(t, err)
	require.Equal(t, []byte("one"), value)

}

func TestAVoterThatAnswersEndsTheWalk(t *testing.T) {
	services, engines := ledgers(t, 3)
	outsider, refuses, would := services[0], services[1], services[2]

	// The first voter answers, and what it answers is a refusal.
	engines[1].refusing(NotLeading{})

	through := &routed{
		service: outsider, at: ledgerAt(),
		others: []string{refuses.peer, would.peer},
	}

	// The code and the answering node's sentence survive the round trip.
	err := through.Put(t.Context(), "entry-1", []byte("one"))
	require.ErrorContains(t, err, ErrNotLeading.Error())
	require.NotErrorIs(t, err, ErrNotHere,
		"a voter's own refusal was read as a node that could not be reached")

	// AND THE NEXT VOTER NEVER SAW THE CALL.
	there, err := would.Local().OpenKV(t.Context(), ledgerGrant())
	require.NoError(t, err)
	_, err = there.Get(t.Context(), "entry-1")
	require.Error(t, err, "the walk went past a refusal and wrote through the next voter")
}

func TestAHopCountThisNodeCannotReadIsTheCeiling(t *testing.T) {
	type testCase struct {
		header string
		want   int
	}
	for _, tc := range []testCase{
		{"", 0},
		{"1", 1},
		{"banana", maxHops},
		{"-3", maxHops},
	} {
		header := http.Header{}
		if tc.header != "" {
			header.Set(hopHeader, tc.header)
		}
		require.Equal(t, tc.want, hopsOf(hopsIn(t.Context(), header)), "%q", tc.header)
	}
}

func TestALedgerThatCannotBeOpenedIsNotHeld(t *testing.T) {
	services, engines := ledgers(t, 1)
	s := services[0]
	engines[0].refuse = errors.New("no disk")

	at := ledgerAt()
	declaration, err := s.declared(t.Context(), at)
	require.NoError(t, err)

	require.False(t, s.voting(t.Context(), at, declaration).GetHeld())

	_, err = s.route(t.Context(), at, declaration)
	require.ErrorContains(t, err, "no disk")
}

func TestAGroupThisNodeOpenedIsStillAnsweredWhenPlacementMovesOn(t *testing.T) {
	services, engines := ledgers(t, 2)
	leads, elsewhere := services[0], services[1]

	// Placement chose this node, so it opened the group and it leads it.
	kvs, err := leads.Local().OpenKV(t.Context(), ledgerGrant())
	require.NoError(t, err)
	require.NoError(t, kvs.Put(t.Context(), "entry-1", []byte("one")))

	opened, _ := engines[0].counts()
	require.Equal(t, 1, opened, "the node placement chose never opened the group")

	// AND THEN PLACEMENT NAMES SOMEBODY ELSE.
	leads.members = func() []placement.Member {
		return []placement.Member{{ID: elsewhere.peer, Accepts: true}}
	}

	again, err := leads.Local().OpenKV(t.Context(), ledgerGrant())
	require.NoError(t, err)

	value, err := again.Get(t.Context(), "entry-1")
	require.NoError(t, err, "the node leading the group refused the group it leads")
	require.Equal(t, []byte("one"), value)

	require.NoError(t, again.Put(t.Context(), "entry-2", []byte("two")))

	// AND IT SAYS SO WHEN ASKED, because a voter that told its peers it holds
	// nothing is a voter the rest of the fleet cannot count.
	declaration, err := leads.declared(t.Context(), ledgerAt())
	require.NoError(t, err)
	require.True(t, leads.voting(t.Context(), ledgerAt(), declaration).GetHeld())

	// AND NOTHING WAS SENT ANYWHERE: the other node is not in this group.
	there, err := elsewhere.Local().OpenKV(t.Context(), ledgerGrant())
	require.NoError(t, err)
	_, err = there.Get(t.Context(), "entry-2")
	require.Error(t, err, "a node that leads a group forwarded a call it could answer")
}

func TestTheSweepKeepsAskingTheOtherVotersAboutALedger(t *testing.T) {
	services, engines := ledgers(t, 2)
	opened, other := services[0], services[1]

	fleet := func() []placement.Member {
		return []placement.Member{
			{ID: opened.peer, Accepts: true},
			{ID: other.peer, Accepts: true},
		}
	}
	opened.members, other.members = fleet, fleet

	// THE REQUEST PATH SUCCEEDS, which is the condition under test: a group
	// with a leader never asks anybody anything.
	kvs, err := opened.Local().OpenKV(t.Context(), ledgerGrant())
	require.NoError(t, err)
	require.NoError(t, kvs.Put(t.Context(), "entry-1", []byte("one")))

	asked, _ := engines[1].counts()
	require.Zero(t, asked, "the other voter was asked by the request path, so the sweep proves nothing")

	opened.reconsiderLedgers(t.Context())

	require.Eventually(t, func() bool {
		asked, _ := engines[1].counts()
		return asked == 1
	}, 10*time.Second, 20*time.Millisecond,
		"nothing ever re-checked, so this ledger is a cluster of one for the life of the process")
}

func TestAnAnswerAndASilenceAreNotTheSameUnavailable(t *testing.T) {
	// Anything this service wrote carries the mark and keeps its own sentinel.
	require.ErrorIs(t, refusal(wire(ErrNotLeading)), ErrNotLeading)
	require.NotErrorIs(t, refusal(wire(ErrNotLeading)), ErrUnreachable)

	// Anything else is a silence, and a silence is walked past.
	for _, err := range []error{
		connect.NewError(connect.CodeUnavailable, errors.New("stream reset")),
		connect.NewError(connect.CodeUnknown, errors.New("protocols not supported")),
		errors.New("dial backoff"),
	} {
		back := refusal(err)
		require.ErrorIs(t, back, ErrUnreachable, "%v", err)
		require.ErrorIs(t, back, ErrNotHere,
			"a node that did not answer stopped a walk that should have gone on")
	}
}

func TestANodeThatHasClosedItsGroupsSendsTheCallOn(t *testing.T) {
	services, engines := ledgers(t, 2)
	leaving, other := services[0], services[1]

	fleet := func() []placement.Member {
		return []placement.Member{
			{ID: leaving.peer, Accepts: true},
			{ID: other.peer, Accepts: true},
		}
	}
	leaving.members, other.members = fleet, fleet

	engines[0].refusing(ErrGroupClosed)

	kvs, err := leaving.Local().OpenKV(t.Context(), ledgerGrant())
	require.NoError(t, err)
	require.NoError(t, kvs.Put(t.Context(), "entry-1", []byte("one")),
		"a node whose group is gone refused a call another voter could answer")

	there, err := other.Local().OpenKV(t.Context(), ledgerGrant())
	require.NoError(t, err)
	value, err := there.Get(t.Context(), "entry-1")
	require.NoError(t, err)
	require.Equal(t, []byte("one"), value)
}

func TestTheSweepDoesNotLeaveALedgerItCannotResolve(t *testing.T) {
	services, _ := ledgers(t, 1)
	s := services[0]

	s.opening(address{project: "nobody", environment: "prod", id: "QmNeverDeclared"})
	s.reconsiderLedgers(t.Context())
	require.Len(t, s.ledgers(), 1, "a ledger nobody could resolve was forgotten")

	// AND A SWEEP THAT HAS BEEN STOPPED STOPS.
	stopped, done := context.WithCancel(t.Context())
	done()
	s.reconsiderLedgers(stopped)
}
