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

// log.go is the `sync` kind at `type: log` — append and read forward, with
// nothing rewriting what is already there.
//
// # RAFT DECIDES THE ORDER; THE SWARM CARRIES THE RECORDS
//
// The obvious implementation puts the records in raft, and it is the wrong one.
// pkg/raft's state machine is an in-memory map whose Snapshot() exports the
// WHOLE of it, so a log kept there would hold every record in every node's heap
// until retention dropped it, and re-serialise the lot on every snapshot. A log
// is the one type whose size is unbounded by design; that is the shape raft is
// worst at.
//
// So raft holds one key per log — the head — plus one per LIVE POSITION, and
// the records go in the replicated store. What raft is used for is the only
// thing a raft log actually needs it for: deciding who assigns the next
// position.
//
// The position map is still raft's, which is the cost this service has to bound
// rather than hide: between the floor and the head there is one raft key per
// record, so a log with no retention is an unbounded raft KEYSPACE and not only
// an unbounded disk. retention.go is the half of that answer this service owns;
// the other half is the vocabulary's "absent means forever", which is an
// author's promise and stays.
//
// AND THE LIFECYCLE TICK PAYS FOR THAT KEYSPACE, which belongs in this
// paragraph rather than in a profile somebody takes later. [logsIn] scans every
// key under `log/` to find one head per log, so it reads the position map of
// every log this node leads; [records.sweep] then walks a bounded log
// newest-first and re-reads every record it KEEPS — a raft Get, a store Get and
// an unmarshal apiece — because a size budget is a running total and an age
// budget only stops at the first record past the cutoff, which on a log inside
// its budget is all of them. Both are O(kept) per pass and neither is
// O(dropped). What would fix the second is a per-log summary raft holds beside
// the head — the bytes held and the oldest timestamp, moved in the same entry —
// and that is a third key per log, so it waits until a log big enough to want
// it exists.
//
// # The record keys are immutable, which is what makes them safe in a CRDT
//
// A record key is written ONCE, by whichever node holds the leadership at that
// moment, with a value that never changes afterwards. Concurrent appends cannot
// collide because the position they write at came from raft.
//
// # A hole is a record on its way, not a missing one
//
// The head advances after the record lands, so a crash between the two leaves a
// record nobody can see rather than a position with nothing at it. A reader that
// finds nothing at a position below the head has out-run replication and stops
// there: the next call answers it.

// records is the log half of this service: the raft cluster that orders
// appends, and the store that holds what was appended.
type records struct {
	service *Service

	mu sync.Mutex
	db kvdb.KVDB

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

	// THE RECORD LANDS BEFORE THE HEAD MOVES. The other order publishes a
	// position whose record has not been written yet, which a reader cannot tell
	// from one it will never get.
	//
	// AND IT LANDS UNDER ITS OWN DIGEST, not under the position, because the
	// store it lands in is a CRDT and the position is a name two leaders can
	// both hold. A leader that read this head, stalled and lost the election
	// still completes its write: under a digest it writes a key only its own
	// bytes produce, its raft commit below is refused, and the record it left
	// behind is one nothing points at — which is why both refusal arms take it
	// back rather than leaving it. See [records.orphaned].
	raw, err := json.Marshal(logRecord{At: r.service.clock().Unix(), Data: data})
	if err != nil {
		return "", err
	}
	digest := digestOf(raw)
	if err := db.Put(ctx, recordKey(prefix, head, digest), raw); err != nil {
		return "", fmt.Errorf("primitives: writing the record: %w", err)
	}

	// THE HEAD IS READ AGAIN, and a head that moved means somebody else was the
	// leader for this position. Raft only elects a node whose log already holds
	// what was committed, so a leader that lost and regained the term reads the
	// newer head here and stands down rather than publishing over the record
	// that won. What this does not cover is the sliver where such a node holds
	// that entry and has not applied it yet; closing it needs a conditional
	// write, which pkg/raft does not have.
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
			return "", r.service.notLeading(cluster)
		}
		return "", fmt.Errorf("primitives: advancing the log's head: %w", err)
	}

	// NOTHING SWEEPS HERE, and that is the lifecycle fix this service exists to
	// make. node/log.go ran retention on the appender, which is the one node
	// that may write and the one moment the log is known to have changed — and
	// therefore never for a log that went quiet. An author who wrote `age: 24h`
	// on a log that stopped receiving got forever. retention.go runs it on a
	// tick instead, over every log this node leads.
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

	// RETENTION TAKES THE FRONT, so position zero is not where a log starts —
	// the floor is. Reading from zero would find the hole retention left and
	// stop there, answering nothing for a log that is full of records.
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
			// A STORE THAT CANNOT ANSWER IS NOT A LOG WITH NOTHING MORE IN IT.
			// Both end the page and only one of them is a fact: this call's
			// contract is that a short page means caught up, so a node whose
			// store is sick would tell every reader it had reached the end, and
			// the records they never saw are records they never ask for again.
			if !missing(err) {
				return nil, fmt.Errorf("primitives: reading the record at %d: %w", at, err)
			}
			// A POSITION WITH NOTHING AT IT ENDS THE PAGE, and does not skip
			// ahead. Below the head it is a record still replicating; at the
			// head it is the end of the log. Either way the next call answers
			// it, and stepping over it would hand a reader records out of order
			// with a hole it can never come back to.
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
// dropped.
//
// IT IS CALLED BY THE LIFECYCLE LOOP AND BY NOTHING ELSE. Running it on the
// append path put it on the one node that may write and the one moment the log
// changed, which sounds like the cheapest place and is the one place that
// cannot serve an age budget: a log stops receiving exactly when its oldest
// records start expiring.
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
			// A FAILING STORE IS NOT A LOG INSIDE ITS BUDGET, and the two
			// answers are the same shape: retention that quietly stopped
			// running looks exactly like retention with nothing to do. Reported
			// here it reaches the pass, which says so once a tick.
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

		// THE PAYLOAD AND NOT THE ENVELOPE. The record is json with a []byte
		// field, so on disk the author's bytes are base64 — measuring the raw
		// form enforced `size` against roughly 1.37x what the author wrote, and
		// an author who asked for 1MB got 750KB with nothing saying so.
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

