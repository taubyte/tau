package storage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ipfs/bbloom"
	"github.com/ipfs/go-cid"
)

// Three collections, separate on purpose, each dropping a reference this node holds
// and each behind its own gates:
//
//	SUPERSEDED  a later version of the name replaced this one, and its bucket keeps
//	            no versions it replaced.
//	ABANDONED   a write that never became a version: no index holder has its row.
//	MISPLACED   the bytes are wanted, but placement no longer puts them here.
//
// Superseded: as many index holders as a write is acknowledged on hold the version's
// row and a newer one, and none says the version is kept (collect.go); then the grace
// window, timed from this node's first such answer (refs.go). Rows are never removed,
// so a version once superseded stays superseded, and a holder that is behind says
// nothing rather than something wrong.
//
// Abandoned: every holder placement names answers and none holds the row, in two
// passes at least the grace window apart. A holder that has not merged the row yet
// answers the same, and has had the window to merge it.
//
// Misplaced: every holder placement names proved it holds the root (probe.go) and
// took every reference this node has to it (repair.go), or this node holds no copy
// to prove theirs against and its references were all it had to give.
//
// Then the sweep marks every block reachable from a reference left, and deletes
// unmarked blocks from this service's own block index, never the shared blockstore.
// A false positive keeps a dead block; an incomplete walk deletes nothing that cycle
// and heals once the missing block returns. It sweeps the whole DAG, not the root.

// Purpose is why a reference is dropped, selecting which gates apply.
type Purpose int

const (
	// Superseded is a version a later one replaced, in a bucket that keeps no
	// versions it replaced.
	Superseded Purpose = iota
	// Abandoned is a write that never became a version.
	Abandoned
	// Misplaced is bytes wanted elsewhere: placement no longer puts them here.
	Misplaced
)

func (p Purpose) String() string {
	switch p {
	case Superseded:
		return "superseded"
	case Abandoned:
		return "abandoned"
	case Misplaced:
		return "misplaced"
	default:
		return fmt.Sprintf("purpose(%d)", int(p))
	}
}

// Gates is the evidence a drop rests on: all measured by this node, nothing a peer
// said about itself.
type Gates struct {
	// SupersededAt is when this node first had as many index holders as a write
	// is acknowledged on answer that they hold a newer row than the version's,
	// none of them saying it is kept. The zero time is never.
	SupersededAt time.Time

	// AbsentAt is when this node first had every holder placement names answer
	// that it holds no row of the version. The zero time is never.
	AbsentAt time.Time

	// AbsentNow is every holder placement names answering, this pass, that it
	// holds no row of the version.
	AbsentNow bool

	// ProvenHolders is how many of the holders placement names passed a
	// possession probe. Nothing sets it from a claim; there is no such door.
	ProvenHolders int

	// Handed is every holder placement names having taken every reference this
	// node has to the root.
	Handed bool

	// Bare is this node holding no copy of the root to prove theirs against.
	Bare bool
}

