package distributed

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"
	"weak"

	"github.com/fxamacker/cbor/v2"
	"github.com/samyfodil/mushql/crdt"
	"github.com/taubyte/tau/pkg/raft"
)

// This file answers the question a raft-backed `sql` database put to mushql:
// does it need a raft replication driver, or an interface tau can implement?
//
// NOTHING HERE MODIFIES MUSHQL, AND NOTHING HERE CAN BE WIRED UP YET. Authority
// is the seam mushql does not have; raftAuthority is tau's side of it, written
// out in full so that the proposal is a compiled, tested thing rather than a
// sketch. The moment mushql grows the seam — see Authority's doc for exactly
// what has to change — this file becomes the driver with no redesign.
//
// # WHY IT IS NOT A crdt.Transport
//
// The hypothesis was that raft is a degenerate CRDT: one site, the log index
// as the timestamp, no two ops ever concurrent, and anti-entropy collapsing to
// "everything after index N". raft_hypothesis_test.go tests that claim, and it
// splits in half.
//
// The ORDERING half holds, completely. Store.mergeCell compares (hlc, site)
// with TagLess and consults no wall clock, so log indices order the merge
// exactly as they order the log; a one-site version vector is exactly an
// after-index cursor; and single-site anti-entropy carries a whole log between
// two stores. On ordering alone, raft really is a degenerate CRDT and a
// Transport would be enough.
//
// The DURABILITY half fails, in three independent places, and a Transport
// cannot reach any of them:
//
//  1. THE TRANSPORT IS ON THE WRONG SIDE OF THE COMMIT. capture.postCommit
//     hands ops to Syncer.propagate AFTER the user's transaction is durable,
//     and Node.Broadcast discards every PushOp error. A refused write commits,
//     materializes, takes a sequence number, and is reported to the caller as
//     a success. Raft's whole contract is the opposite: the write is not a
//     write until the cluster has it.
//
//  2. THE INDEX AND THE CAPTURE CLOCK ARE DIFFERENT NUMBER DOMAINS.
//     crdt.Clock.Now stamps a captured write with Unix milliseconds in the high
//     48 bits, around 1.7e12. A log index is small. So a locally captured write
//     outranks a cluster op at index 1e9 forever, and the merge the hypothesis
//     said would never fire fires on every cell and always for the same side.
//
//  3. THE SEQUENCE NUMBER IS ASSIGNED BEFORE THE CLUSTER SEES THE OP, and the
//     cursor built from it is not backed by the oplog. capture.flush takes a
//     number from the Syncer's own counter, and a commit that then fails burns
//     it; capture.postCommit advances the cursor with Store.NoteLocal, which
//     names the highest number that committed without checking that the ones
//     below it exist, while Store.loadVV on reopen walks CONTIGUOUSLY and stops
//     at the first hole. Under raft the single site is the whole cluster, so
//     one refused proposal stops the entire database's replication — at the
//     next restart, silently.
//
// Each of those is pinned by a passing test. Together they say the seam raft
// needs is not the one that carries ops between peers; it is the one that
// decides, before the transaction commits, whether the ops exist at all and
// what identity they carry.

