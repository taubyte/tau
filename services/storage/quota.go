package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	ds "github.com/ipfs/go-datastore"
)

// quota.go is the accounting rows §4 defines, and the honest name for what
// they add up to: A CONVERGENT APPROXIMATE CEILING WITH A STATED OVERSHOOT
// BOUND, not a hard quota.
//
// ── WHY A COUNTER IS ALLOWED TO EXIST HERE AT ALL ────────────────────────
//
// AGENTS.md §1 kills read-modify-write on a replicated key, and a usage
// counter is exactly that. It is legal here for one reason and only that one:
// each row is keyed by its WRITER, so /u/<writer>/added has exactly one writer
// in the whole cloud and there is no key two nodes can write differently. Used
// is a SUM over those rows, computed on read — the same shape as the version
// list, one level down.
//
//	sum(added) - sum(removed)   over every writer's rows
//
// Concurrent writers each add to their own row, so nothing is lost and no node
// has to see another's row to write its own.
//
// ── THE OVERSHOOT, BOUNDED AND STATED ────────────────────────────────────
//
// A ceiling checked against a value that converges is a ceiling two
// coordinators can cross at once. The bound is:
//
//	(concurrent coordinators) x (largest in-flight object)
//
// and that is the whole story: it cannot drift further, because every
// coordinator's own row is authoritative for its own writes and the sum
// converges once their deltas merge.
//
// A HARD CEILING NEEDS A FENCE AND THE FENCE IS REFUSED. The leaderless shape
// that would give overshoot zero is budget sub-leasing — each of R holders owns
// 1/R of the quota and hands out sub-leases — and it is named rather than built
// (brief §11). Saying "approximately 1GB" in doc.go is better than a number
// that is wrong under exactly the conditions that make it matter.
//
// ── ADMISSION IS CHECKED ON THE HOLDER ───────────────────────────────────
//
// Not on the coordinator, which may be any node and holds no rows. v1's hole
// is worth naming because it is not a subtle one: its admitWrite is
// `return nil` in BOTH builds, so its over-capacity code can never be
// returned by anything.
//
// ── AND size IS PER INSTANCE ─────────────────────────────────────────────
//
// The vocabulary parks what `size` means when a bucket holds many instances
// (decisions-answered.md §3a), and it is the question a quota cannot be
// enforced without. PER INSTANCE is free — the holder checks a local sum and
// coordinates with nobody. TOTAL ACROSS INSTANCES is one counter shared by
// every instance's holder, which is a contended value: precisely what
// consensus is for, on the kind this design just argued out of consensus.
// THIS IMPLEMENTS PER INSTANCE and doc.go says so as a finding rather than
// leaving it to be discovered from the code.

// Usage is a bucket instance's accounting, summed over every writer.
type Usage struct {
	// Added and Removed are the raw sums. Both are monotonically
	// non-decreasing, which is what makes the difference convergent rather
	// than a value two nodes can drive in opposite directions.
	Added   uint64
	Removed uint64

	// Writers is how many writers contributed, which is the multiplier in the
	// overshoot bound above. It is reported rather than inferred so an
	// operator reading a ceiling can see how loose it currently is.
	Writers int
}

// Used is the current figure: bytes this instance holds. It saturates at zero
// rather than underflowing — a removed sum ahead of an added sum is a partial
// merge, which is a moment in time and not a negative object.
func (u Usage) Used() uint64 {
	if u.Removed >= u.Added {
		return 0
	}
	return u.Added - u.Removed
}

// Add records bytes this node wrote into an instance. It is a
// read-modify-write on THIS writer's own row, which no other node writes.
func (i index) Add(ctx context.Context, a Address, writer string, bytes uint64) error {
	if err := i.bump(ctx, a, writer, usageAdded, bytes); err != nil {
		return err
	}
	i.record(ctx, a, writer, OpAdd, bytes)
	return nil
}

// Remove records bytes this node released from an instance.
func (i index) Remove(ctx context.Context, a Address, writer string, bytes uint64) error {
	if err := i.bump(ctx, a, writer, usageRemoved, bytes); err != nil {
		return err
	}
	i.record(ctx, a, writer, OpRemove, bytes)
	return nil
}

