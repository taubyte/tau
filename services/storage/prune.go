package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
)

// Pruning. No write removes a row: a delete is a row, and kvdb.Delete is LWW against
// a write of the same key. Each holder of a shard removes the rows no reader is owed,
// by one rule every holder computes alike from the rows themselves:
//
//	an older row, superseded at least pruneAfter ago by the row after it, whose
//	bucket does not keep it, or expired at least pruneAfter ago;
//	a name's newest row, a tombstone written or a version expired at least
//	pruneAfter ago, once every older row of the name goes too.
//
// A row that old is never written again (a retried append is the same row, and comes
// within holdWait), so a removal races no write. A byte holder that had not found a
// removed version superseded finds it absent, and collects it as abandoned.

// pruneAfter is how long a row outlives the moment no reader is owed it: time for the
// byte holders to find it superseded first.
func (s *server) pruneAfter() time.Duration { return 2 * s.cfg.GraceGC }

// pruneIfDue prunes every shard this node holds open, once every resweep.
func (s *server) pruneIfDue(ctx context.Context) error {
	s.collectMu.Lock()
	defer s.collectMu.Unlock()
	now := s.now()
	if !s.pruned.IsZero() && now.Sub(s.pruned) < resweep {
		return nil
	}
	s.pruned = now
	var errs []error
	for _, shard := range s.shards.held() {
		store, open := s.shards.storeOf(shard)
		if !open {
			continue
		}
		if _, err := (index{store: store, at: now}).Prune(ctx, s.pruneAfter()); err != nil {
			errs = append(errs, fmt.Errorf("shard %d: %w", shard, err))
		}
	}
	return errors.Join(errs...)
}

// pruned is one version row and its key, as a prune reads it.
type pruned struct {
	key string
	v   Version
}

// Prune removes this copy's rows no reader is owed at i.at, and says how many.
func (i index) Prune(ctx context.Context, after time.Duration) (int, error) {
	if i.at.IsZero() {
		return 0, errors.New("storage: a prune needs the instant it prunes at")
	}
	cut := uint64(i.at.Add(-after).UnixMilli())
	res, err := i.store.Query(ctx, query.Query{Prefix: bucketsRoot, Orders: []query.Order{query.OrderByKey{}}})
	if err != nil {
		return 0, err
	}
	defer res.Close()

	var doomed []string
	var name []pruned
	for {
		e, ok := res.NextSync()
		if !ok {
			break
		}
		if e.Error != nil {
			return 0, e.Error
		}
		if _, err := ParseVersionKey(e.Key); err != nil {
			continue
		}
		v, err := parseVersion(e.Key, e.Value)
		if err != nil {
			return 0, err
		}
		if len(name) > 0 && objectOf(name[0].key) != objectOf(e.Key) {
			doomed = append(doomed, prunable(name, cut)...)
			name = name[:0]
		}
		name = append(name, pruned{key: e.Key, v: v})
	}
	doomed = append(doomed, prunable(name, cut)...)

	for _, k := range doomed {
		if err := i.store.Delete(ctx, ds.RawKey(k)); err != nil {
			return 0, err
		}
	}
	return len(doomed), nil
}

// prunable is the keys of one name's rows, newest first, that go at cut.
func prunable(rows []pruned, cut uint64) []string {
	var out []string
	older := true
	for j := len(rows) - 1; j >= 1; j-- {
		r, newer := rows[j], rows[j-1]
		superseded, _ := UnpackStamp(newer.v.Stamp)
		if (!r.v.Record.Kept && superseded <= cut) || expiredBy(r.v.Record, cut) {
			out = append(out, r.key)
			continue
		}
		older = false
	}
	if len(rows) == 0 || !older {
		return out
	}
	newest := rows[0]
	written, _ := UnpackStamp(newest.v.Stamp)
	if (newest.v.Tombstone() && written <= cut) || expiredBy(newest.v.Record, cut) {
		out = append(out, newest.key)
	}
	return out
}

func expiredBy(r VersionRecord, cut uint64) bool {
	return r.ExpiresMS != 0 && r.ExpiresMS <= cut
}

// objectOf is the object a version row belongs to: its key less the stamp and writer.
func objectOf(key string) string {
	key = key[:strings.LastIndex(key, "/")]
	return key[:strings.LastIndex(key, "/")]
}