// Authority is the seam mushql would have to grow, and the whole of it.
//
// A mushql database with an Authority defers its write identity and its
// durability to that Authority instead of to its own counter and its own
// oplog. For a CRDT deployment the Authority is the local site itself and
// Admit is the counter it already has, so nothing changes. For raft the
// Authority is the cluster, and Admit is a proposal that can be refused.
//
// # WHERE MUSHQL WOULD CALL IT
//
// Inside capture.flush, in place of the three lines that stamp Site, Seq and
// HLC from local state:
//
//	stamped, err := authority.Admit(ctx, c.buf)
//	if err != nil {
//	    return err // aborts the commit; puredriver rolls the transaction back
//	}
//
// That position is not a new mechanism. puredriver.CaptureHooks.PreCommit
// already runs inside the transaction about to commit and already documents
// that its error aborts that transaction, and
// TestTheDriverCanRefuseACommitThoughTheCrdtLayerOffersNoWayTo measures that it
// does. The capacity raft needs is present in mushql's driver today; what is
// missing is any way for the crdt layer to reach it, because crdt.Wrap installs
// its own CaptureHooks and Syncer exposes no interposition.
//
// # WHAT ELSE HAS TO CHANGE WITH IT
//
// Admit alone is not sufficient. Four smaller things travel with it, each
// pinned by a test in this package:
//
//   - THE CURSOR MUST COME FROM THE AUTHORITY, not from the contiguity walk in
//     Store.loadVV and not from Store.NoteLocal. Under raft the cursor is a log
//     index and the authority is the only thing that knows it. mushql should
//     persist the applied cursor in _crdt_meta inside the same transaction that
//     applies the ops, and read it back on open, instead of deriving it.
//     (TestAVersionVectorAheadOfTheOplogFallsBackOnRestart)
//
//   - THE SITE ID MUST STOP BEING A PROCESS-GLOBAL REGISTRY KEY. Every replica
//     of a raft-backed database has the same site — the cluster — and tau's
//     dream runs a cloud's nodes in one process, so two replicas are two Wrap
//     calls with one site id and the second is refused. The registry needs its
//     own key, or Wrap needs to take the connection marker separately from the
//     actor id. (TestTwoReplicasOfOneRaftClusterCannotBeWrappedInOneProcess)
//
//   - THE SYNCED TABLE SET MUST BE ABLE TO GROW AFTER THE FIRST WRAP. This one
//     predates raft and blocks the engine outright: a tau `sql` database
//     declares no tables, every table is created by a guest's CREATE TABLE
//     after the database is open, and rewrapping with the grown set is refused
//     permanently by the stored schema fingerprint. (schema_test.go)
//
//   - THE TWO LOCAL COUNTERS MUST BECOME ONE. crdt.Syncer and crdt.Node keep
//     separate sequence counters over one oplog and both hand out the same
//     number, so the second write is deduped away silently. An Authority makes
//     this moot by being the only source of a sequence — which is the cleanest
//     argument for putting the counter behind an interface at all.
//     (seq_test.go)
type Authority interface {
	// Admit offers every op one transaction produced and returns them stamped
	// with the identity the authority assigned — Site, Seq and HLC — in the
	// same order.
	//
	// IT RETURNS ONLY WHEN THE OPS ARE DURABLE by the authority's own rule. For
	// raft that is a committed log entry; for a lone CRDT site it is
	// immediate. The caller may commit the transaction once Admit returns, and
	// must abort it on any error: an op that was not admitted must not become
	// visible to a local reader, which is the ordering a CRDT has backwards and
	// raft needs.
	//
	// It is all-or-nothing. A transaction's ops are one unit, so a partial
	// admission is an error and stamps nothing.
	//
	// AN ERROR MUST BURN NOTHING. A refused Admit leaves no hole in the
	// sequence — which is the defect the local counter has today, and the
	// reason this is an interface rather than a retry loop around the existing
	// one.
	Admit(ctx context.Context, ops []crdt.Op) ([]crdt.Op, error)

	// Since returns admitted ops after the given cursor, in order, at most
	// limit of them. It is the whole of anti-entropy for a raft authority: the
	// log answers it natively.
	//
	// It may lag: a follower answers from the state it has applied. A cursor
	// the caller holds is always valid — the authority never renumbers — so
	// lag resolves by asking again.
	Since(ctx context.Context, after uint64, limit int) ([]crdt.Op, error)

	// Head returns the cursor of the last op this node has seen admitted, for
	// a caller deciding whether it is behind.
	Head(ctx context.Context) (uint64, error)
}

// ClusterSite is the site id every replica of a raft-backed database shares:
// the cluster is the single writer, so there is exactly one actor and the LWW
// merge has nothing to arbitrate between.
const ClusterSite = "cluster"

var (
	// ErrPartialAdmission is returned when the cluster accepted some of a
	// transaction's ops and not others, which the interface forbids.
	ErrPartialAdmission = errors.New("distributed: the cluster admitted part of a transaction")

	// ErrCursorGap is returned by Since when the log is missing an index below
	// the head. It is never expected — it means the authority renumbered or
	// lost an entry — and it is an error rather than a silent stop because a
	// silent stop is exactly the failure mushql has today.
	ErrCursorGap = errors.New("distributed: the admitted log has a hole in it")
)

// proposeTimeout bounds one Admit. It is well inside raft.MaxApplyTimeout
// because a guest's INSERT is blocked for the whole of it: a database that
// takes a minute to refuse a write is a function that takes a minute to fail.
const proposeTimeout = 5 * time.Second

// sinceLimit caps a Since with no limit of its own.
const sinceLimit = 256

