package distributed

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/samyfodil/mushql/crdt"
	_ "github.com/samyfodil/mushql/puredriver"
	"github.com/taubyte/tau/pkg/raft"
)

// These tests exercise the Authority proposal from the tau side, so that the
// interface mushql is being asked for comes with a working implementation of
// the half tau owns rather than a description of one.
//
// WHAT THE MOCK CLUSTER IS AND IS NOT. raft.MockCluster is one always-leader
// in-memory map: a fair model of the replicated state two replicas converge on
// — the applied KV is the same map — and no model at all of leadership change,
// replication lag, or a partition. What is measured here is the sequencing, the
// encoding and the cursor; the concurrency argument for the dense index is
// written down in raftAuthority.gate and is reasoning, not a measurement.
//
// A REPLICA HERE IS A STORE AND NOT A SECOND AUTHORITY. NewRaftAuthority
// interns one authority per cluster and prefix, so asking it twice for the same
// mock answers with the same object — see authorities, and
// authority_sharing_test.go for why. Two replicas of one database are therefore
// two mushql stores reading one authority, which is also what they are on a
// node: one cluster, one log, one allocator.

// pendingOp is an op as capture builds it before anything stamps it: a table, a
// key, a kind and cells, with no site, sequence or timestamp.
func pendingOp(id string, amount int64) crdt.Op {
	return crdt.Op{
		Tbl:  "entries",
		PK:   crdt.EncodePK(id),
		Kind: crdt.OpInsert,
		Cells: []crdt.Cell{
			crdt.CellFromValue("payee", "alice"),
			crdt.CellFromValue("amount", amount),
		},
	}
}

func newAuthority(t *testing.T, cluster raft.Cluster) Authority {
	t.Helper()

	authority, err := NewRaftAuthority(cluster, "projects/p/databases/ledger")
	if err != nil {
		t.Fatalf("building the authority: %v", err)
	}
	return authority
}

func TestTheClusterAssignsADenseSequenceAcrossTransactions(t *testing.T) {
	ctx := context.Background()
	authority := newAuthority(t, raft.NewMockCluster())

	first, err := authority.Admit(ctx, []crdt.Op{pendingOp("e1", 1), pendingOp("e2", 2)})
	if err != nil {
		t.Fatalf("admitting the first transaction: %v", err)
	}
	second, err := authority.Admit(ctx, []crdt.Op{pendingOp("e3", 3)})
	if err != nil {
		t.Fatalf("admitting the second transaction: %v", err)
	}

	stamped := append(append([]crdt.Op{}, first...), second...)
	for i, op := range stamped {
		want := uint64(i + 1)
		if op.Seq != want {
			t.Fatalf("op %d took sequence %d; the log index is %d", i, op.Seq, want)
		}
		if op.HLC != crdt.HLC(want) {
			t.Fatalf("op %d carries timestamp %d; the index and the timestamp must be one number", i, op.HLC)
		}
		if op.Site != ClusterSite {
			t.Fatalf("op %d claims site %q; every replica writes as the cluster", i, op.Site)
		}
	}

	if head, err := authority.Head(ctx); err != nil || head != 3 {
		t.Fatalf("the head is %d (%v); three admitted ops put it at 3", head, err)
	}
}

// followerCluster is a cluster this node does not lead.
type followerCluster struct{ *raft.MockCluster }

func (followerCluster) IsLeader() bool { return false }

// TestAFollowerAdmitsNothingAndBurnsNothing is the property the local counter
// does not have: a refused write leaves no hole behind it, so the next
// successful write takes the index the refused one would have.
func TestAFollowerAdmitsNothingAndBurnsNothing(t *testing.T) {
	ctx := context.Background()
	mock := raft.NewMockCluster()

	follower := newAuthority(t, followerCluster{mock})
	if _, err := follower.Admit(ctx, []crdt.Op{pendingOp("e1", 1)}); !errors.Is(err, raft.ErrNotLeader) {
		t.Fatalf("a follower admitted a write with %v; it must refuse", err)
	}
	if head, err := follower.Head(ctx); err != nil || head != 0 {
		t.Fatalf("a refused admission moved the head to %d (%v)", head, err)
	}

	// The same log, led. The refused op's index was never spent.
	leader := newAuthority(t, mock)
	stamped, err := leader.Admit(ctx, []crdt.Op{pendingOp("e1", 1)})
	if err != nil {
		t.Fatalf("admitting on the leader: %v", err)
	}
	if stamped[0].Seq != 1 {
		t.Fatalf("the first admitted op took index %d; the refusal before it must have cost nothing", stamped[0].Seq)
	}
}