// drop removes every position from `from` down to the floor, and publishes the
// new floor.
//
// THE POSITIONS ARE RETIRED BEFORE THE BYTES GO, which is the mirror of the
// order [records.append] argues for and the same argument. An append publishes
// a position only once its record is there, because a position with nothing at
// it cannot be told from a record still on its way; retention has to reach that
// same invariant from the other end. Deleting the bytes first and then losing
// the entry that retires the positions — a lost election, or one enormous entry
// that out-ran applyTimeout — leaves every SURVIVING record behind a position
// whose bytes are gone, and [records.read] stops at the first of those: the log
// answers empty, on every node, for as long as it exists, and no later pass
// undoes it because the sweep stops at the same hole.
//
// AND THE FLOOR MOVES IN THAT SAME ENTRY, not in a write after it. A floor left
// behind the positions it names points every reader at a hole, which is the
// identical failure reached by the other route.
func (r *records) drop(ctx context.Context, cluster raft.Cluster, db kvdb.KVDB, prefix string, from, floor uint64) (int, error) {
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

	// NOW THE BYTES, AND A FAILURE HERE IS NOT A FAILED RETENTION. What these
	// keys hold is already unreachable — a read resolves every position through
	// raft, which no longer names them, and refuses a cursor below the floor —
	// so what a refused delete leaves behind is garbage and not a hole. It is
	// said out loud and not retried: the half that had to be one entry is
	// already committed.
	//
	// WHAT THIS DOES NOT DO IS RECLAIM THE DISK. kvdb grows on a Delete — a
	// delete is a tombstone plus a DAG node — and nothing on this path calls
	// Compact(). Retention frees the read path and the raft keyspace; the bytes
	// go when the store is compacted, which is nobody's job yet.
	for at, digest := range gone {
		if err := db.Delete(ctx, recordKey(prefix, at, digest)); err != nil {
			logger.Warnf("primitives: %s kept the bytes of the record at %d: %s", prefix, at, err)
		}
	}
	return len(gone), nil
}

// orphaned removes a record whose position was never published.
//
// THE NODE THAT WROTE IT IS THE ONLY ONE THAT CAN. A record lands under its own
// digest so that a leader which stalled and lost the term cannot overwrite the
// record that won — and what that leaves when its own commit is then refused is
// a key no position names: no read reaches it, and retention deletes the digest
// RAFT named rather than everything at a position, so nothing else would ever
// come back for it. This node still holds the key it just wrote, so it takes it
// back here.
//
// WHAT IS LEFT IS THE CASE NOBODY CAN REACH: a process that dies between the
// two writes leaves its record behind. That is a leak bounded by crashes
// mid-append, and it goes when something compacts the store — the same "nobody's
// job yet" [records.drop] names.
func (r *records) orphaned(ctx context.Context, db kvdb.KVDB, prefix string, at uint64, digest string) {
	if err := db.Delete(ctx, recordKey(prefix, at, digest)); err != nil {
		logger.Warnf("primitives: %s left the record it could not publish at %d: %s", prefix, at, err)
	}
}

// missing tells a record this store does not hold from a store that cannot
// answer at all. core/kvdb's contract is the datastore's — a Get for a key it
// does not hold answers ErrNotFound — and everything above reads the two
// differently: a miss is a hole, which is a record on its way, and a failure is
// a node whose "there is nothing more" nobody should believe.
func missing(err error) bool { return errors.Is(err, ds.ErrNotFound) }

// logsIn is every log this node's namespace holds a head for.
//
// IT WALKS THE KEYSPACE AND NOT A LIST SOMEBODY MAINTAINS, which is why
// [Spec.Prefix] has to parse back: a list would be a second thing to keep in
// step with the heads, and a log missing from it would be one retention never
// visits.
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
func (r *records) ready() (raft.Cluster, kvdb.KVDB, error) {
	cluster, err := r.service.consensus()
	if err != nil {
		return nil, nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.db == nil {
		db, err := r.service.stores.New(logger, recordStore, rebroadcastSeconds)
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

// recordKey is where one position's record sits. THE POSITION IS ZERO-PADDED so
// the store's own ordering is the log's ordering, which is what lets a range
// read walk forward without sorting.
//
// THE DIGEST IS THE LAST SEGMENT because the store is a CRDT: a key that named
// only the position could be written by two leaders with different bytes, and
// the later write would win over a record already acknowledged. With the digest
// there, two writers of different bytes write two keys, and only the one raft
// named at this position is ever read back.
func recordKey(prefix string, at uint64, digest string) string {
	return prefix + "/records/" + fmt.Sprintf("%020d", at) + "/" + digest
}

// digestOf names a record by its bytes.
func digestOf(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// A cursor is opaque on purpose — see wit/deps/taubyte-log. Base64 so nobody
// assembles one by hand and discovers it is a number.
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
