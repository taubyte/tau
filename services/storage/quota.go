package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	ds "github.com/ipfs/go-datastore"
)

// The accounting rows add up to a convergent approximate ceiling, not a hard quota.
// A counter is legal here because each row is keyed by its writer, so no key has two
// writers; usage is sum(added) - sum(removed) over every writer's rows, computed on
// read. Two coordinators can cross the ceiling at once, bounded by (concurrent
// coordinators) x (largest in-flight object). A hard ceiling needs a fence, which is
// not built. Admission runs on the holder. `size` is per instance.

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

// Checkpoint records the usage the sweep recomputed, so a new holder starts from it
// rather than a full scan. A stale checkpoint still means walking the rows above it,
// so the ceiling is softest right after a node fails; frequency trades that against
// write amplification.
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

// Admit is the ceiling check, run on the holder. It returns the usage, so a refusal
// can report the current overshoot bound.
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
