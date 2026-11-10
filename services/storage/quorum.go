package storage

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// A READ HEARS AS MANY COPIES OF THE INDEX AS A WRITE LEAVES UNCONFIRMED, plus one. A
// write is confirmed on need of an instance's index holders, so a read that hears
// holders - need + 1 of them hears one that confirmed it, and answers with the newest
// it heard: a cloud of one node hears 1 of 1, of two 1 of 2, since a write there is
// on both, and of three or more, at the default, 2 of 3. This node's own copy answers
// first and costs no call.
//
// Each copy answers with the newest rows it holds, tombstones and expired versions
// included, and the reader hides those itself: a copy behind, still holding a version
// another deleted, would otherwise bring it back. A copy holding nothing of the
// instance (one placement just moved there) is no answer. When fewer copies than a
// read needs answer and some could not be asked, the read fails rather than answer
// from what may be behind; when every copy holds nothing, the instance holds nothing.

// readQuorum is the holders of a's index, this node first, and how many must answer.
func (s *server) readQuorum(ctx context.Context, a Address) ([]string, int, error) {
	shard, err := shardOf(a.Bucket())
	if err != nil {
		return nil, 0, err
	}
	replicas, barrier, err := s.promise(ctx)
	if err != nil {
		return nil, 0, err
	}
	holders := indexHolders(shard, s.placement(), replicas)
	if i := slices.Index(holders, s.self); i > 0 {
		holders[0], holders[i] = holders[i], holders[0]
	}
	need := max(1, min(barrier, len(holders)))
	return holders, len(holders) - need + 1, nil
}

// reply is one copy's answer.
type reply[T any] struct {
	v   T
	err error
}

// fromCopies hears q copies of a's index: local reads this node's own, remote asks a
// holder for its own. It asks q at a time, and another holder for each that cannot
// answer.
func fromCopies[T any](ctx context.Context, s *server, a Address, local func(index) (T, error), remote func(storagev2Client) (T, error)) ([]T, error) {
	holders, q, err := s.readQuorum(ctx, a)
	if err != nil {
		return nil, err
	}
	if len(holders) == 0 {
		return nil, fmt.Errorf("storage: placement names no holder of %s's index", a)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	replies := make(chan reply[T], len(holders))
	next := 0
	ask := func() {
		h := holders[next]
		next++
		go func() {
			v, err := answerFrom(ctx, s, h, a, local, remote)
			replies <- reply[T]{v: v, err: err}
		}()
	}
	for next < q {
		ask()
	}
	var out []T
	var errs []error
	for pending := q; pending > 0 && len(out) < q; pending-- {
		r := <-replies
		switch {
		case r.err == nil:
			out = append(out, r.v)
			continue
		case !errors.Is(r.err, ErrEmptyCopy):
			errs = append(errs, r.err)
		}
		if next < len(holders) {
			ask()
			pending++
		}
	}
	switch {
	case len(out) >= q || (len(out) > 0 && len(errs) == 0):
		return out, nil
	case len(errs) == 0:
		return nil, fmt.Errorf("%w: no copy of %s's index holds anything of it", ErrNotFound, a)
	}
	return nil, fmt.Errorf("storage: %d of the %d copies of %s's index a read needs answered: %w", len(out), q, a, errors.Join(errs...))
}

// answerFrom is holder's answer: this node's own copy, or another's asked.
func answerFrom[T any](ctx context.Context, s *server, holder string, a Address, local func(index) (T, error), remote func(storagev2Client) (T, error)) (T, error) {
	var zero T
	if holder == s.self {
		var out T
		err := s.copyHere(ctx, a, func(i index) error {
			v, err := local(i)
			out = v
			return err
		})
		return out, err
	}
	svc, err := s.peer(holder)
	if err != nil {
		return zero, err
	}
	v, err := remote(svc)
	return v, unwire(err)
}

// copyHere reads this node's own copy of a's index: ErrEmptyCopy when it holds
// nothing of the instance.
func (s *server) copyHere(ctx context.Context, a Address, read func(index) error) error {
	i, release, ok, err := s.indexOf(ctx, a)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("storage: this node does not hold %s's index", a)
	}
	defer release()
	held, err := i.holdsAny(ctx, a)
	if err != nil {
		return err
	}
	if !held {
		return fmt.Errorf("%w: %s", ErrEmptyCopy, a)
	}
	return read(i)
}

// newer says v orders before w: what byte order over the keys says.
func newer(v, w Version) bool {
	if v.Stamp != w.Stamp {
		return v.Stamp > w.Stamp
	}
	return v.WriterSegment < w.WriterSegment
}

// gone says a version is not there to read at at: a tombstone, or expired.
func gone(v Version, at time.Time) bool {
	return v.Tombstone() || v.Record.expired(at)
}

// copyRow is a copy's newest row of a name, ok false when it holds none.
type copyRow struct {
	v  Version
	ok bool
}

// newestOf is the newest of the copies' rows; ok false when none holds one.
func newestOf(rows []copyRow) (Version, bool) {
	var out Version
	found := false
	for _, r := range rows {
		if r.ok && (!found || newer(r.v, out)) {
			out, found = r.v, true
		}
	}
	return out, found
}

// versionID is what tells one version of a name from another.
type versionID struct {
	stamp  uint64
	writer string
}

// unionOf is every version the copies hold of a name, newest first, at most limit.
func unionOf(lists [][]Version, limit int) []Version {
	var out []Version
	seen := map[versionID]bool{}
	for _, vs := range lists {
		for _, v := range vs {
			if id := (versionID{v.Stamp, v.WriterSegment}); !seen[id] {
				seen[id] = true
				out = append(out, v)
			}
		}
	}
	out = Order(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// mergePages merges the copies' pages of newest rows per name into the live names,
// in order. A page a copy cut at limit is complete only up to its last name, so names
// past the first such cut wait for the next page, which resumes at bound; more says
// there is one.
func mergePages(pages [][]Version, limit int, at time.Time) (live []Version, bound string, more bool) {
	for _, p := range pages {
		if limit > 0 && len(p) == limit {
			if last := p[len(p)-1].Name; !more || last < bound {
				bound = last
			}
			more = true
		}
	}
	newest := map[string]Version{}
	for _, p := range pages {
		for _, v := range p {
			if more && v.Name > bound {
				continue
			}
			if w, ok := newest[v.Name]; !ok || newer(v, w) {
				newest[v.Name] = v
			}
		}
	}
	for _, v := range newest {
		if !gone(v, at) {
			live = append(live, v)
		}
	}
	slices.SortFunc(live, func(x, y Version) int { return strings.Compare(x.Name, y.Name) })
	return live, bound, more
}