// raftAuthority is tau's Authority: the cluster is the single writer, the raft
// log is the oplog, and the log index is both the sequence number and the
// timestamp.
//
// UNEXERCISED AGAINST A LIVE CLUSTER. authority_test.go drives it over
// raft.MockCluster, which is one always-leader in-memory map — enough to pin
// the sequencing, the encoding and the cursor, and not enough to say anything
// about leadership change or replication lag. Treat the concurrency argument
// below as reasoning, not as a measurement.
type raftAuthority struct {
	cluster raft.Cluster
	prefix  string

	// gate serializes the proposals of everyone holding this authority, and
	// NewRaftAuthority makes sure that is everyone proposing for this database
	// on this node — see authorities.
	//
	// IT IS LOAD-BEARING AND IT IS NOT SUFFICIENT ON ITS OWN. pkg/raft has no
	// compare-and-swap, so the next index has to be read and then written as
	// two operations, and the gap between them is where two proposers would
	// collide. Three facts close it, and all three are needed:
	//
	//   - only the leader proposes (Admit refuses on a follower, because
	//     pkg/raft's Set/Batch return ErrNotLeader and do not forward);
	//   - there is at most one leader per term, so the only proposers that can
	//     race are on THIS node, and one authority per database means the gate
	//     serializes all of them — a lock on an instance would only serialize
	//     the callers that happened to share the instance;
	//   - a leader that has been deposed loses the write rather than
	//     interleaving with the new one — its Batch is rejected by raft itself,
	//     so a stale read of the head cannot be committed on top of a newer one.
	//
	// The Barrier below is what makes the read current: a freshly elected
	// leader has committed entries it has not applied, and Get reads applied
	// state.
	//
	// IT IS A CHANNEL AND NOT A MUTEX BECAUSE THE WAIT IS THE ONE PART OF
	// ADMIT NOTHING ELSE BOUNDS. The holder is inside two round trips of up to
	// proposeTimeout each, so the Nth proposer for a busy database waits
	// roughly N times that — while the guest's INSERT that is waiting on it has
	// a context somebody chose a deadline for. A caller blocked on sync.Mutex
	// cannot give up; one blocked on a channel can, and one that gives up
	// before it proposes burns nothing, which is what Admit promises. Past the
	// gate the deadline is proposeTimeout's and not the caller's: Barrier and
	// Batch take a duration, not a context.
	gate chan struct{}
}

var _ Authority = (*raftAuthority)(nil)

// authorityKey names one database on one cluster.
type authorityKey struct {
	cluster raft.Cluster
	prefix  string
}

// authorities holds one raftAuthority per database, because TWO AUTHORITIES
// FOR ONE DATABASE WOULD HAND OUT ONE INDEX TWICE: each reads head N under its
// own mutex and each writes N+1, and being on the same leader does not stop
// them. Sharing the authority rather than sharing a lock is what makes mu
// correct by construction — two instances holding one mutex would still be two
// objects with two futures, and the next piece of per-database state would
// have the same bug again.
//
// THE KEY IS THE CLUSTER AND THE PREFIX, NOT THE PREFIX ALONE. A prefix names
// a database, but tau's dream runs a cloud's nodes in one process, so one
// prefix there is one database per node; interning on it alone would hand one
// node another node's authority, and with it another node's cluster.
//
// A Cluster implementation has to be comparable to be half of a key. Every one
// is a pointer today; one written on a struct with a map or a slice in it would
// panic here rather than at its own call site.
//
// IT HOLDS WHAT IT INTERNS WEAKLY, so an entry lives exactly as long as
// somebody can still propose through it. Holding it strongly never evicted
// anything: the key holds the cluster, a cluster is one per node per boot — the
// namespace cache drops the lot when the groups go — so every reboot and every
// torn-down dream universe left an entry pinning a dead cluster, its state
// machine and its store handle for the life of the process. Weak interning is
// the shape the language offers for exactly this map, and it costs nothing
// here: the authority holds no state of its own, the head lives in the log, and
// an authority nobody holds is an authority nobody can be allocating through.
var authorities = struct {
	mu   sync.Mutex
	held map[authorityKey]weak.Pointer[raftAuthority]
}{held: make(map[authorityKey]weak.Pointer[raftAuthority])}

