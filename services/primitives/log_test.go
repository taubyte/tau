package primitives

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
)

// TestALogAppendsAndReadsForward, which is the whole of what a log promises:
// records come back in the order they were taken, and a cursor names the one
// the reader already has rather than the one it wants next.
func TestALogAppendsAndReadsForward(t *testing.T) {
	tier := starting(t).Local()
	ctx := t.Context()
	audit := grantFor(auditID)

	for _, line := range []string{"created", "paid", "shipped"} {
		cursor, err := tier.Append(ctx, audit, []byte(line))
		require.NoError(t, err)
		require.NotEmpty(t, cursor, "an append must name where the record landed")
	}

	read, err := tier.Read(ctx, audit, "", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"created", "paid", "shipped"}, payloads(read))

	// AFTER, AND NOT AT. The cursor names what the reader has, so reading from
	// the first record answers the two that follow it.
	rest, err := tier.Read(ctx, audit, read[0].Cursor, 0)
	require.NoError(t, err)
	require.Equal(t, []string{"paid", "shipped"}, payloads(rest))

	// CAUGHT UP IS AN EMPTY PAGE AND NOT A REFUSAL, which is what lets a reader
	// poll the same cursor until something lands.
	none, err := tier.Read(ctx, audit, read[2].Cursor, 0)
	require.NoError(t, err)
	require.Empty(t, none)
}

// TestTwoLogsDoNotSeeEachOther. Every log's state hangs off Spec.Prefix, which
// carries the deployment and the resource id — so two logs in one project share
// a store and never a key, and two projects that both declare `audit` never
// meet.
func TestTwoLogsDoNotSeeEachOther(t *testing.T) {
	tier := starting(t).Local()
	ctx := t.Context()

	_, err := tier.Append(ctx, grantFor(auditID), []byte("audit record"))
	require.NoError(t, err)
	_, err = tier.Append(ctx, grantFor(tracesID), []byte("trace record"))
	require.NoError(t, err)

	audit, err := tier.Read(ctx, grantFor(auditID), "", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"audit record"}, payloads(audit))

	traces, err := tier.Read(ctx, grantFor(tracesID), "", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"trace record"}, payloads(traces))
}

// TestAnAppendOffTheLeaderIsRefusedAndNamesWhoMayWrite.
//
// Nothing forwards — that is pkg/queue's rule and node/queue.go's — so what the
// caller gets is an arm it RETRIES, with the node to retry at on it.
func TestAnAppendOffTheLeaderIsRefusedAndNamesWhoMayWrite(t *testing.T) {
	h := starting(t)
	ctx := t.Context()

	_, err := h.Local().Append(ctx, grantFor(auditID), []byte("before"))
	require.NoError(t, err)

	h.cluster.stepDown()

	_, err = h.Local().Append(ctx, grantFor(auditID), []byte("after"))
	require.ErrorIs(t, err, wasmtier.ErrNotWritable,
		"a guest must be told to retry, not that the log is gone")

	var elsewhere NotLeading
	require.ErrorAs(t, err, &elsewhere)
	require.NotEmpty(t, elsewhere.Leader, "a refusal that names nobody is only a retry")

	// READING IS NOT WRITING. A follower's state is what the cluster committed,
	// so a reader is served from it — which is what keeps one node's leadership
	// from being an outage for everybody else.
	read, err := h.Local().Read(ctx, grantFor(auditID), "", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"before"}, payloads(read))
}

// TestARecordLargerThanTheCeilingIsRefusedBeforeItIsWritten. The ceiling is the
// ABI's — a record crosses as one list<u8> — so the refusal is about the record
// and not about the log.
func TestARecordLargerThanTheCeilingIsRefusedBeforeItIsWritten(t *testing.T) {
	tier := starting(t).Local()
	ctx := t.Context()

	_, err := tier.Append(ctx, grantFor(auditID), make([]byte, MaxRecord+1))
	require.ErrorIs(t, err, wasmtier.ErrRecordTooLarge)

	read, err := tier.Read(ctx, grantFor(auditID), "", 0)
	require.NoError(t, err)
	require.Empty(t, read, "a refused append must leave no position behind")
}

