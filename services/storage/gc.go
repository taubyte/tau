package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ipfs/bbloom"
	"github.com/ipfs/go-cid"
)

// gc.go is where bytes are destroyed, so it is the file with the most
// conditions in it and the fewest clever ideas.
//
// TWO DELETIONS THAT LOOK ALIKE AND ARE NOT:
//
//	A DEAD OBJECT     nobody references these bytes any more. Gates 1-4.
//	A MISPLACEMENT    somebody does, but not here. Gates 1, 2 and 5.
//
// They are separate deliberately, and v1 is why: it conflated a CLAIM with
// POSSESSION and let a data-destruction path trust a peer's self-assertion
// that it held a copy (registry.go -> stream.go -> migration/sweep.go). A node
// dropping its copy because another node SAID it had one is a node deleting
// the last copy.
//
// ── THE FIVE GATES ───────────────────────────────────────────────────────
//
//  1. THIS NODE HAS AUTHORITY. It holds the referencing shard AND is
//     HRW-desired for it under the STABLE membership set. A node that just
//     lost the shard has no standing to read the truth, and a node that
//     merely opened it has not been told it should have.
//
//  2. NO CO-HOLDER IS BEHIND. A replica that has not merged a head its peers
//     advertised must not conclude that anything is dead: what it cannot see
//     may be the re-create. The signal is the shard store's own heads against
//     the heads seen on the topic — *kvdb.Datastore.InternalStats().Heads —
//     and wiring it is the service round's; the GATE is here so that round has
//     somewhere to put the answer rather than a decision to re-litigate.
//
//  3. THE REFERENCE WALK WAS DEFINITIVE. A byte holder does not learn of a
//     delete by subscribing to a shard it does not hold — it ASKS. An
//     unreachable index holder means UNKNOWN, never DEAD. That is v1's one
//     piece of discipline worth carrying verbatim (reconcile.go's rule that a
//     TNS outage is not a deletion), and it is the difference between a
//     partition costing availability and a partition costing data.
//
//  4. THE GRACE WINDOW HAS ELAPSED, TIMED FROM LOCAL OBSERVATION. Never from
//     the stamp's physical component. See refs.go: this is what keeps every
//     clock in the fleet out of the path that destroys data.
//
//  5. FOR A MISPLACEMENT DROP ONLY: R OTHER HOLDERS HAVE PASSED A POSSESSION
//     PROBE. Passed, not claimed. The probe is a nonce and an HMAC over blocks
//     at nonce-derived offsets — one or two block reads, unforgeable without
//     the bytes — so A REPLICA COUNT BECOMES A MEASUREMENT. The probe itself is
//     an RPC and belongs to that round; the gate counts PROVEN holders and has
//     no door that takes a claimed one.
//
// ── THE SWEEP, AND THE ONE ENUMERATION IT MAY MAKE ───────────────────────
//
// Mark every block reachable from a live root; then walk THIS SERVICE'S OWN
// BLOCK INDEX and delete what is unmarked. It never enumerates the blockstore,
// because the blockstore is shared with pkg/kvdb's own Merkle-DAG blocks and
// with every other consumer on the node (refs.go says this at length, and
// config.go's Blocks interface has no door for it).
//
// A FALSE POSITIVE KEEPS A DEAD BLOCK; IT CAN NEVER DELETE A LIVE ONE. That is
// the direction every approximation here runs in, and it is why a Bloom filter
// is admissible at all: a Bloom has false positives and no false negatives, so
// its error mode is a space leak. Space leaks; data does not.
//
// AN INCOMPLETE WALK DELETES NOTHING. If a live root's DAG cannot be walked to
// its leaves on this node, the set of blocks below it is unknown, and an
// unknown set cannot be subtracted from. The sweep reports the roots it could
// not finish and deletes nothing at all that cycle. It is self-healing rather
// than an operator step: a byte holder writes its reference row only after the
// whole DAG is verified present, so an incomplete walk is a real anomaly, and
// the repair loop refetching it is what clears it.
//
// AND IT MUST SWEEP THE WHOLE DAG, NOT THE ROOT. v1's DeleteFile removes
// exactly one block (p2p/peer/dag.go's merkledag.Remove -> one DeleteBlock),
// orphaning every leaf of every multi-chunk object permanently — and there is
// no blockstore GC anywhere in v1's tree to collect them later.

