package primitives

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	ds "github.com/ipfs/go-datastore"

	"github.com/taubyte/tau/core/kvdb"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/raft"
)

// The `sync` log: append and read forward, nothing rewritten. Raft decides the order;
// the replicated store carries the records. pkg/raft's state machine is an in-memory
// map snapshotted whole, so raft holds only each log's head and one key per live
// position, and a log with no retention is an unbounded raft keyspace (retention.go
// bounds what it can). A lifecycle pass is O(kept): logsIn scans all positions, and
// sweep re-reads every kept record, until a per-log summary beside the head is worth
// a third key.
//
// Record keys are written once with an immutable value at a raft-assigned position,
// so they are safe in a CRDT. The head advances after the record lands, so a hole
// below the head is a record still replicating; a reader stops there.

// records is the log half of this service: the raft cluster that orders
// appends, and the store that holds what was appended.
type records struct {
	service *server

	mu sync.Mutex
	db kvdb.Instance

	// writing serialises appends per log WITHIN this process. Raft gives one
	// leader; this gives one appender on it, which is what makes read-then-write
	// on the head safe — pkg/raft has no conditional write, so the lock is the
	// whole of the argument.
	writing map[string]*sync.Mutex
}

// logRecord is what one position holds, on disk.
type logRecord struct {
	At   int64  `json:"at"`
	Data []byte `json:"data"`
}

// append adds one record and answers the cursor it landed at.
func (r *records) append(ctx context.Context, spec Spec, data []byte) (string, error) {
	if len(data) > MaxRecord {
		return "", tooLarge("that record", len(data), MaxRecord)
	}

	cluster, db, err := r.ready()
	if err != nil {
		return "", err
	}

	prefix := spec.Prefix()

	// ONE APPENDER, ON THE ONE LEADER. Raft refuses a write off the leader, so
	// this only has to stop two goroutines here from reading one head.
	lock := r.lockFor(prefix)
	lock.Lock()
	defer lock.Unlock()

	if err := r.service.writable(cluster); err != nil {
		return "", err
	}
	// THE HEAD IS READ AFTER A BARRIER, so what this node believes is what the
	// cluster has committed. Without it a leader that just won an election could
	// assign a position another leader already used.
	if err := cluster.Barrier(applyTimeout); err != nil {
		return "", fmt.Errorf("primitives: reading the log's head: %w", err)
	}

	head, err := headOf(cluster, prefix)
	if err != nil {
		return "", err
	}

	// The record lands before the head moves, so no published position lacks its record.
	// It lands under its own digest, not the position: a leader that stalled and lost the
	// election still writes, but to a key only its bytes produce, which nothing points at
	// once its commit is refused; both refusal arms remove it (records.orphaned).
	raw, err := json.Marshal(logRecord{At: r.service.clock().Unix(), Data: data})
	if err != nil {
		return "", err
	}
	digest := digestOf(raw)
	if err := db.Put(ctx, recordKey(prefix, head, digest), raw); err != nil {
		return "", fmt.Errorf("primitives: writing the record: %w", err)
	}

	// The head is re-read: if it moved, another leader held this position and this node
	// stands down. The sliver where such a node holds but has not applied that entry needs
	// a conditional write pkg/raft lacks.
	if now, err := headOf(cluster, prefix); err != nil || now != head {
		r.orphaned(ctx, db, prefix, head, digest)
		return "", fmt.Errorf("%w: position %d was taken while this record was being written",
			ErrNotLeading, head)
	}

	// WHICH RECORD OCCUPIES THE POSITION IS RAFT'S TO SAY, and it says it in the
	// same entry that moves the head — so a position is never published without
	// its record, and never left holding one the head has passed.
	if err := cluster.Batch([]raft.BatchOp{
		{Set: &raft.SetCommand{Key: atKey(prefix, head), Value: []byte(digest)}},
		{Set: &raft.SetCommand{Key: headKey(prefix), Value: []byte(strconv.FormatUint(head+1, 10))}},
	}, applyTimeout); err != nil {
		r.orphaned(ctx, db, prefix, head, digest)
		if errors.Is(err, raft.ErrNotLeader) {
			return "", fmt.Errorf("%w: advancing the log's head", ErrUncertain)
		}
		return "", fmt.Errorf("primitives: advancing the log's head: %w", err)
	}

	// No sweep on append: retention runs on a tick, so a log that went quiet still expires
	// (retention.go).
	return encodeCursor(head), nil
}