// Checkpoint records the truth the sweep recomputed from live version sizes,
// so a new holder taking over cold has somewhere to start from other than a
// full scan.
//
// THE UNBOUNDED SCAN THIS DOES NOT QUITE CLOSE, said out loud: if the newest
// checkpoint is stale, a new holder still walks the rows above it, admitting
// meanwhile against the last figure plus a margin. So the ceiling is SOFTEST
// EXACTLY WHEN A NODE HAS JUST FAILED. Checkpoint frequency is the tunable
// that trades that against write amplification, and nobody has chosen it —
// choosing it badly reproduces v1's O(objects) cost on a different path.
func (i index) Checkpoint(ctx context.Context, a Address, writer string, used uint64) error {
	key, err := a.usageKey(writer, usageCheckpoint)
	if err != nil {
		return err
	}
	return i.put(ctx, key, encodeMS(used))
}

func (i index) bump(ctx context.Context, a Address, writer, kind string, delta uint64) error {
	key, err := a.usageKey(writer, kind)
	if err != nil {
		return err
	}
	current, err := i.counter(ctx, key)
	if err != nil {
		return err
	}
	if current+delta < current {
		return fmt.Errorf("storage: %s for %s overflows a uint64", kind, writer)
	}
	return i.put(ctx, key, encodeMS(current+delta))
}

func (i index) counter(ctx context.Context, key string) (uint64, error) {
	raw, err := i.store.Get(ctx, ds.RawKey(key))
	if errors.Is(err, ds.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return decodeMS(raw)
}

// Usage sums every writer's rows for one instance.
func (i index) Usage(ctx context.Context, a Address) (Usage, error) {
	prefix, err := a.usagePrefix()
	if err != nil {
		return Usage{}, err
	}
	rows, err := i.all(ctx, prefix, 0)
	if err != nil {
		return Usage{}, err
	}

	var u Usage
	writers := map[string]struct{}{}
	for _, r := range rows {
		rest, ok := strings.CutPrefix(r.key, prefix+"/")
		if !ok {
			return Usage{}, fmt.Errorf("%w: %q is not a usage row", ErrNotThisServicesRow, r.key)
		}
		parts := strings.Split(rest, "/")
		if len(parts) != 2 {
			return Usage{}, fmt.Errorf("%w: usage row %q has %d parts, not 2", ErrNotThisServicesRow, r.key, len(parts))
		}
		v, err := decodeMS(r.value)
		if err != nil {
			return Usage{}, err
		}
		writers[parts[0]] = struct{}{}
		switch parts[1] {
		case usageAdded:
			u.Added += v
		case usageRemoved:
			u.Removed += v
		case usageCheckpoint:
			// The checkpoint is the sweep's recomputed truth, not a term in
			// the sum. Adding it would double-count everything it summarises.
		default:
			return Usage{}, fmt.Errorf("%w: usage row kind %q", ErrNotThisServicesRow, parts[1])
		}
	}
	u.Writers = len(writers)
	return u, nil
}

// Admit is the ceiling check, and it runs ON THE HOLDER. It returns the
// overshoot bound alongside its answer so a caller reporting a refusal can
// report an honest one.
func (i index) Admit(ctx context.Context, a Address, capacity, incoming uint64) (bool, Usage, error) {
	u, err := i.Usage(ctx, a)
	if err != nil {
		return false, Usage{}, err
	}
	if capacity == 0 {
		// A capacity of zero is "no ceiling", which is the vocabulary's own
		// convention for instances.limit: the unbounded answer is a sentence
		// somebody wrote rather than a field they never reached.
		return true, u, nil
	}
	if u.Used()+incoming > capacity {
		// Admit runs on the holder and knows nothing about which node is
		// asking, so the refusal is attributed to the instance rather than to
		// a writer. A sink totalling refusals wants the instance anyway.
		i.record(ctx, a, "", OpRefuse, incoming)
		return false, u, nil
	}
	return true, u, nil
}