// Open reports whether every gate for this purpose is open, naming the first that is
// not. holders is how many holders placement names for the root, which a misplaced
// copy needs every one of.
func (g Gates) Open(p Purpose, now time.Time, grace time.Duration, holders int) error {
	switch p {
	case Superseded:
		if g.SupersededAt.IsZero() {
			return fmt.Errorf("%w: %s: no barrier of index holders has said a newer version exists", ErrGateClosed, p)
		}
		if elapsed := now.Sub(g.SupersededAt); elapsed < grace {
			return fmt.Errorf("%w: %s: found %s ago, grace is %s", ErrGateClosed, p, elapsed, grace)
		}
	case Abandoned:
		if g.AbsentAt.IsZero() || !g.AbsentNow {
			return fmt.Errorf("%w: %s: an index holder holds its row, or did not answer", ErrGateClosed, p)
		}
		if elapsed := now.Sub(g.AbsentAt); elapsed < grace {
			return fmt.Errorf("%w: %s: first found absent %s ago, grace is %s", ErrGateClosed, p, elapsed, grace)
		}
	case Misplaced:
		if holders < 1 {
			return fmt.Errorf("%w: %s: placement names no holder to leave it to", ErrGateClosed, p)
		}
		if !g.Handed {
			return fmt.Errorf("%w: %s: a holder has not taken every reference this node has", ErrGateClosed, p)
		}
		if !g.Bare && g.ProvenHolders < holders {
			return fmt.Errorf("%w: %s: %d of %d holders proved possession", ErrGateClosed, p, g.ProvenHolders, holders)
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

// Markers builds the mark set a sweep fills, given how many blocks are expected.
type Markers interface {
	New(hint int) (Marker, error)
}

// ExactMarkers builds exact markers, the default.
type ExactMarkers struct{}

func (ExactMarkers) New(hint int) (Marker, error) { return NewExactMarker(hint), nil }

// BloomMarkers builds Bloom filters at Rate, for a node whose reachable set does not
// fit comfortably in memory.
type BloomMarkers struct{ Rate float64 }

func (b BloomMarkers) New(hint int) (Marker, error) { return NewBloomMarker(hint, b.Rate) }

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

// Sweeper is the mark-and-sweep over this node's own block index, and what a keep
// records through so that no sweep deletes a block the keep relies on.
//
// A KEEP AND A SWEEP'S DELETES EXCLUDE EACH OTHER. A write stores its bytes, finding
// some blocks already here and storing nothing of them, then keeps them: it indexes
// every block and references the root. A block unreferenced before that is a sweep's
// to delete. So a keep holds keeping shared, refusing a block that is gone by then,
// and records its root as fresh while a sweep runs; the sweep marks without holding
// it, and deletes holding it alone, a chunk at a time, marking every fresh root first.
type Sweeper struct {
	local   *Local
	blocks  Blocks
	markers Markers

	keeping sync.RWMutex

	// sweepMu runs one sweep at a time: a second would reset the first's fresh.
	sweepMu sync.Mutex

	freshMu  sync.Mutex
	sweeping bool
	fresh    map[string]cid.Cid
}

// sweepChunk is how many unmarked blocks a sweep deletes per hold of keeping: a keep
// waits at most that long.
const sweepChunk = 256

// NewSweeper builds a sweeper. Nil markers means exact ones.
func NewSweeper(local *Local, blocks Blocks, markers Markers) (*Sweeper, error) {
	if local == nil {
		return nil, errors.New("storage: no local rows to sweep")
	}
	if blocks == nil {
		return nil, errors.New("storage: no block plane to sweep")
	}
	if markers == nil {
		markers = ExactMarkers{}
	}
	return &Sweeper{local: local, blocks: blocks, markers: markers}, nil
}

// Keep records that this node holds root's blocks for a version: each block indexed,
// then the reference. It refuses with ErrSwept when one of them is gone.
func (s *Sweeper) Keep(ctx context.Context, root cid.Cid, blocks []cid.Cid, a Address, name string, stamp uint64) error {
	s.keeping.RLock()
	defer s.keeping.RUnlock()
	for _, b := range blocks {
		have, err := s.blocks.Has(ctx, b)
		if err != nil {
			return err
		}
		if !have {
			return fmt.Errorf("%w: %s of %s", ErrSwept, b, root)
		}
	}
	for _, b := range blocks {
		if err := s.local.Imported(ctx, b); err != nil {
			return err
		}
	}
	if err := s.local.Reference(ctx, root, a, name, stamp); err != nil {
		return err
	}
	s.freshMu.Lock()
	if s.sweeping {
		s.fresh[root.KeyString()] = root
	}
	s.freshMu.Unlock()
	return nil
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
	// on this node. A non-empty list stops the deletes: the sweep leaked rather
	// than guessed.
	Incomplete []cid.Cid
}

// Sweep marks from every live reference and frees what is unreachable. The block goes
// before its index row: a crash between leaves a row the next sweep resolves, while
// the other order leaks a block nothing indexes. (A delete is the opposite: the
// reference first, so no live reference outlives its bytes.)
func (s *Sweeper) Sweep(ctx context.Context) (SweepResult, error) {
	s.sweepMu.Lock()
	defer s.sweepMu.Unlock()
	s.begin()
	defer s.end()

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

	marker, err := s.markers.New(len(indexed))
	if err != nil {
		return SweepResult{}, err
	}

	for _, root := range roots {
		if err := s.markRoot(ctx, root, marker, &result); err != nil {
			return SweepResult{}, err
		}
	}

	for start := 0; start < len(indexed) && len(result.Incomplete) == 0; start += sweepChunk {
		if err := s.deleteUnmarked(ctx, indexed[start:min(start+sweepChunk, len(indexed))], marker, &result); err != nil {
			return result, err
		}
	}
	return result, nil
}

// begin starts a sweep: every keep from here on records its root as fresh. Any keep
// still running is waited for, so its reference is among those the sweep lists.
func (s *Sweeper) begin() {
	s.keeping.Lock()
	defer s.keeping.Unlock()
	s.freshMu.Lock()
	defer s.freshMu.Unlock()
	s.sweeping, s.fresh = true, map[string]cid.Cid{}
}

func (s *Sweeper) end() {
	s.freshMu.Lock()
	defer s.freshMu.Unlock()
	s.sweeping, s.fresh = false, nil
}

// deleteUnmarked deletes the unmarked blocks of chunk holding keeping alone, once the
// roots kept since the marks began are marked. An unknown set cannot be subtracted
// from: a root that cannot be walked stops it.
func (s *Sweeper) deleteUnmarked(ctx context.Context, chunk []cid.Cid, marker Marker, result *SweepResult) error {
	s.keeping.Lock()
	defer s.keeping.Unlock()
	s.freshMu.Lock()
	fresh := s.fresh
	s.fresh = map[string]cid.Cid{}
	s.freshMu.Unlock()
	for _, root := range fresh {
		if err := s.markRoot(ctx, root, marker, result); err != nil {
			return err
		}
	}
	if len(result.Incomplete) > 0 {
		return nil
	}
	for _, c := range chunk {
		if marker.Marked(c) {
			continue
		}
		if err := s.blocks.Delete(ctx, c); err != nil {
			return fmt.Errorf("storage: freeing %s: %w", c, err)
		}
		if err := s.local.Forget(ctx, c); err != nil {
			return fmt.Errorf("storage: dropping the index row for %s: %w", c, err)
		}
		result.Deleted++
	}
	return nil
}

func (s *Sweeper) markRoot(ctx context.Context, root cid.Cid, marker Marker, result *SweepResult) error {
	marked, complete, err := s.mark(ctx, root, marker)
	if err != nil {
		return err
	}
	result.Marked += marked
	if !complete {
		result.Incomplete = append(result.Incomplete, root)
	}
	return nil
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
