package node

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/raft"
)

// log_test is the node's half: raft decides the order, the replicated store
// holds the records, and neither holds what the other should.

func logGrant(handle string, settings map[string]string) invoke.Grant {
	if settings == nil {
		settings = map[string]string{}
	}
	settings["type"] = "log"
	return invoke.Grant{
		Kind: "sync", Label: "audit", Handle: handle,
		Ops: []string{"append", "read"}, Settings: settings,
	}
}

// logging is a node whose queue cluster has formed, and the logs over it.
func logging(t *testing.T) *logs {
	t.Helper()

	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}})
	q := &queues{node: n}
	q.form()
	<-q.forming
	n.work = q
	return &logs{node: n}
}

// TestTHEORDERISTHELOGS. Appends come back in the order they were taken, and a
// cursor reads what FOLLOWS it.
func TestALogKeepsTheOrderItTookRecordsIn(t *testing.T) {
	l := logging(t)
	ctx := t.Context()
	grant := logGrant("shop/prod/QmAudit", nil)

	var cursors []string
	for _, line := range []string{"one", "two", "three"} {
		c, err := l.Append(ctx, grant, []byte(line))
		require.NoError(t, err)
		require.NotEmpty(t, c)
		cursors = append(cursors, c)
	}
	require.Len(t, cursors, 3)

	all, err := l.Read(ctx, grant, "", 0)
	require.NoError(t, err)
	require.Len(t, all, 3)
	require.Equal(t, "one", string(all[0].Data))
	require.Equal(t, "three", string(all[2].Data))
	require.Equal(t, cursors[0], all[0].Cursor, "the cursor an append answered is the one the record has")

	after, err := l.Read(ctx, grant, cursors[0], 0)
	require.NoError(t, err)
	require.Len(t, after, 2, "a cursor reads what follows it, not what it names")
	require.Equal(t, "two", string(after[0].Data))
}

// TestREADINGCONSUMESNOTHING — the difference from a queue, at the backing.
func TestReadingTheSameLogTwiceAnswersTheSame(t *testing.T) {
	l := logging(t)
	ctx := t.Context()
	grant := logGrant("shop/prod/QmAudit", nil)

	_, err := l.Append(ctx, grant, []byte("kept"))
	require.NoError(t, err)

	first, err := l.Read(ctx, grant, "", 0)
	require.NoError(t, err)
	second, err := l.Read(ctx, grant, "", 0)
	require.NoError(t, err)
	require.Equal(t, first, second)
}

// TestTWOLOGSDoNotSeeEachOther, and a handle that is a PREFIX of another's is
// the case that catches a missing separator.
func TestLogsAreScopedByHandle(t *testing.T) {
	l := logging(t)
	ctx := t.Context()

	require.NotPanics(t, func() {
		_, err := l.Append(ctx, logGrant("shop/prod/QmA", nil), []byte("mine"))
		require.NoError(t, err)
		_, err = l.Append(ctx, logGrant("shop/prod/QmAB", nil), []byte("theirs"))
		require.NoError(t, err)
	})

	mine, err := l.Read(ctx, logGrant("shop/prod/QmA", nil), "", 0)
	require.NoError(t, err)
	require.Len(t, mine, 1, "QmA saw QmAB's records: %v", mine)
	require.Equal(t, "mine", string(mine[0].Data))
}

// TestTHEPAGEIsBounded, so a reader of a long log is not handed all of it.
func TestALogPageIsCapped(t *testing.T) {
	l := logging(t)
	ctx := t.Context()
	grant := logGrant("shop/prod/QmAudit", nil)

	for i := 0; i < 5; i++ {
		_, err := l.Append(ctx, grant, []byte(strconv.Itoa(i)))
		require.NoError(t, err)
	}

	page, err := l.Read(ctx, grant, "", 2)
	require.NoError(t, err)
	require.Len(t, page, 2)

	require.Equal(t, 512, maxLogPage, "and an unbounded ask is capped at the cloud's own number")
}