// read answers the records after a cursor, oldest first.
func (r *records) read(ctx context.Context, spec Spec, after string, limit uint32) ([]wasmtier.Record, error) {
	cluster, db, err := r.ready()
	if err != nil {
		return nil, err
	}

	prefix := spec.Prefix()
	from, err := decodeCursor(after)
	if err != nil {
		return nil, err
	}

	// Retention moves the floor, so reading starts there rather than at zero.
	floor, err := floorOf(cluster, prefix)
	if err != nil {
		return nil, err
	}
	switch {
	case after == "":
		from = floor
	case from < floor:
		// The records this caller wants are gone. Its own arm rather than an
		// empty answer, because "you are behind the log" and "you are caught up"
		// are different facts and a reader acts on them differently.
		return nil, fmt.Errorf("%w: those records are past this log's retention", ErrBadCursor)
	}

	page := int(limit)
	if page <= 0 || page > maxPage {
		page = maxPage
	}

	out := make([]wasmtier.Record, 0, page)
	for at := from; len(out) < page; at++ {
		// THE POSITION IS RESOLVED THROUGH RAFT, which is what makes a record
		// the one that was committed rather than the one that arrived last.
		digest, held := cluster.Get(atKey(prefix, at))
		if !held {
			break
		}
		raw, err := db.Get(ctx, recordKey(prefix, at, string(digest)))
		if err != nil {
			// A store that cannot answer is not a log with nothing more: a short page means caught
			// up, so the error is returned rather than ending the page.
			if !missing(err) {
				return nil, fmt.Errorf("primitives: reading the record at %d: %w", at, err)
			}
			// A position with nothing at it ends the page rather than skipping ahead; the next
			// call answers it.
			break
		}

		var record logRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return nil, fmt.Errorf("primitives: the record at %d is not one: %w", at, err)
		}
		out = append(out, wasmtier.Record{Cursor: encodeCursor(at), At: record.At, Data: record.Data})
	}
	return out, nil
}

// sweep drops what retention no longer keeps, and answers how many positions it
// dropped. Called only by the lifecycle loop, since an age budget matters most for a
// log that stopped receiving.
func (r *records) sweep(ctx context.Context, spec Spec, keep retention) (int, error) {
	if !keep.bounded() {
		// ABSENT MEANS FOREVER, which is the vocabulary's own sentence and the
		// safe direction for a ledger.
		return 0, nil
	}

	cluster, db, err := r.ready()
	if err != nil {
		return 0, err
	}
	prefix := spec.Prefix()

	head, err := headOf(cluster, prefix)
	if err != nil || head == 0 {
		return 0, err
	}
	// WHERE IT STOPS DIGGING. Below the floor there is nothing left to drop, and
	// walking to zero every pass would re-delete the whole prefix each time —
	// which, the mapping being raft's, is a raft write per position.
	floor, err := floorOf(cluster, prefix)
	if err != nil {
		return 0, err
	}

	// THE WALK IS NEWEST FIRST, so what is kept is what a reader is most likely
	// to want and the scan stops at the first record beyond the budget instead
	// of reading the whole log.
	var held int64
	cutoff := r.service.clock().Add(-keep.age)
	for at := head - 1; ; at-- {
		digest, ok := cluster.Get(atKey(prefix, at))
		if !ok {
			break
		}
		raw, err := db.Get(ctx, recordKey(prefix, at, string(digest)))
		if err != nil {
			// A failing store is reported, not read as a log inside its budget.
			if !missing(err) {
				return 0, fmt.Errorf("primitives: reading the record at %d: %w", at, err)
			}
			// A record that has not replicated to this node cannot be measured,
			// and what cannot be measured is kept.
			break
		}

		var record logRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			// A RECORD THAT IS NOT ONE STOPS THE PASS RATHER THAN BEING STEPPED
			// OVER: stepping over it would drop everything below it against a
			// budget computed without it.
			return 0, fmt.Errorf("primitives: the record at %d is not one: %w", at, err)
		}

		// The payload, not the base64 JSON envelope (about 1.37x larger).
		held += int64(len(record.Data))

		beyond := (keep.age > 0 && time.Unix(record.At, 0).Before(cutoff)) ||
			(keep.size > 0 && held > keep.size)

		// THE NEWEST RECORD IS NEVER DROPPED, whatever the budget says. A log
		// whose ceiling cannot hold one record is a misconfiguration, and
		// answering it by forgetting everything — including what was just
		// appended — loses the write a caller was told succeeded.
		if beyond && at < head-1 {
			return r.drop(ctx, cluster, db, prefix, at, floor)
		}
		if at == floor || at == 0 {
			break
		}
	}
	return 0, nil
}

// drop removes every position from `from` down to the floor and publishes the new
// floor, retiring positions and moving the floor in one raft entry before deleting
// bytes. Deleting bytes first and then losing that entry would leave surviving
// records behind positions whose bytes are gone, and reads would stop there forever.
func (r *records) drop(ctx context.Context, cluster raft.Cluster, db kvdb.Instance, prefix string, from, floor uint64) (int, error) {
	gone := make(map[uint64]string, from-floor+1)
	ops := make([]raft.BatchOp, 0, from-floor+2)
	for at := from; ; at-- {
		if digest, ok := cluster.Get(atKey(prefix, at)); ok {
			gone[at] = string(digest)
			ops = append(ops, raft.BatchOp{Delete: &raft.DeleteCommand{Key: atKey(prefix, at)}})
		}
		if at == floor || at == 0 {
			break
		}
	}
	if len(ops) == 0 {
		return 0, nil
	}

	// WHERE THE LOG NOW STARTS, written where the head is written: a reader on
	// any node has to know which hole is retention and which is a record still
	// on its way.
	ops = append(ops, raft.BatchOp{
		Set: &raft.SetCommand{Key: floorKey(prefix), Value: []byte(strconv.FormatUint(from+1, 10))},
	})
	if err := cluster.Batch(ops, applyTimeout); err != nil {
		return 0, err
	}

	// The bytes are already unreachable, so a failed delete leaves garbage, not a hole,
	// and is not retried. kvdb grows on delete (a tombstone) and nothing here compacts.
	for at, digest := range gone {
		if err := db.Delete(ctx, recordKey(prefix, at, digest)); err != nil {
			logger.Warnf("primitives: %s kept the bytes of the record at %d: %s", prefix, at, err)
		}
	}
	return len(gone), nil
}

