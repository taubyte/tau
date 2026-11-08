package raft

import (
	"encoding/binary"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	etcdraft "go.etcd.io/raft/v3"
	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	raftv2 "github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2"
)

const leaseGroup = "a"

// newLeaseSim is one group of three whose tick is ten rounds, so a message
// crosses the network in a fraction of a tick, as it does between real nodes.
func newLeaseSim(t *testing.T, seed uint64) *sim {
	return newSimTimed(t, seed, 3, map[string][]int{leaseGroup: {0, 1, 2}}, 500*time.Millisecond)
}

func status(n *simNode) etcdraft.BasicStatus { return n.groups[leaseGroup].g.rn.BasicStatus() }

// until runs rounds until done, at most rounds of them, and says whether it was.
func (s *sim) until(rounds int, done func() bool) bool {
	for range rounds {
		if done() {
			return true
		}
		s.step()
		s.answered()
	}
	return done()
}

// rounds runs n rounds.
func (s *sim) rounds(n int) { s.until(n, func() bool { return false }) }

// leaderBut is a member other than not that raft says leads, if one up does.
func (s *sim) leaderBut(not *simNode) *simNode {
	for _, n := range s.nodes {
		if n != not && n.up && status(n).RaftState == etcdraft.StateLeader {
			return n
		}
	}
	return nil
}

func (s *sim) others(of *simNode) []*simNode {
	var o []*simNode
	for _, n := range s.nodes {
		if n != of {
			o = append(o, n)
		}
	}
	return o
}

// read asks n for a read as Barrier does, and returns where its index comes.
func (s *sim) read(n *simNode) chan uint64 {
	g := n.groups[leaseGroup].g
	id := randomID()
	ch := make(chan uint64, 1)
	g.mu.Lock()
	g.reads[id] = ch
	g.mu.Unlock()
	readAsk{g: g, ctx: binary.BigEndian.AppendUint64(nil, id)}.run(n.e)
	return ch
}

func answered(ch chan uint64) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// alone says whether n answers a read at once, without asking anyone: only a
// lease answers before another round.
func (s *sim) alone(n *simNode) bool { return answered(s.read(n)) }

// leasedLeader is the group's leader, holding a lease earned by a read a quorum
// confirmed, with no other read in flight. A read can take longer to confirm than
// the lease it earns lasts, so reads are asked one at a time until one does.
func (s *sim) leasedLeader() *simNode {
	for range 20 {
		var l *simNode
		require.True(s.t, s.until(2000, func() bool { l = s.leaderBut(nil); return l != nil }))
		ch := s.read(l)
		if answered(ch) {
			return l
		}
		require.True(s.t, s.until(2000, func() bool { return answered(ch) }))
		if s.alone(l) {
			return l
		}
	}
	s.t.Fatalf("no leader earned a lease")
	return nil
}

// lapse runs rounds until n's lease has lapsed, renewals included.
func (s *sim) lapse(n *simNode) {
	g := n.groups[leaseGroup].g
	require.True(s.t, s.until(2000, func() bool { return !g.leased(status(n), s.clock.now) }))
}

func tickRounds(s *sim) int {
	return int(s.nodes[0].groups[leaseGroup].g.tickEvery / engineResolution)
}

func leaseRounds(s *sim) int {
	return int(s.nodes[0].groups[leaseGroup].g.leaseFor() / engineResolution)
}

// noStaleReads runs rounds with old cut off from the rest, writing through whichever
// other member leads, and fails if old answers a read asked after that write was
// acknowledged. It says whether another member wrote.
func (s *sim) noStaleReads(old *simNode, rounds int) bool {
	var asked []chan uint64
	key, wrote := "", false
	check := func() {
		for _, ch := range asked {
			if answered(ch) {
				s.t.Fatalf("the old leader answered a read asked after another leader's write was acknowledged")
			}
		}
	}
	for range rounds {
		if n := s.leaderBut(old); n != nil && key == "" && s.proposeOn(n, leaseGroup) {
			key = fmt.Sprintf("k%d", s.n)
		}
		s.step()
		s.answered()
		if key != "" && s.acked[leaseGroup][key] {
			wrote = true
		}
		check()
		if wrote {
			asked = append(asked, s.read(old))
		}
	}
	check()
	return wrote
}