// forgetAuthority drops the entry of an authority that has been collected.
//
// IT CHECKS THE ENTRY IS STILL THE DEAD ONE. A cleanup runs some time after the
// collection, and an opener may have interned a live replacement under the same
// key in between; deleting that one would hand the next opener a second
// allocator, which is the whole defect this registry exists to close.
func forgetAuthority(key authorityKey) {
	authorities.mu.Lock()
	defer authorities.mu.Unlock()

	if held, ok := authorities.held[key]; ok && held.Value() == nil {
		delete(authorities.held, key)
	}
}

// NewRaftAuthority returns the authority for one database over one cluster.
// prefix separates this database's log from everything else in the cluster's
// keyspace, and is the database's own addressing prefix.
//
// IT RETURNS THE SAME AUTHORITY EVERY TIME it is asked for the same database,
// which is what keeps a second opener from allocating over the first. Callers
// race to open — the resident table opens outside its own lock and closes the
// loser asynchronously, and a redeployment serves from a new connection before
// the old one has closed — so two openers for one database is that code's
// normal path, not its exceptional one.
func NewRaftAuthority(cluster raft.Cluster, prefix string) (Authority, error) {
	if cluster == nil {
		return nil, errors.New("distributed: no cluster to admit writes to")
	}
	if prefix == "" {
		return nil, errors.New("distributed: an authority with no prefix would share a log with every other database")
	}

	key := authorityKey{cluster: cluster, prefix: prefix}

	authorities.mu.Lock()
	defer authorities.mu.Unlock()

	if held, ok := authorities.held[key]; ok {
		if authority := held.Value(); authority != nil {
			return authority, nil
		}
	}

	authority := &raftAuthority{cluster: cluster, prefix: prefix, gate: make(chan struct{}, 1)}
	authorities.held[key] = weak.Make(authority)

	// THE CLEANUP TAKES THE KEY AND NOT THE AUTHORITY. An argument the
	// authority can be reached from would keep it alive forever, which is the
	// leak again with a cleanup attached to it.
	runtime.AddCleanup(authority, forgetAuthority, key)

	return authority, nil
}

// opKey addresses one admitted op.
//
// TWENTY ZERO-PADDED DIGITS so that the keyspace orders the way the log does.
// pkg/raft offers Keys(prefix) and no range scan, so a reader that ever has to
// list rather than seek gets the right order for free; twenty digits is the
// width of a uint64, so the padding never truncates.
func (a *raftAuthority) opKey(index uint64) string {
	return fmt.Sprintf("%s/ops/%020d", a.prefix, index)
}

// headKey names the cursor of the last admitted op.
func (a *raftAuthority) headKey() string { return a.prefix + "/head" }

// Admit proposes one transaction's ops as a single raft log entry.
func (a *raftAuthority) Admit(ctx context.Context, ops []crdt.Op) ([]crdt.Op, error) {
	if len(ops) == 0 {
		return nil, nil
	}

	// See gate: this wait is the one part of an Admit that nothing else
	// bounds, so it is the one that has to answer to the caller's context.
	select {
	case a.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-a.gate }()

	// THE ALLOCATOR HAS TO OUTLIVE ITS OWN PROPOSAL. The registry holds this
	// authority weakly, and nothing below reads a field after the Batch is
	// handed off, so the collector is free to decide mid-proposal that nobody
	// holds it, drop its entry and let the next opener intern a second
	// allocator for this database — the defect the registry exists to close,
	// reintroduced by the thing that keeps the registry from leaking.
	defer runtime.KeepAlive(a)

	// A FOLLOWER REFUSES RATHER THAN FORWARDS, because pkg/raft has no
	// forwarding: Set, Batch and Apply all answer ErrNotLeader on a follower
	// and there is no leader-proxy in the surface. Answering here rather than
	// letting Batch answer keeps the refusal free of a half-computed head.
	if !a.cluster.IsLeader() {
		return nil, raft.ErrNotLeader
	}

	// See mu: Get reads applied state, and a leader that has just won an
	// election has committed entries it has not applied yet.
	if err := a.cluster.Barrier(proposeTimeout); err != nil {
		return nil, fmt.Errorf("distributed: catching up before admitting: %w", err)
	}

	head, err := a.head()
	if err != nil {
		return nil, err
	}

	stamped := make([]crdt.Op, len(ops))
	writes := make([]raft.BatchOp, 0, len(ops)+1)
	for i, op := range ops {
		index := head + uint64(i) + 1

		// ONE SITE, AND THE INDEX IS BOTH THE SEQUENCE AND THE TIMESTAMP. The
		// sequence must be dense for a version-vector cursor to advance, and
		// the timestamp must be a total order for the merge to be
		// deterministic; a log index is both, so one number does both jobs and
		// no two ops are ever concurrent.
		op.Site = ClusterSite
		op.Seq = index
		op.HLC = crdt.HLC(index)

		blob, err := cbor.Marshal(op)
		if err != nil {
			return nil, fmt.Errorf("distributed: encoding an op: %w", err)
		}
		writes = append(writes, raft.BatchOp{Set: &raft.SetCommand{Key: a.opKey(index), Value: blob}})
		stamped[i] = op
	}

	newHead := head + uint64(len(ops))
	headBlob, err := cbor.Marshal(newHead)
	if err != nil {
		return nil, fmt.Errorf("distributed: encoding the head: %w", err)
	}
	writes = append(writes, raft.BatchOp{Set: &raft.SetCommand{Key: a.headKey(), Value: headBlob}})

	// ONE BATCH IS ONE LOG ENTRY, which is what makes the head and the ops it
	// covers atomic. A head that moved without its ops would be a permanent
	// hole; ops without a head would be invisible.
	//
	// ATOMIC ON THE NODE THAT APPLIES IT, AND NOT CLUSTER-WIDE. hashicorp/raft
	// advances lastApplied whether the state machine accepted the entry or
	// refused it, so a node whose store failed on this batch is permanently
	// missing it while its peers have it, and nothing in the running cluster
	// repairs that — only a snapshot restore does. If such a node is later
	// elected, its head is below the cluster's and this allocates over indices
	// that are already spent. Closing that means an apply error taking the node
	// out of service, which is pkg/raft's call to make and not this file's.
	if err := a.cluster.Batch(writes, proposeTimeout); err != nil {
		return nil, err
	}
	return stamped, nil
}