// Purpose is why blocks are being deleted. It selects which gates apply, and
// it is an argument rather than a guess because the two cases genuinely differ
// and reading one for the other is how v1 dropped copies.
type Purpose int

const (
	// DeadObject is "nobody references these bytes any more". Gates 1-4.
	DeadObject Purpose = iota
	// MisplacementDrop is "somebody does, but this node should not". Gates 1,
	// 2 and 5.
	MisplacementDrop
)

func (p Purpose) String() string {
	switch p {
	case DeadObject:
		return "dead object"
	case MisplacementDrop:
		return "misplacement drop"
	default:
		return fmt.Sprintf("purpose(%d)", int(p))
	}
}

// Gates is the evidence a deletion decision rests on. Every field is something
// this node MEASURED, and there is no field for something a peer said about
// itself.
type Gates struct {
	// HoldsShard is gate 1a: this node has the referencing shard open.
	HoldsShard bool

	// DesiredUnderStable is gate 1b: HRW places that shard here, under the
	// stable membership set rather than the live one.
	DesiredUnderStable bool

	// CoHoldersMerged is gate 2: no co-holder has advertised a head this node
	// has not merged.
	CoHoldersMerged bool

	// ReferencesDefinitive is gate 3: every index holder that had to be asked
	// answered. One unreachable holder makes this false.
	ReferencesDefinitive bool

	// TombstoneObservedAt is gate 4's input: when THIS node first saw the
	// tombstone, from refs.go's local monotonic row. The zero time means it
	// has never been observed here, which is not an elapsed grace window.
	TombstoneObservedAt time.Time

	// ProvenHolders is gate 5: how many OTHER nodes passed a possession probe.
	// Nothing sets it from a claim; there is no such door.
	ProvenHolders int
}

// Open reports whether every gate that applies to this purpose is open, and
// names the first one that is not. A caller that cannot say which gate closed
// cannot be trusted to delete data, so the error always says.
func (g Gates) Open(p Purpose, now time.Time, grace time.Duration, replicas int) error {
	if !g.HoldsShard {
		return fmt.Errorf("%w: %s: this node does not hold the referencing shard", ErrGateClosed, p)
	}
	if !g.DesiredUnderStable {
		return fmt.Errorf("%w: %s: this node is not HRW-desired for that shard under the stable membership", ErrGateClosed, p)
	}
	if !g.CoHoldersMerged {
		return fmt.Errorf("%w: %s: a co-holder has advertised a head this node has not merged", ErrGateClosed, p)
	}

	switch p {
	case DeadObject:
		if !g.ReferencesDefinitive {
			return fmt.Errorf("%w: %s: an index holder did not answer, so the references are unknown rather than dead", ErrGateClosed, p)
		}
		if g.TombstoneObservedAt.IsZero() {
			return fmt.Errorf("%w: %s: this node has never observed the tombstone", ErrGateClosed, p)
		}
		if elapsed := now.Sub(g.TombstoneObservedAt); elapsed < grace {
			return fmt.Errorf("%w: %s: observed %s ago, grace is %s", ErrGateClosed, p, elapsed, grace)
		}
	case MisplacementDrop:
		if g.ProvenHolders < replicas {
			return fmt.Errorf("%w: %s: %d other holders proved possession, %d required", ErrGateClosed, p, g.ProvenHolders, replicas)
		}
	default:
		return fmt.Errorf("%w: unknown %s", ErrGateClosed, p)
	}
	return nil
}

