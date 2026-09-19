package node

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/raft"
)

// log.go backs `taubyte:log/records` on this node — the `sync` kind at
// `type: log`, with `consensus: raft`.
//
// # RAFT DECIDES THE ORDER; THE SWARM CARRIES THE RECORDS
//
// The obvious implementation puts the records in raft, and it is the wrong one.
// pkg/raft's state machine is an in-memory map whose Snapshot() exports the
// WHOLE of it (fsm.go), so a log kept there would hold every record in every
// node's heap until retention dropped it, and re-serialise the lot on every
// snapshot. A log is the one kind whose size is unbounded by design; that is
// the shape raft is worst at.
//
// So raft holds ONE key per log — the head — and the records go in the
// replicated store beside every other kind's bytes. What raft is used for is the
// only thing a raft log actually needs it for: deciding who assigns the next
// position.
//
// # The record keys are immutable, which is what makes them safe in a CRDT
//
// AGENTS.md's rule for every kvdb layout here is that no key may be one two
// nodes can write differently. A record key is written ONCE, by whichever node
// holds the raft leadership at that moment, with a value that never changes
// afterwards — the strongest form of that rule rather than an exception to it.
// Concurrent appends cannot collide because the position they write at came
// from raft.
//
// # A hole is a record on its way, not a missing one
//
// The head advances after the record lands, so a crash between the two leaves a
// record nobody can see rather than a position with nothing at it. A reader
// that finds nothing at a position below the head has out-run replication, and
// stops there: the next call answers it. That is the same distinction
// node/storage.go draws between "the name is here and the bytes are not" and a
// miss.

// logDatabase is the one database every log's records live in, for the reason
// cacheDatabase states: one kvdb, keys prefixed by handle.
const logDatabase = "logs"

// logDomain is the raft namespace the heads live in. It is the queues' own:
// both are "who may write next", one cluster answers it for the node, and a
// second consensus group would be a second election over the same question.
const logDomain = queueDomain

// The setting keys services/worker writes, which are the vocabulary's own
// attribute paths under the type's block.
const (
	settingLogRetentionAge  = "log/retention/age"
	settingLogRetentionSize = "log/retention/size"
	settingLogConsensus     = "log/consensus"
)

// maxLogRecord is the largest record this cloud takes, and it is the ABI's
// ceiling rather than a policy: a record crosses as one `list<u8>`, so it is
// materialised whole in the guest's memory on the way out and in the node's on
// the way in. Retention bounds the log; this bounds one entry.
const maxLogRecord = 1 << 20

// logs is the node's Logs: the raft cluster that orders appends, and the kvdb
// that holds what was appended.
type logs struct {
	node *Node

	mu sync.Mutex
	db kvdb.KVDB

	// writing serialises appends per log WITHIN this process. Raft gives one
	// leader; this gives one appender on it, which is what makes read-then-write
	// on the head safe — pkg/raft has no conditional write, so the lock is the
	// whole of the argument (see project-raft-has-no-conditional-write).
	writing map[string]*sync.Mutex
}

var _ wasmtier.Logs = (*logs)(nil)

// ErrLogConsensusUnserved is a log whose author asked for a consensus this node
// cannot keep. It is refused by name rather than served weaker than written.
var ErrLogConsensusUnserved = errors.New("node: this cloud serves raft logs and that one asks for crdt")