// TestAnAdmittedWriteReachesASecondReplicaThroughTheLogAlone is the end of the
// argument: with the identity assigned by the cluster, mushql's own apply path
// carries the write to another replica with no transport, no version-vector
// exchange and no anti-entropy round — Since is the whole of it.
func TestAnAdmittedWriteReachesASecondReplicaThroughTheLogAlone(t *testing.T) {
	ctx := context.Background()

	// One replicated state, two replicas of it — and one authority over it,
	// which is what the registry answers with for one cluster and one prefix.
	mock := raft.NewMockCluster()
	writerAuthority := newAuthority(t, mock)
	readerAuthority := newAuthority(t, mock)

	writer, writerDB := openLedgerStore(t, ClusterSite)
	reader, readerDB := openLedgerStore(t, ClusterSite)

	stamped, err := writerAuthority.Admit(ctx, []crdt.Op{pendingOp("e1", 42), pendingOp("e2", 7)})
	if err != nil {
		t.Fatalf("admitting: %v", err)
	}

	// The writer applies what the cluster admitted. In the real path the
	// guest's own statement already wrote these rows and this Ingest would
	// carry local=true; here no statement ran, so the row has to be
	// materialized from the merge.
	for _, op := range stamped {
		if err := writer.Ingest(ctx, op, false); err != nil {
			t.Fatalf("the writer applying index %d: %v", op.Seq, err)
		}
	}
	if got := entriesOn(t, writerDB); got["e1"] != 42 || got["e2"] != 7 {
		t.Fatalf("the writer holds %v", got)
	}

	// The reader is behind and catches up from its cursor.
	cursor := reader.LocalVV()[ClusterSite]
	ops, err := readerAuthority.Since(ctx, cursor, 0)
	if err != nil {
		t.Fatalf("reading after index %d: %v", cursor, err)
	}
	if len(ops) != len(stamped) {
		t.Fatalf("the log offers %d ops after index %d; %d were admitted", len(ops), cursor, len(stamped))
	}
	for _, op := range ops {
		if err := reader.Ingest(ctx, op, false); err != nil {
			t.Fatalf("the reader applying index %d: %v", op.Seq, err)
		}
	}

	if got := entriesOn(t, readerDB); got["e1"] != 42 || got["e2"] != 7 {
		t.Fatalf("the reader holds %v after replaying the log", got)
	}

	// And the cursor it now offers is the head, so the next Since asks for
	// nothing. This is the degenerate version vector the hypothesis predicted,
	// and it is the one part of the hypothesis that survived intact.
	head, err := readerAuthority.Head(ctx)
	if err != nil {
		t.Fatalf("reading the head: %v", err)
	}
	if got := reader.LocalVV()[ClusterSite]; got != head {
		t.Fatalf("the reader's cursor is %d and the head is %d", got, head)
	}
	if rest, err := readerAuthority.Since(ctx, head, 0); err != nil || len(rest) != 0 {
		t.Fatalf("a caught-up replica was offered %d more ops (%v)", len(rest), err)
	}
}

// TestConcurrentAdmissionsOnOneLeaderNeverShareAnIndex is what raftAuthority.mu
// is for, over the one racing shape the mock can actually express: several
// proposers inside one process, on one leader.
func TestConcurrentAdmissionsOnOneLeaderNeverShareAnIndex(t *testing.T) {
	ctx := context.Background()
	authority := newAuthority(t, raft.NewMockCluster())

	const writers = 16
	var wg sync.WaitGroup
	seen := make(chan uint64, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			stamped, err := authority.Admit(ctx, []crdt.Op{pendingOp(fmt.Sprintf("e%02d", i), int64(i))})
			if err != nil {
				t.Errorf("writer %d: %v", i, err)
				return
			}
			seen <- stamped[0].Seq
		}(i)
	}
	wg.Wait()
	close(seen)

	taken := map[uint64]bool{}
	for seq := range seen {
		if taken[seq] {
			t.Fatalf("index %d was handed out twice", seq)
		}
		taken[seq] = true
	}
	if len(taken) != writers {
		t.Fatalf("%d of %d writers were stamped", len(taken), writers)
	}
	for i := uint64(1); i <= writers; i++ {
		if !taken[i] {
			t.Fatalf("index %d was never handed out; the sequence must be dense", i)
		}
	}

	// Every op is readable, in order, with no hole — which is what a dense
	// sequence is FOR: mushql's cursor advances contiguously or not at all.
	ops, err := authority.Since(ctx, 0, writers)
	if err != nil {
		t.Fatalf("replaying the log: %v", err)
	}
	if len(ops) != writers {
		t.Fatalf("the log replays %d of %d ops", len(ops), writers)
	}
}