// Marker records which blocks are reachable from a live root.
//
// THE CONTRACT IS ONE-SIDED ON PURPOSE. Marked may answer true for a block
// that was never marked; it must NEVER answer false for one that was. A caller
// deletes only what is unmarked, so the permitted error keeps a dead block and
// the forbidden one destroys a live one.
type Marker interface {
	Mark(c cid.Cid)
	Marked(c cid.Cid) bool
}

// exactMarker is a set. It is the default because it is obviously correct; it
// costs a multihash per reachable block.
type exactMarker struct{ seen map[string]struct{} }

// NewExactMarker builds a marker that answers exactly.
func NewExactMarker(hint int) Marker {
	return &exactMarker{seen: make(map[string]struct{}, hint)}
}

func (m *exactMarker) Mark(c cid.Cid)        { m.seen[c.KeyString()] = struct{}{} }
func (m *exactMarker) Marked(c cid.Cid) bool { _, ok := m.seen[c.KeyString()]; return ok }

// bloomMarker is a Bloom filter sized from the reference count, for a node
// whose reachable set does not fit comfortably in memory.
//
// It is admissible for exactly one reason: its errors are false POSITIVES, and
// a false positive here keeps a dead block. It is not "probably fine"; it is
// wrong in the only direction that is safe.
type bloomMarker struct{ b *bbloom.Bloom }

// NewBloomMarker sizes a filter for `entries` blocks at `falsePositiveRate`.
// A rate at or above 1 is refused rather than silently treated as a location
// count, which is what bbloom's own two-number constructor does.
func NewBloomMarker(entries int, falsePositiveRate float64) (Marker, error) {
	if entries < 1 {
		entries = 1
	}
	if falsePositiveRate <= 0 || falsePositiveRate >= 1 {
		return nil, fmt.Errorf("storage: bloom false-positive rate %v is not in (0,1)", falsePositiveRate)
	}
	b, err := bbloom.New(float64(entries), falsePositiveRate)
	if err != nil {
		return nil, fmt.Errorf("storage: sizing the sweep bloom: %w", err)
	}
	return &bloomMarker{b: b}, nil
}

func (m *bloomMarker) Mark(c cid.Cid)        { m.b.Add([]byte(c.KeyString())) }
func (m *bloomMarker) Marked(c cid.Cid) bool { return m.b.Has([]byte(c.KeyString())) }

// Sweeper is the mark-and-sweep over this node's own block index.
type Sweeper struct {
	local  *Local
	blocks Blocks

	// marker builds the mark set, given how many blocks are expected. It is a
	// field so a large node can hand in a Bloom without this file choosing for
	// it, and so a test can hand in one that is deliberately terrible.
	marker func(hint int) (Marker, error)
}

// NewSweeper builds a sweeper. A nil marker means the exact one.
func NewSweeper(local *Local, blocks Blocks, marker func(hint int) (Marker, error)) (*Sweeper, error) {
	if local == nil {
		return nil, errors.New("storage: no local rows to sweep")
	}
	if blocks == nil {
		return nil, errors.New("storage: no block plane to sweep")
	}
	if marker == nil {
		marker = func(hint int) (Marker, error) { return NewExactMarker(hint), nil }
	}
	return &Sweeper{local: local, blocks: blocks, marker: marker}, nil
}

// SweepResult is what one cycle did, and what it refused to do.
type SweepResult struct {
	// Roots is how many distinct live roots were walked.
	Roots int
	// Marked is how many distinct blocks the walk reached.
	Marked int
	// Indexed is how many rows this service's block index held.
	Indexed int
	// Deleted is how many blocks were freed.
	Deleted int
	// Incomplete lists live roots whose DAG could not be walked to its leaves
	// on this node. A non-empty list means Deleted is zero: the sweep leaked
	// rather than guessed.
	Incomplete []cid.Cid
}

