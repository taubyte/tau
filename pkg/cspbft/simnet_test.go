package cspbft

import (
	"fmt"
	"testing"
)

// simnet_test.go is the whole-protocol assertion: across every configuration,
// every seed and every adversary below, NO TWO REPLICAS EVER EXECUTE DIFFERENT
// PROPOSALS AT ONE SEQUENCE NUMBER. That is S1, and the harness checks it after
// every single execution rather than at the end, so a failing seed names the
// step it broke on.

// equivocatingPrimary assigns two different real requests to one sequence
// number, splitting the cluster: half the replicas are told sequence n holds
// one digest, half are told it holds another. Both halves hold a genuine
// request, so both halves can and will vote — which is the point.
func equivocatingPrimary(alt func() (Digest, bool)) mutator {
	return func(s *sim, from, to ReplicaID, m Message) (Message, bool) {
		if m.Kind != KindPrePrepare || to >= s.ids[len(s.ids)/2] {
			return m, true
		}
		d, ok := alt()
		if !ok || d == m.Digest {
			return m, true
		}
		m.Digest = d
		return resign(from, m), true
	}
}

// silent drops everything a replica would send. It is the ordinary crash, and
// it is the case the view change exists for.
func silent() mutator {
	return func(*sim, ReplicaID, ReplicaID, Message) (Message, bool) { return Message{}, false }
}

// replayer sends everything twice, which is what a network with a retrying
// middlebox looks like and what S8's dedup has to absorb.
func replayer() mutator {
	return func(s *sim, from, to ReplicaID, m Message) (Message, bool) {
		s.queue = append(s.queue, envelope{from: from, to: to, msg: m})
		return m, true
	}
}

// flooder adds votes outside the water marks, which is the attack the window
// exists to stop: without it a faulty replica exhausts the sequence space.
func flooder() mutator {
	return func(s *sim, from, to ReplicaID, m Message) (Message, bool) {
		if m.Kind == KindPrepare {
			for _, seq := range []Sequence{1 << 40, 1<<40 + 1} {
				junk := resign(from, Message{Kind: KindPrepare, From: from, View: m.View, Seq: seq, Digest: m.Digest})
				s.queue = append(s.queue, envelope{from: from, to: to, msg: junk})
			}
		}
		return m, true
	}
}

// forger signs as somebody else, which must never reach a log.
func forger(as ReplicaID) mutator {
	return func(s *sim, from, to ReplicaID, m Message) (Message, bool) {
		m.From = as
		m.Signature = sigFor("replica", string(from), Preimage(m)) // the wrong key
		return m, true
	}
}

func doubleVoter() mutator {
	return func(s *sim, from, to ReplicaID, m Message) (Message, bool) {
		if m.Kind == KindPrepare || m.Kind == KindCommit {
			alt := m
			alt.Digest = Digest{0xde, 0xad}
			s.queue = append(s.queue, envelope{from: from, to: to, msg: resign(from, alt)})
		}
		return m, true
	}
}

// liar votes for assignments nobody proposed.
func liar() mutator {
	return func(s *sim, from, to ReplicaID, m Message) (Message, bool) {
		if m.Kind == KindPrepare || m.Kind == KindCommit {
			m.Digest = Digest{0xde, 0xad}
			return resign(from, m), true
		}
		return m, true
	}
}

func TestNoTwoReplicasEverExecuteDifferentProposalsAtOneSequence(t *testing.T) {
	adversaries := map[string]func(*sim) mutator{
		"none": nil,
		"an equivocating primary": func(s *sim) mutator {
			i := 0
			return equivocatingPrimary(func() (Digest, bool) {
				if len(s.digests) == 0 {
					return Digest{}, false
				}
				i++
				return s.digests[i%len(s.digests)], true
			})
		},
		"a silent replica":               func(*sim) mutator { return silent() },
		"a replayer":                     func(*sim) mutator { return replayer() },
		"a flooder":                      func(*sim) mutator { return flooder() },
		"a forger":                       func(s *sim) mutator { return forger(s.ids[1]) },
		"a liar":                         func(*sim) mutator { return liar() },
		"a replica contradicting itself": func(*sim) mutator { return doubleVoter() },
	}
	for _, n := range []int{4, 7, 10} {
		for name, make := range adversaries {
			for seed := int64(1); seed <= 5; seed++ {
				t.Run(fmt.Sprintf("n=%d/%s/seed=%d", n, name, seed), func(t *testing.T) {
					s := newSim(t, n, seed, withSimInterval(4))
					if make != nil {
						// The faulty replica is the primary of view 0, which
						// is the position an attacker actually wants.
						s.mutate[s.ids[0]] = make(s)
					}
					for i := uint64(1); i <= 10; i++ {
						s.request("c1", i, fmt.Sprintf("p%d", i))
						s.settle(25)
					}
					s.checkSafety()
					for _, sl := range s.slices {
						checkPersistOrder(t, sl.who, sl.acts)
					}
				})
			}
		}
	}
}