// Append adds one record and answers the cursor it landed at.
func (l *logs) Append(ctx context.Context, grant invoke.Grant, data []byte) (string, error) {
	if len(data) > maxLogRecord {
		return "", fmt.Errorf("%w: %d bytes, and the ceiling is %d",
			wasmtier.ErrRecordTooLarge, len(data), maxLogRecord)
	}
	if err := servableLog(grant); err != nil {
		return "", err
	}

	cluster, db, err := l.ready()
	if err != nil {
		return "", err
	}

	// ONE APPENDER, ON THE ONE LEADER. Raft refuses a write off the leader, so
	// this only has to stop two goroutines here from reading one head.
	lock := l.lockFor(grant.Handle)
	lock.Lock()
	defer lock.Unlock()

	if !cluster.IsLeader() {
		return "", fmt.Errorf("%w: appends are ordered by the node holding the log's leadership",
			wasmtier.ErrNotWritable)
	}
	// THE HEAD IS READ AFTER A BARRIER, so what this node believes is what the
	// cluster has committed. Without it a leader that just won an election
	// could assign a position another leader already used.
	if err := cluster.Barrier(applyTimeout); err != nil {
		return "", fmt.Errorf("node: reading the log's head: %w", err)
	}

	head, err := headOf(cluster, grant.Handle)
	if err != nil {
		return "", err
	}

	// THE RECORD LANDS BEFORE THE HEAD MOVES. The other order publishes a
	// position whose record has not been written yet, which a reader cannot
	// tell from one it will never get.
	//
	// AND IT LANDS UNDER ITS OWN DIGEST, not under the position, because the
	// store it lands in is a CRDT and the position is a name two leaders can
	// both hold. A leader that read this head, stalled, and lost the election
	// still completes its write: it used to land on the position a NEW leader
	// had already published, replacing bytes a caller was told were committed,
	// and no process-local lock can stop that — the two writers are two
	// processes. Under a digest the deposed leader writes a key only its own
	// bytes produce, its raft commit below is refused, and what it left is an
	// orphan nothing points at.
	record := logRecord{At: l.clock().Unix(), Data: data}
	raw, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	digest := digestOf(raw)
	if err := db.Put(ctx, recordKey(grant.Handle, head, digest), raw); err != nil {
		return "", fmt.Errorf("node: writing the record: %w", err)
	}

	// THE HEAD IS READ AGAIN, and a head that moved means somebody else was
	// the leader for this position. Raft only elects a node whose log already
	// holds what was committed, so a leader that lost and regained the term
	// reads the newer head here and stands down rather than publishing over
	// the record that won. What this does not cover is the sliver where such a
	// node holds that entry and has not applied it yet; closing that needs a
	// conditional write, which pkg/raft does not have (Set/Delete/Batch only).
	if now, err := headOf(cluster, grant.Handle); err != nil || now != head {
		return "", fmt.Errorf("%w: position %d was taken while this record was being written",
			wasmtier.ErrNotWritable, head)
	}

	// WHICH RECORD OCCUPIES THE POSITION IS RAFT'S TO SAY, and it says it in
	// the same entry that moves the head — so a position is never published
	// without its record, and never left holding one the head has passed.
	if err := cluster.Batch([]raft.BatchOp{
		{Set: &raft.SetCommand{Key: atKey(grant.Handle, head), Value: []byte(digest)}},
		{Set: &raft.SetCommand{Key: headKey(grant.Handle), Value: []byte(strconv.FormatUint(head+1, 10))}},
	}, applyTimeout); err != nil {
		if errors.Is(err, raft.ErrNotLeader) {
			return "", fmt.Errorf("%w: %s", wasmtier.ErrNotWritable, err.Error())
		}
		return "", fmt.Errorf("node: advancing the log's head: %w", err)
	}

	l.sweep(ctx, cluster, grant, db, head)
	return encodeCursor(head), nil
}