// TestACursorThisLogNeverIssuedIsItsOwnArm, because a caller FIXES it by
// starting from the beginning rather than retrying it.
func TestACursorThisLogNeverIssuedIsItsOwnArm(t *testing.T) {
	tier := starting(t).Local()

	_, err := tier.Read(t.Context(), grantFor(auditID), "not-a-cursor!", 0)
	require.ErrorIs(t, err, wasmtier.ErrBadCursor)
}

// TestAReaderBehindTheFloorIsToldSoRatherThanHandedNothing. "You are past this
// log's retention" and "you are caught up" are different facts and a reader
// acts on them differently.
func TestAReaderBehindTheFloorIsToldSoRatherThanHandedNothing(t *testing.T) {
	h := starting(t)
	tier := h.Local()
	ctx := t.Context()
	traces := grantFor(tracesID)

	for _, line := range []string{"one", "two", "three"} {
		_, err := tier.Append(ctx, traces, []byte(line))
		require.NoError(t, err)
	}
	first, err := tier.Read(ctx, traces, "", 0)
	require.NoError(t, err)

	// `traces` keeps an hour, and two have passed.
	h.clock.advance(2 * time.Hour)
	h.cycle(ctx)

	_, err = tier.Read(ctx, traces, first[0].Cursor, 0)
	require.ErrorIs(t, err, wasmtier.ErrBadCursor)
}

// TestAnAppendThatWasNeverPublishedTakesItsRecordBackWithIt.
//
// The record lands before the head moves, and under its own digest, so a leader
// whose commit is then refused leaves bytes at a key NO POSITION NAMES. Nothing
// reads it — every read resolves a position through raft — and nothing collects
// it either: retention deletes the digest raft named, not everything sitting at
// a position. The node that wrote it is the only one that can take it back.
func TestAnAppendThatWasNeverPublishedTakesItsRecordBackWithIt(t *testing.T) {
	cluster := &brittle{fakeCluster: newCluster()}
	h := startingOn(t, cluster)
	ctx := t.Context()
	audit := grantFor(auditID)

	_, err := h.Local().Append(ctx, audit, []byte("published"))
	require.NoError(t, err)

	cluster.refuseWrites(true)
	_, err = h.Local().Append(ctx, audit, []byte("never published"))
	require.Error(t, err, "a record whose head never moved is not an appended record")
	cluster.refuseWrites(false)

	require.Len(t, h.stored(t, auditID), 1,
		"a record nothing points at must not be left in the store")

	// AND THE LOG IS STILL THE LOG. The refused append neither published a
	// position nor took the one before it with it.
	read, err := h.Local().Read(ctx, audit, "", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"published"}, payloads(read))
}

// TestAStoreThatCannotAnswerIsNotAReaderThatIsCaughtUp.
//
// A short page means caught up — that is this call's contract, and it is what a
// reader polls on. So a node whose record store is sick must not answer one: it
// would tell every reader it had reached the end of the log, and the records
// they never saw are records they never ask for again.
func TestAStoreThatCannotAnswerIsNotAReaderThatIsCaughtUp(t *testing.T) {
	h := starting(t)
	ctx := t.Context()
	audit := grantFor(auditID)

	for _, line := range []string{"created", "paid"} {
		_, err := h.Local().Append(ctx, audit, []byte(line))
		require.NoError(t, err)
	}

	h.stores.sicken(true)
	read, err := h.Local().Read(ctx, audit, "", 0)
	require.Error(t, err, "a store that cannot answer must not answer \"nothing more\"")
	require.Empty(t, read)

	// AND IT IS NOT A FACT ABOUT THE LOG. The store recovers and the records are
	// where they were.
	h.stores.sicken(false)
	read, err = h.Local().Read(ctx, audit, "", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"created", "paid"}, payloads(read))
}

// TestACursorPastTheLastPositionALogCanHoldIsRefused. A cursor names the record
// a reader already has, so the answer for the last position a log could ever
// issue is a refusal — wrapping to zero would replay the whole log from the
// floor to a caller that asked for what comes after the end.
func TestACursorPastTheLastPositionALogCanHoldIsRefused(t *testing.T) {
	tier := starting(t).Local()

	_, err := tier.Read(t.Context(), grantFor(auditID), encodeCursor(math.MaxUint64), 0)
	require.ErrorIs(t, err, wasmtier.ErrBadCursor)
}

func payloads(records []wasmtier.Record) []string {
	out := make([]string, 0, len(records))
	for _, record := range records {
		out = append(out, string(record.Data))
	}
	return out
}
