package database

import (
	"bytes"
	"context"
	"sync"

	"github.com/taubyte/tau/pkg/placement"
)

// maintain.go is what a node does about a database whose placement moved.
//
// # Two decisions, and only one of them can lose anything
//
// ACQUIRE is opening the database. On a store whose writes converge that is the
// whole of it: opening subscribes to that database's own topic and the rows
// arrive.
//
// RELEASE deletes the local copy, and it is the one that can cost the data. So
// it is gated on facts and never on placement alone — see release.
//
// # Nobody agrees about placement, and that is fine
//
// holds() is deterministic, not agreed: same inputs, same answer, with no
// coordination. But the inputs are four PER-NODE views — the floor, the pin at
// a register revision, a freshness-filtered membership, and a self-reported
// region — so two nodes can and do compute different desired sets.
//
// Membership divergence alone cannot produce a cycle. If P is desired by A and
// A is not, P outranks A; symmetrically A outranks P; contradiction. The other
// three CAN: a pin that moved between revisions, two nodes relabelled inside
// one freshness window, or two nodes draining at once — which is just a rolling
// restart — each leave both nodes computing the other desired, both converged,
// and both dropping.
//
// ONE RULE CLOSES ALL THREE: a keeper counts only if it answers `keeps` — its
// OWN holds() from its own inputs, decided under the same lock as its own
// release. In every cycle each node is releasing BECAUSE it computed itself not
// desired, so it answers keeps=false and the other stays. No two nodes have to
// agree on anything; each answer is a commitment about the answerer's own
// future, and a committed node can only drop through its own gated release.

// keeper is what one node answers about one database: whether it has the bytes,
// and whether it means to keep them.
//
// HELD AND KEEPS ARE DIFFERENT QUESTIONS. A node can hold a copy it is about to
// let go of — that is exactly a node mid-release — and counting it as a keeper
// is how two nodes both drop.
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

// release reports whether this node may stop holding a database.
//
// IT IS THE ONLY THING THAT MAY AUTHORISE A DELETE ON A MOVE, and the argument
// for it is a failure it prevents rather than a property it has:
//
//	eu goes dark; an operator repins to us to restore service; us nodes are
//	alive and are now the desired holders, but their source is down so they
//	receive nothing; eu comes back, reads the new placement, and drops the
//	only copies that ever existed.
//
// So this asks. Equal digests mean that node holds the same keys with the same
// values, which for a store whose writes converge is a proof and not a sample —
// and `keeps` means it is not about to let go of them itself.
//
// A NODE THAT CANNOT BE REACHED HAS NOT CONVERGED. An error is not a maybe: the
// answer to "may I delete the data" defaults to no, every time, for every reason.
//
// PER REGION, because that is what a pin means. A database pinned to eu and us
// has copies in BOTH and the floor is how many in EACH, so an eu node that lost
// its eu slot must not leave on the strength of two converged us keepers while
// its actual replacement in eu has not synced. A pinned region no node serves
// requires nothing — it is already the refusal placement.go returns empty for —
// but a desired set that is empty EVERYWHERE is not a move at all.
func release(ctx context.Context, at address, keys uint64, sum []byte, desired map[string][]string, replication int, ask digester) bool {
	total := 0
	for _, peers := range desired {
		total += len(peers)
	}
	if total == 0 {
		// NOBODY IS SUPPOSED TO HOLD THIS, which is not a move — it is a
		// database whose regions no node serves, and placement.go returns that
		// empty rather than filling it from elsewhere. Letting go here would
		// delete a copy that nothing is replacing.
		return false
	}

	for _, peers := range desired {
		want := replication
		if len(peers) < want {
			// THE REGION IS ITS OWN BAR WHEN IT IS SMALLER. Requiring the floor
			// where the region holds fewer nodes than that is a bar nothing can
			// clear, and the copy would be held forever.
			want = len(peers)
		}

		converged := 0
		for _, peer := range peers {
			answer, err := ask(ctx, peer, at)
			if err != nil {
				continue
			}
			if answer.held && answer.keeps && answer.keys == keys && bytes.Equal(answer.sum, sum) {
				converged++
			}
		}
		if converged < want {
			return false
		}
	}
	return true
}

// moved reports whether this node should still hold a database, and who should,
// BY REGION — because the bar release applies is per region.
//
// The desired sets EXCLUDE this node: a node comparing its digest against its
// own would converge with itself and let go.
func moved(at address, byRegion map[string][]placement.Member, regions []string, replication int, me, region string) (desired map[string][]string, keep bool) {
	if holds(at, byRegion, regions, replication, me, region) {
		return nil, true
	}

	desired = map[string][]string{}
	for _, pinned := range placementRegions(regions) {
		for _, id := range placement.Desired(resourceOf(at), membersIn(byRegion, pinned), replication) {
			if id != me {
				desired[pinned] = append(desired[pinned], id)
			}
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

// holdings is what this node is doing about each database it holds.
//
// IT EXISTS TO MAKE `keeps` HONEST. A node answers keeps=false while it is
// leaving, so a second node cannot count it as a reason to leave too. Two nodes
// that both begin leaving therefore both stay, and the next sweep resolves it
// once their inputs heal — which is how a rolling restart stops being a way to
// delete a database.
//
// THE LOCK IS NEVER HELD ACROSS THE NETWORK. A release takes it to mark
// `leaving`, releases it to go ask, and takes it again to commit or back out.
// Holding it across the asks would make two nodes probing each other deadlock.
type holdings struct {
	mu sync.Mutex
	at map[string]intent
}

func newHoldings() *holdings { return &holdings{at: map[string]intent{}} }

// keeps answers what a peer asking this node is entitled to rely on: that this
// node will not let go without its own gated release.
func (h *holdings) keeps(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.at[key] == kept
}

// begin marks a database as leaving, and reports false if this node is already
// letting it go — one release at a time per database, or two sweeps would ask
// on behalf of the same copy.
func (h *holdings) begin(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.at[key] != kept {
		return false
	}
	h.at[key] = leaving
	return true
}

// stay backs out of a release: the replacements did not answer, so this node
// keeps the copy and will ask again next sweep.
func (h *holdings) stay(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.at[key] == leaving {
		h.at[key] = kept
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
	if h.at[key] != leaving {
		h.at[key] = kept
	}
}