// Read answers the records after a cursor, oldest first.
func (l *logs) Read(ctx context.Context, grant invoke.Grant, after string, limit uint32) ([]wasmtier.Record, error) {
	if err := servableLog(grant); err != nil {
		return nil, err
	}

	cluster, db, err := l.ready()
	if err != nil {
		return nil, err
	}

	from, err := decodeCursor(after)
	if err != nil {
		return nil, err
	}

	// RETENTION TAKES THE FRONT, so position zero is not where a log starts —
	// the floor is. Reading from zero would find the hole retention left and
	// stop there, answering nothing for a log that is full of records.
	floor, err := floorOf(cluster, grant.Handle)
	if err != nil {
		return nil, err
	}
	switch {
	case after == "":
		from = floor
	case from < floor:
		// The records this caller wants are gone. Its own arm rather than an
		// empty answer, because "you are behind the log" and "you are caught
		// up" are different facts and a reader acts on them differently.
		return nil, fmt.Errorf("%w: those records are past this log's retention", wasmtier.ErrBadCursor)
	}

	page := int(limit)
	if page <= 0 || page > maxLogPage {
		page = maxLogPage
	}

	out := make([]wasmtier.Record, 0, page)
	for at := from; len(out) < page; at++ {
		// THE POSITION IS RESOLVED THROUGH RAFT, which is what makes a record
		// the one that was committed rather than the one that arrived last.
		digest, held := cluster.Get(atKey(grant.Handle, at))
		if !held {
			break
		}
		raw, err := db.Get(ctx, recordKey(grant.Handle, at, string(digest)))
		if err != nil {
			// A POSITION WITH NOTHING AT IT ENDS THE PAGE, and does not skip
			// ahead. Below the head it is a record still replicating; at the
			// head it is the end of the log. Either way the next call answers
			// it, and stepping over it would hand a reader records out of order
			// with a hole it can never come back to.
			break
		}

		var record logRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return nil, fmt.Errorf("node: the record at %d is not one: %w", at, err)
		}
		out = append(out, wasmtier.Record{Cursor: encodeCursor(at), At: record.At, Data: record.Data})
	}
	return out, nil
}

// maxLogPage is both the default page and the ceiling on a larger ask.
const maxLogPage = 512

// logRecord is what one position holds. The data is the author's; `At` is the
// LOG's clock, because what a log orders is when it took the record.
type logRecord struct {
	At   int64  `json:"at"`
	Data []byte `json:"data"`
}

// servableLog refuses a log this node cannot keep the promise of.
//
// A CONSENSUS THIS CLOUD DOES NOT SERVE IS REFUSED BY NAME. `crdt` is a
// different log — append anywhere, an order that converges rather than one that
// is decided — and serving it with raft machinery would answer an author's
// question with the other answer.
func servableLog(grant invoke.Grant) error {
	if c := grant.Settings[settingLogConsensus]; c != "" && c != "raft" {
		return fmt.Errorf("%w: %q", ErrLogConsensusUnserved, c)
	}
	return nil
}

// sweep drops what retention no longer keeps.
//
// IT RUNS ON THE APPENDER, which is the one node that may write and the one
// moment the log is known to have changed. A loop of its own would be a second
// thing to elect.
func (l *logs) sweep(ctx context.Context, cluster raft.Cluster, grant invoke.Grant, db kvdb.KVDB, head uint64) {
	age, _ := time.ParseDuration(grant.Settings[settingLogRetentionAge])
	size, _ := strconv.ParseInt(grant.Settings[settingLogRetentionSize], 10, 64)
	if age <= 0 && size <= 0 {
		// ABSENT MEANS FOREVER, which is the safe direction for a ledger.
		return
	}

	// THE WALK IS NEWEST FIRST, so what is kept is what a reader is most likely
	// to want and the scan stops at the first record beyond the budget instead
	// of reading the whole log.
	// WHERE IT STOPS DIGGING. Below the floor there is nothing left to drop,
	// and walking to zero on every append would re-delete the whole prefix each
	// time — once the mapping is raft's, that is a raft write per position.
	floor, err := floorOf(cluster, grant.Handle)
	if err != nil {
		return
	}

	var held int64
	cutoff := l.clock().Add(-age)
	for at := head; ; at-- {
		digest, ok := cluster.Get(atKey(grant.Handle, at))
		if !ok {
			break
		}
		raw, err := db.Get(ctx, recordKey(grant.Handle, at, string(digest)))
		if err != nil {
			break
		}
		held += int64(len(raw))

		var record logRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			break
		}
		beyond := (age > 0 && time.Unix(record.At, 0).Before(cutoff)) ||
			(size > 0 && held > size)

		// THE NEWEST RECORD IS NEVER DROPPED, whatever the budget says. A log
		// whose ceiling cannot hold one record is a misconfiguration, and
		// answering it by forgetting everything — including what was just
		// appended — loses the write a caller was told succeeded.
		if beyond && at < head {
			drop := make([]raft.BatchOp, 0, at-floor+1)
			for older := at; ; older-- {
				if digest, ok := cluster.Get(atKey(grant.Handle, older)); ok {
					db.Delete(ctx, recordKey(grant.Handle, older, string(digest)))
					drop = append(drop, raft.BatchOp{Delete: &raft.DeleteCommand{Key: atKey(grant.Handle, older)}})
				}
				if older == floor || older == 0 {
					break
				}
			}
			cluster.Batch(drop, applyTimeout)
			// WHERE THE LOG NOW STARTS, written where the head is written: a
			// reader on any node has to know which hole is retention and which
			// is a record still on its way.
			cluster.Set(floorKey(grant.Handle), []byte(strconv.FormatUint(at+1, 10)), applyTimeout)
			break
		}
		if at == 0 {
			break
		}
	}
}

