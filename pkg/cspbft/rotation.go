package cspbft

import (
	"encoding/binary"
	"sort"
)

// The only file holding a Scorer; certificate.go and log.go have no path to one
// (TestTheCertificateFileNamesNoScorer).

// Scorer ranks replicas for leader selection: a liveness heuristic deciding whose
// turn comes first, never whose vote counts. See the package doc's contract. It must
// return a permutation of all Snapshot.Replicas, read only application state as of
// Snapshot.StateDigest, and use integer arithmetic with ReplicaID tie-breaks.
type Scorer interface {
	Rank(Snapshot) []ReplicaID
}

// Snapshot is everything a Scorer may see, and the absence of everything else
// is the design. TestASnapshotCarriesNothingAReplicaCouldLieItselfInWith is the
// census.
//
// A self-reported latency scored into primary selection would be
// pkg/stats' Report.Region at the worst possible stakes: the primary is the
// one replica that can cheaply censor, reorder and stall, so it is the
// position an attacker actually wants.
type Snapshot struct {
	// Epoch is the stable checkpoint this rotation is bound to.
	Epoch Sequence

	// StateDigest is the application state certified by a quorum of
	// CHECKPOINTs at Epoch. It is the ONLY agreed datum a Scorer gets, and
	// therefore the only safe key into whatever the caller wants to score on.
	StateDigest Digest

	// Replicas is the configuration, sorted. A returned permutation is checked
	// against exactly this set.
	Replicas []ReplicaID
}

// RoundRobin is the identity permutation, the textbook rule, the default and every
// fallback.
type RoundRobin struct{}

func (RoundRobin) Rank(s Snapshot) []ReplicaID { return s.Replicas }

type rotation struct {
	scorer   Scorer
	hash     Hasher
	replicas []ReplicaID

	epoch  Sequence
	state  Digest
	perm   []ReplicaID
	digest Digest
}

func newRotation(scorer Scorer, hash Hasher, replicas []ReplicaID) *rotation {
	rot := &rotation{scorer: scorer, hash: hash, replicas: replicas}
	rot.perm = append([]ReplicaID(nil), replicas...)
	rot.digest = rot.digestOf(rot.perm)
	return rot
}

// recompute installs the permutation for a new epoch, and returns a non-nil
// error when the Scorer's answer had to be discarded. It is called ONLY at a
// stable checkpoint: between checkpoints the rotation is frozen, which is what
// makes two correct replicas in one view agree about who the primary is.
func (rot *rotation) recompute(epoch Sequence, state Digest) error {
	ranked, err := rot.rank(Snapshot{Epoch: epoch, StateDigest: state, Replicas: rot.replicas})
	rot.epoch = epoch
	rot.state = state
	if err != nil {
		rot.perm = append([]ReplicaID(nil), rot.replicas...)
		rot.digest = rot.digestOf(rot.perm)
		return err
	}
	rot.perm = ranked
	rot.digest = rot.digestOf(ranked)
	return nil
}

// rank calls the Scorer behind a recover, because a panicking Scorer must cost
// the protocol its scoring and not its liveness.
func (rot *rotation) rank(s Snapshot) (out []ReplicaID, err error) {
	defer func() {
		if p := recover(); p != nil {
			out, err = nil, ErrScorerPermutation
		}
	}()
	// The Scorer is handed its own copy: a Scorer that sorts the slice it was
	// given in place would otherwise reorder the configuration itself, and
	// every quorum index with it.
	s.Replicas = append([]ReplicaID(nil), s.Replicas...)
	ranked := rot.scorer.Rank(s)
	if err := rot.validate(ranked); err != nil {
		return nil, err
	}
	return append([]ReplicaID(nil), ranked...), nil
}

func (rot *rotation) validate(ranked []ReplicaID) error {
	if len(ranked) != len(rot.replicas) {
		return ErrScorerPermutation
	}
	seen := make(map[ReplicaID]bool, len(ranked))
	for _, id := range ranked {
		if seen[id] {
			return ErrScorerPermutation
		}
		if !contains(rot.replicas, id) {
			return ErrScorerPermutation
		}
		seen[id] = true
	}
	return nil
}

// digestOf binds a permutation AND its epoch into one value, so a NEW-VIEW
// cannot claim a permutation computed at a different checkpoint.
func (rot *rotation) digestOf(perm []ReplicaID) Digest {
	b := make([]byte, 0, 32+len(perm)*16)
	b = append(b, "cspbft/rotation/v1\x00"...)
	b = binary.BigEndian.AppendUint64(b, uint64(rot.epoch))
	b = append(b, rot.state[:]...)
	b = binary.BigEndian.AppendUint64(b, uint64(len(perm)))
	for _, id := range perm {
		b = appendBytes(b, []byte(id))
	}
	return rot.hash(b)
}

// primary is perm[v mod n]. The modulus over a full permutation is what keeps
// Castro-Liskov's liveness bound: every replica takes its turn once per cycle,
// so a faulty one holds the view for a bounded run and then it is someone
// else's turn.
func (rot *rotation) primary(v View) ReplicaID {
	return rot.perm[uint64(v)%uint64(len(rot.perm))]
}

func contains(ids []ReplicaID, id ReplicaID) bool {
	i := sort.Search(len(ids), func(i int) bool { return ids[i] >= id })
	return i < len(ids) && ids[i] == id
}