// A LEADER ANSWERS A READ ALONE WHILE ITS LEASE HOLDS, asks a quorum once it has
// lapsed, and holds a lease again once the quorum answers.
func TestALeaderAnswersReadsAloneWhileItsLeaseHolds(t *testing.T) {
	s := newLeaseSim(t, 1)
	l := s.leasedLeader()
	require.True(t, s.alone(l))

	s.lapse(l)
	ch := s.read(l)
	require.False(t, answered(ch), "a lapsed lease answered a read")
	require.True(t, s.until(2000, func() bool { return answered(ch) }))
	require.True(t, s.alone(l))
}

// A LEADER CUT OFF ANSWERS NO READ ONCE ANOTHER LEADER HAS WRITTEN: its lease ends
// before the members that confirmed it may elect anyone, which etcd's own lease,
// held until the leader steps down, does not.
func TestACutOffLeaderAnswersNoReadOnceAnotherHasWritten(t *testing.T) {
	for seed := range uint64(20) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			s := newLeaseSim(t, seed)
			l := s.leasedLeader()
			s.net.side[l.id] = 1
			require.True(t, s.alone(l))
			require.True(t, s.noStaleReads(l, 2000), "no other member came to lead and write")
		})
	}
}

// A MEMBER WHOSE LOOP STALLED VOTES ON NO TICK IT MISSED: they are dropped, not
// taken at once, so having heard its leader just before it went on still holds it
// to a whole election timeout of refusing votes, which the leader's lease rests on.
func TestAMemberWhoseLoopStalledVotesOnNoTickItMissed(t *testing.T) {
	for seed := range uint64(5) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			s := newLeaseSim(t, seed)
			l := s.leasedLeader()
			o := s.others(l)
			v, c := o[0], o[1]
			v.still = true
			for range 3 * electionTicks * tickRounds(s) {
				s.read(l)
				s.step()
			}
			require.True(t, s.alone(l), "the leader's lease lapsed while a member stalled")
			v.still = false
			s.net.side[l.id] = 1
			require.NoError(t, c.groups[leaseGroup].g.rn.Campaign())
			require.True(t, s.noStaleReads(l, 2000), "no other member came to lead and write")
		})
	}
}

// A LEADER THAT STARTS A TRANSFER ANSWERS NO READ ALONE FOR THE REST OF ITS TERM:
// the transfer's target campaigns past every member's refusal whenever its order
// arrives, even after the leader gave the transfer up.
func TestALeaderThatStartsATransferAnswersNoReadAloneForTheRestOfItsTerm(t *testing.T) {
	starts := map[string]func(l, target, f *simNode){
		"asked of the leader": func(l, target, _ *simNode) {
			transferAsk{g: l.groups[leaseGroup].g, to: raftID(target.id)}.run(l.e)
		},
		"forwarded by a follower": func(_, target, f *simNode) {
			transferAsk{g: f.groups[leaseGroup].g, to: raftID(target.id)}.run(f.e)
		},
	}
	for name, start := range starts {
		t.Run(name, func(t *testing.T) {
			s := newLeaseSim(t, 3)
			l := s.leasedLeader()
			o := s.others(l)
			target, f := o[0], o[1]
			term := status(l).GetTerm()
			s.net.side[target.id] = 2
			start(l, target, f)
			require.True(t, s.until(2000, func() bool { return status(l).LeadTransferee != 0 }))
			require.False(t, s.alone(l), "a leader transferring answered a read alone")

			require.True(t, s.until(2000, func() bool { return status(l).LeadTransferee == 0 }))
			clear(s.net.side)
			ch := s.read(l)
			require.True(t, s.until(2000, func() bool { return answered(ch) }))
			require.Equal(t, etcdraft.StateLeader, status(l).RaftState)
			require.Equal(t, term, status(l).GetTerm())
			require.False(t, s.alone(l), "a leader answered a read alone in the term it started a transfer")
		})
	}
}