// Sweep marks from every live reference and frees what is unreachable.
//
// ORDER: THE BLOCK GOES BEFORE ITS INDEX ROW. A crash between the two leaves
// an index row naming a block that is gone, which the next sweep resolves for
// free — it marks nothing, deletes a block that is already absent, and drops
// the row. The other order leaves a block nothing indexes, which no sweep can
// ever find again: a permanent leak, invisible, growing.
//
// THAT IS NOT THE SAME ORDERING AS A DELETE'S. A delete writes the REFERENCE
// first and frees the bytes later — v1 does that one backwards, freeing bytes
// at methods.go:231 before dropping the reference at :235, so a crash between
// them leaves a live reference to bytes that are gone. The two orderings point
// opposite ways because they protect opposite things: a reference outliving
// its bytes is a broken read, and a block outliving its index row is an
// unreclaimable leak.
func (s *Sweeper) Sweep(ctx context.Context) (SweepResult, error) {
	refs, err := s.local.References(ctx)
	if err != nil {
		return SweepResult{}, err
	}

	roots := make([]cid.Cid, 0, len(refs))
	seenRoot := make(map[string]struct{}, len(refs))
	for _, r := range refs {
		if _, dup := seenRoot[r.Root.KeyString()]; dup {
			continue
		}
		seenRoot[r.Root.KeyString()] = struct{}{}
		roots = append(roots, r.Root)
	}

	indexed, err := s.local.IndexedBlocks(ctx)
	if err != nil {
		return SweepResult{}, err
	}

	result := SweepResult{Roots: len(roots), Indexed: len(indexed)}

	marker, err := s.marker(len(indexed))
	if err != nil {
		return SweepResult{}, err
	}

	for _, root := range roots {
		marked, complete, werr := s.mark(ctx, root, marker)
		if werr != nil {
			return SweepResult{}, werr
		}
		result.Marked += marked
		if !complete {
			result.Incomplete = append(result.Incomplete, root)
		}
	}

	// An unknown set cannot be subtracted from.
	if len(result.Incomplete) > 0 {
		return result, nil
	}

	for _, c := range indexed {
		if marker.Marked(c) {
			continue
		}
		if err := s.blocks.Delete(ctx, c); err != nil {
			return result, fmt.Errorf("storage: freeing %s: %w", c, err)
		}
		if err := s.local.Forget(ctx, c); err != nil {
			return result, fmt.Errorf("storage: dropping the index row for %s: %w", c, err)
		}
		result.Deleted++
	}
	return result, nil
}

// mark walks one root's DAG, marking every block it reaches, and reports
// whether it reached the leaves.
//
// IT NEVER LEAVES THE NODE. Blocks.Links is a local-only door by contract
// (config.go), implemented over an offline session so a walk cannot block on a
// peer — a sweep that waited on the network would hold a deletion decision
// open across a partition, which is gate 3's failure wearing a different hat.
//
// A block this node does not hold makes the walk INCOMPLETE rather than
// finished: this node may hold a descendant of it, and marking would then miss
// that descendant and delete it. Naive implementations skip the missing child
// and lose a live grandchild.
func (s *Sweeper) mark(ctx context.Context, root cid.Cid, marker Marker) (marked int, complete bool, err error) {
	visited := make(map[string]struct{})
	stack := []cid.Cid{root}
	complete = true

	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return marked, false, err
		}
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if _, seen := visited[c.KeyString()]; seen {
			continue
		}
		visited[c.KeyString()] = struct{}{}

		have, err := s.blocks.Has(ctx, c)
		if err != nil {
			return marked, false, fmt.Errorf("storage: sweeping %s: %w", c, err)
		}
		if !have {
			complete = false
			continue
		}

		marker.Mark(c)
		marked++

		links, err := s.blocks.Links(ctx, c)
		if err != nil {
			return marked, false, fmt.Errorf("storage: walking %s: %w", c, err)
		}
		stack = append(stack, links...)
	}
	return marked, complete, nil
}
