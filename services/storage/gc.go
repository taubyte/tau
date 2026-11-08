package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ipfs/bbloom"
	"github.com/ipfs/go-cid"
)

// Two deletions, separate on purpose:
//
//	A DEAD OBJECT     nobody references these bytes any more. Gates 1-4.
//	A MISPLACEMENT    somebody does, but not here. Gates 1, 2 and 5.
//
//  1. Authority: this node holds the referencing shard and is HRW-desired for it
//     under the stable membership set.
//  2. No co-holder is behind: a replica that has not merged its peers' heads may
//     be missing the re-create.
//  3. The reference walk was definitive: an unreachable index holder means
//     unknown, never dead.
//  4. The grace window has elapsed, timed from local observation (refs.go).
//  5. Misplacement only: R other holders passed a possession probe (an HMAC over
//     nonce-chosen blocks), and R is the cloud's.
//
// The sweep marks every block reachable from a live root, then deletes unmarked
// blocks from this service's own block index, never the shared blockstore. A false
// positive keeps a dead block; an incomplete walk deletes nothing that cycle and
// heals once the missing block returns. It sweeps the whole DAG, not the root.

// Purpose is why blocks are being deleted, selecting which gates apply.
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

// Gates is the evidence a deletion rests on: all measured by this node, nothing a
// peer said about itself.
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

// Policy is what the cloud promises, as the deleting gate sees it. Without Known,
// gate 5 has no right-hand side, and dead objects are still collected.
type Policy struct {
	// Replicas is R as the cloud published it. It is meaningless unless Known.
	Replicas int

	// Known is whether this node has ever read R at all.
	Known bool
}

// Open reports whether every gate for this purpose is open, naming the first that is
// not. Policy is one argument so R and its validity come from one read.
func (g Gates) Open(p Purpose, now time.Time, grace time.Duration, policy Policy) error {
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
		// R is the right-hand side of a misplacement drop; a node without one does not pick.
		if !policy.Known {
			return fmt.Errorf("%w: %s: this node has never read the cloud's replica target", ErrGateClosed, p)
		}
		if g.ProvenHolders < policy.Replicas {
			return fmt.Errorf("%w: %s: %d other holders proved possession, %d required", ErrGateClosed, p, g.ProvenHolders, policy.Replicas)
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

// Sweep marks from every live reference and frees what is unreachable. The block goes
// before its index row: a crash between leaves a row the next sweep resolves, while
// the other order leaks a block nothing indexes. (A delete is the opposite: the
// reference first, so no live reference outlives its bytes.)
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

// mark walks one root's DAG and reports whether it reached the leaves, never leaving
// the node. A block this node lacks makes the walk incomplete rather than skipped,
// since this node may hold its descendants.
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