// Since reads admitted ops after a cursor.
func (a *raftAuthority) Since(ctx context.Context, after uint64, limit int) ([]crdt.Op, error) {
	if limit <= 0 || limit > sinceLimit {
		limit = sinceLimit
	}
	head, err := a.head()
	if err != nil {
		return nil, err
	}

	var ops []crdt.Op
	for index := after + 1; index <= head && len(ops) < limit; index++ {
		blob, ok, err := a.cluster.TryGet(a.opKey(index))
		if err != nil {
			// A FAILED READ IS NOT A HOLE. ErrCursorGap says the authority
			// renumbered or lost an entry, which is corruption a caller cannot
			// retry past; a store that could not answer is a read to try
			// again.
			return nil, fmt.Errorf("distributed: reading the op at index %d: %w", index, err)
		}
		if !ok {
			// SEEKING AND NOT LISTING, because pkg/raft's Keys(prefix) answers
			// with every key under the prefix and no bound — on an oplog that
			// is the whole history, every round. A miss below the head is
			// therefore a real hole rather than a paging artifact.
			return nil, fmt.Errorf("%w: index %d is missing below head %d", ErrCursorGap, index, head)
		}
		var op crdt.Op
		if err := cbor.Unmarshal(blob, &op); err != nil {
			return nil, fmt.Errorf("distributed: decoding the op at index %d: %w", index, err)
		}
		ops = append(ops, op)
	}
	return ops, nil
}

// Head answers the cursor of the last op this node has seen admitted.
func (a *raftAuthority) Head(ctx context.Context) (uint64, error) { return a.head() }

// head reads the cursor from applied state. An absent head is index zero: a
// database nobody has written to yet.
//
// A READ THAT FAILED IS NOT A DATABASE NOBODY HAS WRITTEN TO, which is why
// this reads through TryGet and not Get. Guessing zero on a failed read spends
// indices that are already spent: the next Admit writes over ops/…0001 upward
// and republishes a head below the one it could not read, which every reader
// then reports as ErrCursorGap forever. Refusing burns nothing, which is what
// Admit's contract promises; guessing is the one move that cannot be undone.
func (a *raftAuthority) head() (uint64, error) {
	blob, ok, err := a.cluster.TryGet(a.headKey())
	if err != nil {
		return 0, fmt.Errorf("distributed: reading the head: %w", err)
	}
	if !ok {
		return 0, nil
	}
	var head uint64
	if err := cbor.Unmarshal(blob, &head); err != nil {
		return 0, fmt.Errorf("distributed: decoding the head: %w", err)
	}
	return head, nil
}
