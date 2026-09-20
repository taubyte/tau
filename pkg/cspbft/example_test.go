package cspbft

import (
	"crypto/sha256"
	"errors"
	"fmt"
)

// Example is the driver the package doc says the caller writes: a loop, a
// queue, and nothing else. It is here compiled and run rather than described,
// because "about forty lines" is a claim until somebody writes them.
func Example() {
	set := []ReplicaID{"r0", "r1", "r2", "r3"}

	// The two seams that are not funcs of the caller's own choosing. A real
	// deployment puts ed25519 behind these; the point is that this package
	// never learns what is behind them.
	sign := func(self ReplicaID) Signer {
		return func(pre []byte) ([]byte, error) {
			h := sha256.Sum256(append([]byte(self), pre...))
			return h[:], nil
		}
	}
	verify := func(from ReplicaID, pre, sig []byte) error {
		h := sha256.Sum256(append([]byte(from), pre...))
		if string(h[:]) != string(sig) {
			return fmt.Errorf("bad signature from %s", from)
		}
		return nil
	}
	verifyClient := func(from ClientID, pre, sig []byte) error {
		h := sha256.Sum256(append([]byte(from), pre...))
		if string(h[:]) != string(sig) {
			return fmt.Errorf("bad signature from client %s", from)
		}
		return nil
	}

	replicas := map[ReplicaID]*Replica{}
	for _, id := range set {
		r, err := New(set, id, WithSigner(sign(id)), WithVerifier(verify), WithClientVerifier(verifyClient))
		if err != nil {
			panic(err)
		}
		replicas[id] = r
	}

	type letter struct {
		to  ReplicaID
		msg Message
	}
	var queue []letter

	// The loop. Step says what to do; the caller does it.
	var apply func(self ReplicaID, acts []Action)
	apply = func(self ReplicaID, acts []Action) {
		for _, a := range acts {
			switch v := a.(type) {
			case Send:
				queue = append(queue, letter{v.To, v.Message})
			case Broadcast:
				for _, id := range set {
					if id != self {
						queue = append(queue, letter{id, v.Message})
					}
				}
			case Persist:
				// Write v.Records durably HERE, before anything below it in
				// this slice is performed.
			case Execute:
				if self == "r0" {
					fmt.Printf("%s executed sequence %d: %s\n", self, v.Seq, v.Proposal)
				}
			case TakeCheckpoint:
				// A real application digests its state; this one has none.
				more, err := replicas[self].Checkpointed(v.Seq, Digest{})
				if err != nil {
					panic(err)
				}
				apply(self, more)
			}
		}
	}
	// ErrFutureView is the one error this loop may not drop. A new primary
	// broadcasts its NEW-VIEW and then assigns in that view immediately, so a
	// recipient that throws away the assignment because it won the race will
	// never be sent it again — and the cluster waits for a replica that is
	// waiting for a message it has already discarded.
	held := map[ReplicaID][]letter{}
	run := func() {
		for len(queue) > 0 {
			l := queue[0]
			queue = queue[1:]
			before := replicas[l.to].CurrentView()
			acts, err := replicas[l.to].Step(l.msg)
			if errors.Is(err, ErrFutureView) {
				held[l.to] = append(held[l.to], l)
			}
			apply(l.to, acts)
			if replicas[l.to].CurrentView() != before {
				queue = append(queue, held[l.to]...)
				delete(held, l.to)
			}
		}
	}

	// A client signs its request over Preimage, never over the wire bytes.
	submit := func(serial uint64, payload string) {
		m := Message{Kind: KindRequest, Client: "alice", Serial: serial, Proposal: Proposal(payload)}
		m.Digest = RequestDigest(SHA256, "alice", serial, m.Proposal)
		h := sha256.Sum256(append([]byte("alice"), Preimage(m)...))
		m.Signature = h[:]
		for _, id := range set {
			queue = append(queue, letter{id, m})
		}
	}

	submit(1, "put k=1")
	run()
	submit(2, "put k=2")
	run()

	fmt.Println("quorum:", Quorum(len(set)), "faults tolerated:", Faults(len(set)))
	fmt.Println(Advice(len(set))[0])
	// Output:
	// r0 executed sequence 1: put k=1
	// r0 executed sequence 2: put k=2
	// quorum: 3 faults tolerated: 1
	// n=4 tolerates 1 faulty replica and needs a quorum of 3
}