// ready opens what a log needs, once.
func (l *logs) ready() (raft.Cluster, kvdb.KVDB, error) {
	if l.node.work == nil {
		return nil, nil, ErrNoRaft
	}
	// THE QUEUES' CLUSTER, not one of its own — see logDomain.
	cluster, err := l.node.work.consensus()
	if err != nil {
		return nil, nil, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.db == nil {
		if l.node.opts.databases == nil {
			return nil, nil, ErrNoCacheStore
		}
		db, err := l.node.opts.databases.New(logger, logDatabase, rebroadcastSeconds)
		if err != nil {
			return nil, nil, fmt.Errorf("node: opening the log database: %w", err)
		}
		l.db = db
		l.node.addCloser(closerFunc(db.Close))
	}
	return cluster, l.db, nil
}

func (l *logs) lockFor(handle string) *sync.Mutex {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.writing == nil {
		l.writing = map[string]*sync.Mutex{}
	}
	if _, held := l.writing[handle]; !held {
		l.writing[handle] = &sync.Mutex{}
	}
	return l.writing[handle]
}

func (l *logs) clock() time.Time {
	if l.node.opts.now == nil {
		return time.Now()
	}
	return l.node.opts.now()
}

func (l *logs) Close() error { return nil }

// headOf is the next position this log will write.
func headOf(cluster raft.Cluster, handle string) (uint64, error) {
	raw, held := cluster.Get(headKey(handle))
	if !held {
		return 0, nil
	}
	head, err := strconv.ParseUint(string(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("node: the head of %s is not a position: %w", handle, err)
	}
	return head, nil
}

func headKey(handle string) string  { return "logs/" + handle + "/head" }
func floorKey(handle string) string { return "logs/" + handle + "/floor" }

// floorOf is the oldest position this log still holds. Absent means nothing has
// been dropped, so the log starts where it always did.
func floorOf(cluster raft.Cluster, handle string) (uint64, error) {
	raw, held := cluster.Get(floorKey(handle))
	if !held {
		return 0, nil
	}
	floor, err := strconv.ParseUint(string(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("node: the floor of %s is not a position: %w", handle, err)
	}
	return floor, nil
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
func recordKey(handle string, at uint64, digest string) string {
	return "logs/" + handle + "/records/" + fmt.Sprintf("%020d", at) + "/" + digest
}

// atKey is which record occupies a position — raft's answer, not the store's.
func atKey(handle string, at uint64) string {
	return "logs/" + handle + "/at/" + fmt.Sprintf("%020d", at)
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
		return 0, fmt.Errorf("%w: %q", wasmtier.ErrBadCursor, cursor)
	}
	at, err := strconv.ParseUint(string(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", wasmtier.ErrBadCursor, cursor)
	}
	// AFTER, NOT AT: the cursor names the record a reader already has.
	return at + 1, nil
}
