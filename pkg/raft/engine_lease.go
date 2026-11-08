package raft

import (
	"encoding/binary"
	"time"

	etcdraft "go.etcd.io/raft/v3"
)

// A LEADER ANSWERS A READ ALONE WHILE ITS LEASE HOLDS, and asks a quorum otherwise.
//
// A read confirmed by a quorum was confirmed by voters who each heard the leader
// after the read was asked. Each refuses every vote for electionTicks of its own
// ticks after hearing it, and ticks never catch up, so no other leader can be
// elected for (electionTicks-1)*tickEvery after the read was asked. The lease is
// one tick shorter, the margin for clocks that run at different rates.
//
// etcd's own lease is not used: its leader keeps leading on contact from a whole
// election timeout earlier, which outlives the voters' refusal. Whatever lets a
// voter vote sooner ends the lease: a transfer, which campaigns past the refusal,
// ends it for the rest of the term; a change of membership changes the quorum; and
// a member that opens again has forgotten whom it heard, so it votes for nobody
// for an election timeout.

// readLease is a leader's right to answer reads alone, from the time the read that
// earned it was asked.
type readLease struct {
	term uint64
	from time.Time
}

// leaseRound is a read asked of a quorum, which earns a lease once confirmed.
type leaseRound struct {
	term, epoch uint64
	at          time.Time
}

func (g *group) leaseFor() time.Duration { return (electionTicks - 2) * g.tickEvery }

// since is how long ago at was, on whichever of the monotonic and the wall clock
// says longer: one stops while a machine sleeps, and the other can be set back.
func since(at, now time.Time) time.Duration {
	return max(now.Sub(at), now.Round(0).Sub(at.Round(0)))
}

// leased says whether a leader in st may answer a read alone at now.
func (g *group) leased(st etcdraft.BasicStatus, now time.Time) bool {
	return st.RaftState == etcdraft.StateLeader &&
		st.LeadTransferee == 0 &&
		g.unleased != st.GetTerm() &&
		g.lease.term == st.GetTerm() &&
		since(g.lease.from, now) < g.leaseFor()
}

// prune forgets rounds of another term, and rounds too old to earn a lease.
func (g *group) prune(term uint64, now time.Time) {
	for id, r := range g.rounds {
		if r.term != term || since(r.at, now) >= g.leaseFor() {
			delete(g.rounds, id)
		}
	}
}

// startRound records when a leader asked a quorum to confirm read id; a read asked
// again keeps when it was first asked.
func (g *group) startRound(id uint64, term uint64, now time.Time) {
	if _, ok := g.rounds[id]; !ok {
		g.rounds[id] = leaseRound{term: term, epoch: g.confEpoch, at: now}
	}
}

// confirmed takes a lease from read id, confirmed by a quorum, if the leader that
// asked it still leads the same members in the same term.
func (g *group) confirmed(id uint64) {
	r, ok := g.rounds[id]
	if !ok {
		return
	}
	delete(g.rounds, id)
	st := g.rn.BasicStatus()
	if st.RaftState != etcdraft.StateLeader || st.GetTerm() != r.term || r.epoch != g.confEpoch || g.unleased == st.GetTerm() {
		return
	}
	if g.lease.term != r.term || r.at.After(g.lease.from) {
		g.lease = readLease{term: r.term, from: r.at}
	}
}

// unlease ends the lease for the rest of the term: a transfer has started, and its
// target may campaign whenever its order arrives.
func (g *group) unlease() {
	g.unleased = g.rn.BasicStatus().GetTerm()
	g.lease = readLease{}
}

// membershipChanged ends the lease and every round asked of the old members.
func (g *group) membershipChanged() {
	g.confEpoch++
	g.lease = readLease{}
}

// answerRead hands read id its index, if it is still waited for.
func (g *group) answerRead(id uint64, index uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if ch, ok := g.reads[id]; ok {
		select {
		case ch <- index:
		default:
		}
	}
}

type readAsk struct {
	g   *group
	ctx []byte
}

// run answers the read from the lease if it holds, and asks a quorum otherwise. A
// lease half spent is renewed by a round of its own while reads go on being
// answered, so a busy leader never waits on a quorum.
func (a readAsk) run(e *engine) {
	g := a.g
	st := g.rn.BasicStatus()
	if st.RaftState != etcdraft.StateLeader {
		g.rn.ReadIndex(a.ctx)
		return
	}
	id, term, now := binary.BigEndian.Uint64(a.ctx), st.GetTerm(), e.clock.Now()
	g.prune(term, now)
	if !g.leased(st, now) {
		g.startRound(id, term, now)
		g.rn.ReadIndex(a.ctx)
		return
	}
	g.answerRead(id, st.GetCommit())
	if len(g.rounds) == 0 && since(g.lease.from, now) >= g.leaseFor()/2 {
		renew := randomID()
		g.startRound(renew, term, now)
		g.rn.ReadIndex(binary.BigEndian.AppendUint64(nil, renew))
	}
}