func TestTheClusterMakesProgressOnceTheFaultyPrimaryIsReplaced(t *testing.T) {
	for _, n := range []int{4, 7, 10} {
		for seed := int64(1); seed <= 3; seed++ {
			t.Run(fmt.Sprintf("n=%d/seed=%d", n, seed), func(t *testing.T) {
				s := newSim(t, n, seed, withSimInterval(4))
				s.mutate[s.ids[0]] = silent()
				for i := uint64(1); i <= 6; i++ {
					s.request("c1", i, fmt.Sprintf("p%d", i))
					s.settle(30)
				}
				for _, id := range s.ids[1:] {
					if s.lastExec[id] < 6 {
						t.Fatalf("%s reached only sequence %d with one faulty primary out of %d", id, s.lastExec[id], n)
					}
				}
			})
		}
	}
}

func TestAPartitionedReplicaRejoinsAndCatchesUp(t *testing.T) {
	for seed := int64(1); seed <= 5; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			s := newSim(t, 7, seed, withSimInterval(4))
			s.part[s.ids[6]] = true
			for i := uint64(1); i <= 8; i++ {
				s.request("c1", i, fmt.Sprintf("p%d", i))
				s.settle(20)
			}
			s.part[s.ids[6]] = false
			for i := uint64(9); i <= 14; i++ {
				s.request("c1", i, fmt.Sprintf("p%d", i))
				s.settle(30)
			}
			if s.lastExec[s.ids[6]] < 12 {
				t.Fatalf("the rejoining replica is at %d, want it caught up", s.lastExec[s.ids[6]])
			}
			s.checkSafety()
		})
	}
}

func TestAReplicaThatCrashesAndRestoresItsJournalDoesNotContradictItself(t *testing.T) {
	for seed := int64(1); seed <= 5; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			s := newSim(t, 7, seed, withSimInterval(4))
			victim := s.ids[3]
			for i := uint64(1); i <= 5; i++ {
				s.request("c1", i, fmt.Sprintf("p%d", i))
				s.settle(20)
				if i == 3 {
					s.restart(t, victim)
				}
			}
			s.settle(30)
			s.checkSafety()
			if s.lastExec[victim] < 5 {
				t.Fatalf("the restarted replica is at %d", s.lastExec[victim])
			}
		})
	}
}

type favourScorer struct{ first ReplicaID }

func (f favourScorer) Rank(s Snapshot) []ReplicaID {
	out := []ReplicaID{f.first}
	for _, id := range s.Replicas {
		if id != f.first {
			out = append(out, id)
		}
	}
	return out
}

func TestAReplicaThatTopsTheRankingIsStillPrimaryOnlyOneViewInN(t *testing.T) {
	r := mkReplica(t, 7, "a", WithScorer(favourScorer{first: "g"}))
	if err := r.rot.recompute(4, Digest{1}); err != nil {
		t.Fatal(err)
	}
	count := map[ReplicaID]int{}
	for v := View(0); v < 70; v++ {
		count[r.Primary(v)]++
	}
	for _, id := range ids(7) {
		if count[id] != 10 {
			t.Fatalf("%q led %d of 70 views.\n\nA scorer must reorder and never replace: a replica that behaves "+
				"perfectly for a thousand views to top a ranking and then stalls would hold primacy forever, "+
				"because the score that would demote it is computed from a log that has stopped advancing.", id, count[id])
		}
	}
}

func TestAHighlyRankedReplicaThatStallsCostsTheClusterOneViewAndNotItsLiveness(t *testing.T) {
	s := newSim(t, 7, 41, withSimInterval(4), withSimScorer(favourScorer{first: "r06"}))
	s.mutate["r06"] = silent()
	for i := uint64(1); i <= 8; i++ {
		s.request("c1", i, fmt.Sprintf("p%d", i))
		s.settle(30)
	}
	for _, id := range s.ids[:6] {
		if s.lastExec[id] < 8 {
			t.Fatalf("%s reached only %d: a scored rotation must never let one replica hold the view", id, s.lastExec[id])
		}
	}
	s.checkSafety()
}