// A CHANGE OF MEMBERS ENDS THE LEASE: the members who confirmed it may no longer
// be a quorum of the group.
func TestAChangeOfMembersEndsTheLease(t *testing.T) {
	s := newLeaseSim(t, 4)
	l := s.leasedLeader()
	g := l.groups[leaseGroup].g
	gone := s.others(l)[1]
	// A fresh lease, so the change commits well inside it.
	require.True(t, s.until(2000, func() bool {
		s.read(l)
		return g.lease.term == status(l).GetTerm() && since(g.lease.from, s.clock.now) <= 3*engineResolution
	}))
	a := confAsk{g: g, cc: &pb.ConfChange{
		Type:    pb.ConfChangeType_ConfChangeRemoveNode.Enum(),
		NodeId:  proto.Uint64(raftID(gone.id)),
		Context: []byte(gone.id),
		Id:      proto.Uint64(1),
	}, reply: make(chan error, 1)}
	a.run(l.e)
	require.NoError(t, <-a.reply)

	// Reads keep the lease alive while the change commits, as a busy leader's do.
	var held readLease
	require.True(t, s.until(2000, func() bool {
		if len(g.conf.GetVoters()) == 2 {
			return true
		}
		s.read(l)
		held = g.lease
		return false
	}))
	require.Equal(t, status(l).GetTerm(), held.term)
	require.Less(t, since(held.from, s.clock.now), g.leaseFor(), "the lease had lapsed before the change")
	require.False(t, s.alone(l), "a lease outlived a change of members")
}

// A READ CONFIRMED LATE EARNS NO LONGER LEASE: the lease runs from when the read
// was asked, since the members that confirm it may have heard the leader long
// before their answers arrive.
func TestAReadConfirmedLateEarnsNoLongerLease(t *testing.T) {
	s := newLeaseSim(t, 5)
	l := s.leasedLeader()
	term := status(l).GetTerm()
	s.lapse(l)
	s.net.side[l.id] = 1
	ch := s.read(l)
	require.False(t, answered(ch))
	s.rounds(leaseRounds(s) + 1)
	clear(s.net.side)
	require.True(t, s.until(2000, func() bool { return answered(ch) }))
	require.Equal(t, etcdraft.StateLeader, status(l).RaftState)
	require.Equal(t, term, status(l).GetTerm())
	require.False(t, s.alone(l), "a read confirmed after its lease would have lapsed earned one")
}

// A MEMBER THAT OPENS AGAIN VOTES FOR NOBODY FOR AN ELECTION TIMEOUT: it forgot
// which leader it heard, and a lease that leader holds may rest on it.
func TestAMemberThatOpensAgainVotesForNobodyForAnElectionTimeout(t *testing.T) {
	s := newLeaseSim(t, 6)
	l := s.leasedLeader()
	o := s.others(l)
	c, r := o[0], o[1]
	s.crash(r, false)
	s.start(r)
	quiet := r.groups[leaseGroup].g.quietUntil
	require.True(t, quiet.After(s.clock.now))
	s.net.side[l.id] = 1
	require.NoError(t, c.groups[leaseGroup].g.rn.Campaign())
	for s.clock.now.Before(quiet) {
		s.step()
		require.NotEqual(t, etcdraft.StateLeader, status(c).RaftState, "a member that opened again voted at once")
	}
	require.True(t, s.until(2000, func() bool { return s.leaderBut(l) != nil }))
}

// A MEMBER IGNORES AN ORDER TO FORGET ITS LEADER FROM THE WIRE: raft never sends
// one, and a member that forgot its leader votes at once.
func TestAMemberIgnoresAnOrderToForgetItsLeaderFromTheWire(t *testing.T) {
	s := newLeaseSim(t, 7)
	l := s.leasedLeader()
	f := s.others(l)[0]
	raw, err := proto.Marshal(&pb.Message{
		Type: pb.MessageType_MsgForgetLeader.Enum(),
		To:   proto.Uint64(raftID(f.id)),
		From: proto.Uint64(raftID(l.id)),
		Term: proto.Uint64(status(f).GetTerm()),
	})
	require.NoError(t, err)
	f.e.received(l.id, &raftv2.Frame{Envelopes: []*raftv2.Envelope{{
		Group: leaseGroup, Message: raw, Formation: f.groups[leaseGroup].g.Formation(),
	}}})
	f.e.drain()
	require.Equal(t, raftID(l.id), status(f).Lead)
}