// TestARECORDCARRIESTHELOGSCLOCK, not the appender's: what a log orders is when
// it took the record.
func TestARecordIsStampedByTheLog(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}},
		WithClock(func() time.Time { return at }))
	q := &queues{node: n}
	q.form()
	<-q.forming
	n.work = q

	l := &logs{node: n}
	grant := logGrant("shop/prod/QmAudit", nil)
	_, err := l.Append(t.Context(), grant, []byte("x"))
	require.NoError(t, err)

	got, err := l.Read(t.Context(), grant, "", 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, at.Unix(), got[0].At)
}

// TestARECORDBIGGERTHANTHEABIWILLCARRYIsItsOwnArm: a record crosses as one
// list<u8>, so it is materialised whole on both sides.
func TestAnOversizeRecordIsRefused(t *testing.T) {
	l := logging(t)

	_, err := l.Append(t.Context(), logGrant("shop/prod/QmAudit", nil), make([]byte, maxLogRecord+1))
	require.ErrorIs(t, err, wasmtier.ErrRecordTooLarge)
}

// TestACRDTLOGISADIFFERENTLOG and is refused by name rather than served with
// raft machinery, which would answer the author's question with the
// other answer.
func TestACrdtLogIsRefusedByName(t *testing.T) {
	l := logging(t)

	_, err := l.Append(t.Context(), logGrant("shop/prod/QmAudit",
		map[string]string{"log/consensus": "crdt"}), []byte("x"))
	require.ErrorIs(t, err, ErrLogConsensusUnserved)

	_, err = l.Read(t.Context(), logGrant("shop/prod/QmAudit",
		map[string]string{"log/consensus": "crdt"}), "", 0)
	require.ErrorIs(t, err, ErrLogConsensusUnserved)
}

// TestABADCursorIsItsOwnArm, so a caller starts over instead of retrying a
// position this log can never answer.
func TestAnUnreadableCursorIsRefused(t *testing.T) {
	l := logging(t)

	_, err := l.Read(t.Context(), logGrant("shop/prod/QmAudit", nil), "not base64!!", 0)
	require.ErrorIs(t, err, wasmtier.ErrBadCursor)
}

// TestRETENTIONDROPSTHEOLDEST, which is the opposite of a bucket's ceiling: a
// full log forgets the front, a full bucket refuses the write.
func TestRetentionDropsTheOldestRecords(t *testing.T) {
	l := logging(t)
	ctx := t.Context()

	// Four records, and a ceiling that holds roughly two of them as stored —
	// what retention measures is what the log occupies, not the payload alone.
	grant := logGrant("shop/prod/QmAudit", map[string]string{"log/retention/size": "100"})
	for _, line := range []string{"0123456789", "1123456789", "2123456789", "3123456789"} {
		_, err := l.Append(ctx, grant, []byte(line))
		require.NoError(t, err)
	}

	held, err := l.Read(ctx, grant, "", 0)
	require.NoError(t, err)
	require.NotEmpty(t, held, "retention dropped the whole log")
	require.Less(t, len(held), 4, "retention kept everything: %d records", len(held))

	// AND WHAT SURVIVED IS THE NEWEST. A log forgets its front.
	require.Equal(t, "3123456789", string(held[len(held)-1].Data))
}

// TestAREADERBEHINDRETENTIONISTOLDSO. "You are behind the log" and "you are
// caught up" are different facts, and an empty answer would tell a reader the
// second when the first is true.
func TestACursorBelowTheFloorIsRefused(t *testing.T) {
	l := logging(t)
	ctx := t.Context()
	grant := logGrant("shop/prod/QmAudit", map[string]string{"log/retention/size": "100"})

	first, err := l.Append(ctx, grant, []byte("0123456789"))
	require.NoError(t, err)
	for _, line := range []string{"1123456789", "2123456789", "3123456789"} {
		_, err := l.Append(ctx, grant, []byte(line))
		require.NoError(t, err)
	}

	// The oldest record is gone, so resuming from it is not an empty page.
	_, err = l.Read(ctx, grant, first, 0)
	require.ErrorIs(t, err, wasmtier.ErrBadCursor)

	// AND READING FROM THE BEGINNING STILL WORKS: it starts at the floor.
	held, err := l.Read(ctx, grant, "", 0)
	require.NoError(t, err)
	require.NotEmpty(t, held)
	require.Equal(t, "3123456789", string(held[len(held)-1].Data))
}

