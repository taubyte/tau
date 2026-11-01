package database

import (
	"bytes"
	"context"
	"sync"

	"github.com/taubyte/tau/pkg/placement"
)

// Acquire is opening the database; on a converging store the rows then arrive.
// Release deletes the local copy and is gated on facts, never placement alone.
//
// holds() is deterministic, not agreed: its inputs (floor, declaration revision,
// freshness-filtered membership) are per-node views. Membership divergence alone
// cannot cycle, but a moved floor or two nodes draining at once can leave each
// computing the other desired. One rule closes it: a keeper counts only if it
// answers `keeps`, its own holds() decided under the lock of its own release. A
// releasing node answers keeps=false, so the other stays.

// keeper is what one node answers about one database: whether it has the bytes, and
// whether it means to keep them. A node mid-release holds but does not keep.
type keeper struct {
	keys  uint64
	sum   []byte
	held  bool
	keeps bool
}

// digester asks one node what it holds for a database, and whether it keeps it.
// The loop takes it as a function so a test can answer for a fleet that does
// not exist.
type digester func(ctx context.Context, peer string, at address) (keeper, error)

// release reports whether this node may stop holding a database, the only authority
// for a delete on a move. It prevents this:
//
//	the holders go dark; membership moves the database to other nodes, which
//	are alive and now desired, but their source is down so they receive
//	nothing; the old holders come back, read the new placement, and drop the
//	only copies that ever existed.
//
// Equal digests prove a converging store holds the same rows, and `keeps` that the
// node will not let go itself. An unreachable node has not converged. The bar is the
// floor, or every replacement when fewer exist.
func release(ctx context.Context, at address, keys uint64, sum []byte, desired []string, replication int, ask digester) bool {
	if len(desired) == 0 {
		// NOBODY ELSE IS SUPPOSED TO HOLD THIS, which is not a move. Letting go
		// here would delete a copy that nothing is replacing.
		return false
	}

	want := min(replication, len(desired))
	converged := 0
	for _, peer := range desired {
		answer, err := ask(ctx, peer, at)
		if err != nil {
			continue
		}
		if answer.held && answer.keeps && answer.keys == keys && bytes.Equal(answer.sum, sum) {
			converged++
		}
	}
	return converged >= want
}

// moved reports whether this node should still hold a database, and who should. The
// desired set excludes this node, which would otherwise converge with itself.
func moved(at address, members []placement.Member, replication int, me string) (desired []string, keep bool) {
	if holds(at, members, replication, me) {
		return nil, true
	}

	for _, id := range holders(at, members, replication) {
		if id != me {
			desired = append(desired, id)
		}
	}
	return desired, false
}

// intent is what this node is doing about one database.
type intent int

const (
	// away is a database this node holds nothing for.
	away intent = iota
	// kept is one it holds and means to keep.
	kept
	// leaving is one it holds and has begun letting go of — it has asked its
	// replacements and is waiting on their answers.
	leaving
)

// state is what this node is doing about one database, and what is being done
// to it while that is decided.
type state struct {
	intent intent

	// sealed is a copy whose gate has been cleared and whose bytes are about to
	// go: nothing may start writing into it.
	sealed bool

	// writing is how many writes are in flight, and written records that one completed
	// since the release began: the gate's answer is about the digest taken then.
	writing int
	written bool
}

// holdings is what this node is doing about each database it holds. It makes `keeps`
// honest (two nodes both leaving both stay) and is the write fence, one decision in
// one table. The lock is never held across the network, or two nodes probing each
// other would deadlock.
type holdings struct {
	mu sync.Mutex
	at map[string]*state
}

func newHoldings() *holdings { return &holdings{at: map[string]*state{}} }

// of answers the entry for one database, making it if there is none. Called
// with h.mu held.
func (h *holdings) of(key string) *state {
	at, held := h.at[key]
	if !held {
		at = &state{}
		h.at[key] = at
	}
	return at
}

// keeps answers what a peer asking this node is entitled to rely on: that this
// node will not let go without its own gated release.
func (h *holdings) keeps(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	at, held := h.at[key]
	return held && at.intent == kept
}

// begin marks a database as leaving, and reports false if this node is already
// letting it go — one release at a time per database, or two sweeps would ask
// on behalf of the same copy.
func (h *holdings) begin(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	at, held := h.at[key]
	if !held || at.intent != kept {
		return false
	}
	at.intent = leaving
	at.written = false
	return true
}

// seal stops this database accepting writes, and reports what the gate's answer is
// still about. It refuses over a half-done write. A copy written into since the
// release began is sealed but not clean; the caller asks again.
func (h *holdings) seal(key string) (sealed, clean bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	at, held := h.at[key]
	if !held || at.intent != leaving || at.writing > 0 {
		return false, false
	}
	at.sealed = true
	return true, !at.written
}

// writing takes a write slot, and reports whether the write may proceed. Only the
// sealed window refuses; earlier writes make the release ask again.
func (h *holdings) writing(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	at := h.of(key)
	if at.sealed {
		return false
	}
	// Writing is holding.
	if at.intent != leaving {
		at.intent = kept
	}
	at.writing++
	return true
}

// wrote releases the slot writing took.
func (h *holdings) wrote(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	at, held := h.at[key]
	if !held {
		return
	}
	if at.writing > 0 {
		at.writing--
	}
	if at.intent == leaving {
		at.written = true
	}
}

// stay backs out of a release: the replacements did not answer, so this node
// keeps the copy, accepts writes into it again, and will ask again next sweep.
func (h *holdings) stay(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	at, held := h.at[key]
	if !held {
		return
	}
	at.sealed = false
	if at.intent == leaving {
		at.intent = kept
	}
}

// gone records that the copy is released and the bytes are removed.
func (h *holdings) gone(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.at, key)
}

// hold records that this node holds a database and means to keep it.
func (h *holdings) hold(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if at := h.of(key); at.intent != leaving {
		at.intent = kept
	}
}