// orphaned removes a record whose position was never published: only the writer
// knows its key. A process dying between the two writes still leaks one record, until
// something compacts the store.
func (r *records) orphaned(ctx context.Context, db kvdb.Instance, prefix string, at uint64, digest string) {
	if err := db.Delete(ctx, recordKey(prefix, at, digest)); err != nil {
		logger.Warnf("primitives: %s left the record it could not publish at %d: %s", prefix, at, err)
	}
}

// missing tells a record the store does not hold (ds.ErrNotFound, a hole) from a
// store that cannot answer.
func missing(err error) bool { return errors.Is(err, ds.ErrNotFound) }

// logsIn is every log this namespace holds a head for, walking the keyspace rather
// than a maintained list, which is why Spec.Prefix parses back.
func logsIn(cluster raft.Cluster) []Spec {
	keys := cluster.Keys(logType + "/")

	out := make([]Spec, 0, len(keys))
	for _, key := range keys {
		prefix, held := strings.CutSuffix(key, "/head")
		if !held {
			continue
		}
		if spec, ok := specFrom(prefix); ok && spec.Type == logType {
			out = append(out, spec)
		}
	}
	return out
}

// ready opens what a log needs, once.
func (r *records) ready() (raft.Cluster, kvdb.Instance, error) {
	cluster, err := r.service.consensus()
	if err != nil {
		return nil, nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.db == nil {
		db, err := r.service.stores(recordStore)
		if err != nil {
			return nil, nil, fmt.Errorf("primitives: opening the record store: %w", err)
		}
		r.db = db
	}
	return cluster, r.db, nil
}

func (r *records) lockFor(prefix string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.writing == nil {
		r.writing = map[string]*sync.Mutex{}
	}
	if _, held := r.writing[prefix]; !held {
		r.writing[prefix] = &sync.Mutex{}
	}
	return r.writing[prefix]
}

func (r *records) Close() error {
	r.mu.Lock()
	db := r.db
	r.db = nil
	r.mu.Unlock()

	if db != nil {
		db.Close()
	}
	return nil
}

// headOf is the next position this log will write.
func headOf(cluster raft.Cluster, prefix string) (uint64, error) {
	raw, held := cluster.Get(headKey(prefix))
	if !held {
		return 0, nil
	}
	head, err := strconv.ParseUint(string(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("primitives: the head of %s is not a position: %w", prefix, err)
	}
	return head, nil
}

// floorOf is the oldest position this log still holds. Absent means nothing has
// been dropped, so the log starts where it always did.
func floorOf(cluster raft.Cluster, prefix string) (uint64, error) {
	raw, held := cluster.Get(floorKey(prefix))
	if !held {
		return 0, nil
	}
	floor, err := strconv.ParseUint(string(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("primitives: the floor of %s is not a position: %w", prefix, err)
	}
	return floor, nil
}

func headKey(prefix string) string  { return prefix + "/head" }
func floorKey(prefix string) string { return prefix + "/floor" }

// atKey is which record occupies a position — raft's answer, not the store's.
func atKey(prefix string, at uint64) string {
	return prefix + "/at/" + fmt.Sprintf("%020d", at)
}

// recordKey is where one position's record sits: the zero-padded position so store
// order is log order, then the digest so two leaders writing different bytes write
// two keys and only raft's choice is read.
func recordKey(prefix string, at uint64, digest string) string {
	return prefix + "/records/" + fmt.Sprintf("%020d", at) + "/" + digest
}

// digestOf names a record by its bytes.
func digestOf(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// A cursor is opaque (wit/deps/taubyte-log), base64 so nobody assembles one.
func encodeCursor(at uint64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatUint(at, 10)))
}

// decodeCursor reads a cursor back. An empty one is the start of the log.
func decodeCursor(cursor string) (uint64, error) {
	if strings.TrimSpace(cursor) == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrBadCursor, cursor)
	}
	at, err := strconv.ParseUint(string(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrBadCursor, cursor)
	}
	// AFTER, NOT AT: the cursor names the record a reader already has. THE LAST
	// POSITION HAS NO AFTER — wrapping to zero would answer a crafted cursor by
	// replaying the whole log from the floor instead of refusing it.
	if at == math.MaxUint64 {
		return 0, fmt.Errorf("%w: %q is past the last position a log can hold", ErrBadCursor, cursor)
	}
	return at + 1, nil
}