// loggingOn is a logs over a cluster the test controls, so what raft answered
// and what the log did about it are two separate questions.
func loggingOn(t *testing.T, cluster raft.Cluster) *logs {
	t.Helper()

	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}},
		WithRaft(func(string) (raft.Cluster, error) { return cluster, nil }))
	q := &queues{node: n}
	q.form()
	<-q.forming
	n.work = q
	return &logs{node: n}
}

// TestAnAppendOffTheLeaderIsRefusedRatherThanOrdered. Raft refuses a write off
// the leader anyway; what this stops is a follower reading a head and handing
// back a cursor for a record it never wrote.
func TestAnAppendOffTheLeaderIsRefused(t *testing.T) {
	l := loggingOn(t, notLeading())

	_, err := l.Append(t.Context(), logGrant("shop/prod/QmAudit", nil), []byte("paid"))
	require.ErrorIs(t, err, wasmtier.ErrNotWritable)
	require.ErrorContains(t, err, "ordered by the node holding the log's leadership")
}

// TestAHeadReadWithoutABarrierIsNotRead. A leader that has just won an election
// has committed entries it has not applied, so a head read before the barrier
// could assign a position the previous leader already used.
func TestAnAppendThatCannotBarrierIsRefused(t *testing.T) {
	cluster := notLeading()
	cluster.leader = true
	cluster.barrier = errors.New("no quorum")

	l := loggingOn(t, cluster)

	_, err := l.Append(t.Context(), logGrant("shop/prod/QmAudit", nil), []byte("paid"))
	require.ErrorContains(t, err, "reading the log's head")
	require.ErrorContains(t, err, "no quorum")
}

// TestLeadershipLostBetweenTheRecordAndTheHeadIsStillNotWritable. The record
// landed and the head did not move, which is the order this writes in on
// purpose — a position published before its record exists is one a reader
// cannot tell from one it will never get.
func TestLeadershipLostWhileAppendingIsNotWritable(t *testing.T) {
	cluster := notLeading()
	cluster.leader = true
	cluster.set = raft.ErrNotLeader

	l := loggingOn(t, cluster)

	_, err := l.Append(t.Context(), logGrant("shop/prod/QmAudit", nil), []byte("paid"))
	require.ErrorIs(t, err, wasmtier.ErrNotWritable)
}

// TestAHeadThatIsNotAPositionIsReported. Raft holds the head as text; something
// that is not a number there is a store this node cannot reason about, and
// appending from zero would overwrite every record already written.
func TestAHeadThatIsNotAPositionIsReported(t *testing.T) {
	cluster := notLeading()
	cluster.leader = true

	l := loggingOn(t, cluster)
	handle := "shop/prod/QmAudit"
	require.NoError(t, cluster.Set(headKey(handle), []byte("somewhere"), time.Second))

	_, err := l.Append(t.Context(), logGrant(handle, nil), []byte("paid"))
	require.ErrorContains(t, err, "is not a position")
}

// TestALogOnANodeThatRunsNoWorkerIsRefused. A log is an order and ordering is
// what raft is for; the cluster a log uses is the QUEUES' one, so a node that
// opened no worker has nowhere to put the head.
func TestALogOnANodeThatRunsNoWorkerIsRefused(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register}})
	require.Nil(t, n.work, "no worker, so nothing built the queue backing")

	l := &logs{node: n}

	_, err := l.Append(t.Context(), logGrant("shop/prod/QmAudit", nil), []byte("paid"))
	require.ErrorIs(t, err, ErrNoRaft)

	_, err = l.Read(t.Context(), logGrant("shop/prod/QmAudit", nil), "", 10)
	require.ErrorIs(t, err, ErrNoRaft)

	require.NoError(t, l.Close())
}