func TestAReplicaThatContradictsItsOwnVotesIsCaughtWithProofAndTheOrderHolds(t *testing.T) {
	for _, n := range []int{4, 7} {
		for seed := int64(1); seed <= 3; seed++ {
			t.Run(fmt.Sprintf("n=%d/seed=%d", n, seed), func(t *testing.T) {
				s := newSim(t, n, seed, withSimInterval(4))
				offender := s.ids[1] // a backup, so the cluster still progresses
				s.mutate[offender] = doubleVoter()
				for i := uint64(1); i <= 8; i++ {
					s.request("c1", i, fmt.Sprintf("p%d", i))
					s.settle(30)
				}
				s.checkSafety()
				if len(s.evidence) == 0 {
					t.Fatal("a replica sent two contradicting votes for one assignment and no proof was produced")
				}
				for _, e := range s.evidence {
					if e.Offender() != offender {
						t.Fatalf("a proof incriminates %q, which never contradicted itself: \"replica j "+
							"equivocated\" is precisely the claim an adversary would most like to be believed "+
							"about a correct replica", e.Offender())
					}
					if err := e.Verify(replicaVerifier()); err != nil {
						t.Fatalf("a proof does not hold up for a third party who was not there: %v", err)
					}
				}
			})
		}
	}
}

func TestThePrimaryIsTheReplicaWhoseRestartMustNotLoseWhatItAssigned(t *testing.T) {
	// Restarting a backup exercises the vote it must not contradict.
	// Restarting the PRIMARY exercises the assignment it must not repeat, and
	// that is the one a correct replica can break safety with.
	for seed := int64(1); seed <= 5; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			s := newSim(t, 7, seed, withSimInterval(4))
			for i := uint64(1); i <= 8; i++ {
				s.request("c1", i, fmt.Sprintf("p%d", i))
				s.settle(20)
				if i == 3 || i == 5 {
					s.restart(t, s.primary())
				}
			}
			s.settle(40)
			s.checkSafety()
			for _, e := range s.evidence {
				t.Fatalf("a proof of equivocation against %q, and every replica here is correct.\n\n"+
					"A replica that honoured the durability contract to the letter produced the one artefact "+
					"the whole quorum argument assumes cannot exist.", e.Offender())
			}
			for _, id := range s.ids {
				if s.lastExec[id] < 6 {
					t.Fatalf("%s reached only sequence %d after two primary restarts", id, s.lastExec[id])
				}
			}
		})
	}
}

func TestAReplicaThatChangedViewWhileAloneTakesPartAgainAtTheNextViewChange(t *testing.T) {
	for seed := int64(1); seed <= 5; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			s := newSim(t, 7, seed, withSimInterval(4))
			alone := s.ids[6]
			s.part[alone] = true
			for i := uint64(1); i <= 8; i++ {
				s.request("c1", i, fmt.Sprintf("p%d", i))
				s.settle(20)
			}
			s.part[alone] = false
			for i := uint64(9); i <= 12; i++ {
				s.request("c1", i, fmt.Sprintf("p%d", i))
				s.settle(20)
			}

			// The primary now fails, which is the ordinary reason a cluster
			// changes view.
			s.down[s.primary()] = true
			resumed := len(s.slices)
			for i := uint64(13); i <= 18; i++ {
				s.request("c1", i, fmt.Sprintf("p%d", i))
				s.settle(40)
			}

			votes := 0
			for _, sl := range s.slices[resumed:] {
				if sl.who != alone {
					continue
				}
				for _, a := range sl.acts {
					if b, ok := a.(Broadcast); ok && (b.Message.Kind == KindPrepare || b.Message.Kind == KindCommit) {
						votes++
					}
				}
			}
			if votes == 0 {
				t.Fatalf("the replica is in view %d and cast no vote across a view change that had its own "+
					"VIEW-CHANGE in hand.\n\n"+
					"A replica that is counted in n and can never be counted in a quorum has eaten the fault "+
					"budget with nothing looking wrong: at n=4 one of them leaves exactly q, and two kill the "+
					"cluster without any replica ever having been byzantine.", s.rep[alone].CurrentView())
			}
			s.checkSafety()
		})
	}
}
