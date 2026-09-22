package primitives

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"connectrpc.com/connect"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	kvdbmock "github.com/taubyte/tau/pkg/kvdb/mock"
	"github.com/taubyte/tau/pkg/raft"
)

// rpc_test drives the service over a real libp2p stream, because the thing
// under test is a SERVICE: what a caller can reach, what it is refused, and
// whether the arm it gets is the one a local caller would have got.

// wired is the service on one host and a client on another, over mocknet.
func wired(t *testing.T) (*Client, *harness) {
	t.Helper()
	return wiredWith(t)
}

// wiredWith is the same with the composition the case is about — a clock, for
// the tasking, which the queue and log cases have no use for.
func wiredWith(t *testing.T, opts ...Option) (*Client, *harness) {
	t.Helper()

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	server, err := mn.GenPeer()
	require.NoError(t, err)
	caller, err := mn.GenPeer()
	require.NoError(t, err)
	require.NoError(t, mn.LinkAll())
	require.NoError(t, mn.ConnectAllButSelf())

	cluster := newCluster()
	tick := newClock()

	s, err := New(server, append([]Option{
		WithRegister(&published{revision: 1, object: compiled()}),
		WithConsensus(func(string) (raft.Cluster, error) { return cluster, nil }),
		WithRecords(kvdbmock.New()),
		WithClock(tick.now),
		WithSweep(time.Hour),
	}, opts...)...)
	require.NoError(t, err)
	require.NoError(t, s.Start(t.Context()))
	t.Cleanup(func() { s.Close() })

	s.formMu.Lock()
	forming := s.forming
	s.formMu.Unlock()
	<-forming

	client, err := NewClient(caller, server.ID())
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	return client, &harness{Service: s, cluster: cluster, clock: tick}
}

// TestTheWireAnswersLikeTheSeamItReplaces.
//
// A node that runs this service reaches it in-process and one that does not
// dials it, and NOTHING ABOVE THE SEAM CAN TELL WHICH IT GOT. That is the whole
// point of the swap: the guest calls queue.send and log.append exactly as
// before.
func TestTheWireAnswersLikeTheSeamItReplaces(t *testing.T) {
	client, _ := wired(t)
	ctx := t.Context()

	audit := grantFor(auditID)
	cursor, err := client.Append(ctx, audit, []byte("created"))
	require.NoError(t, err)
	require.NotEmpty(t, cursor)

	_, err = client.Append(ctx, audit, []byte("paid"))
	require.NoError(t, err)

	read, err := client.Read(ctx, audit, "", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"created", "paid"}, payloads(read))

	rest, err := client.Read(ctx, audit, cursor, 0)
	require.NoError(t, err)
	require.Equal(t, []string{"paid"}, payloads(rest))

	jobs := grantFor(jobsID)
	require.NoError(t, client.Send(ctx, jobs, "job-1", []byte("resize")))

	claimed, err := client.Claim(ctx, jobs, 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, []byte("resize"), claimed[0].Data)
	require.False(t, claimed[0].Deadline.IsZero(), "a claim with no deadline never expires")

	// THE TOKEN ROUND-TRIPS VERBATIM, which is the only thing stopping an
	// expired consumer from acknowledging work somebody else now holds.
	require.NoError(t, client.Settle(ctx, jobs, claimed[0], true))

	depth, err := client.Depth(ctx, jobs)
	require.NoError(t, err)
	require.EqualValues(t, 0, depth.Ready+depth.Claimed+depth.Dead)
}

// TestAWriteOffTheLeaderReachesTheCallerAsTheArmItRetries. The code is what
// survives the round trip, and this is the table read backwards.
func TestAWriteOffTheLeaderReachesTheCallerAsTheArmItRetries(t *testing.T) {
	client, h := wired(t)
	ctx := t.Context()

	h.cluster.stepDown()

	err := client.Send(ctx, grantFor(jobsID), "job-1", []byte("resize"))
	require.ErrorIs(t, err, wasmtier.ErrNotWritable)
	require.Contains(t, err.Error(), "does",
		"the refusal must still name whoever may write")

	_, err = client.Append(ctx, grantFor(auditID), []byte("created"))
	require.ErrorIs(t, err, wasmtier.ErrNotWritable)
}

// TestAResourceThisDeploymentDoesNotDeclareIsNotFoundOverTheWireEither.
func TestAResourceThisDeploymentDoesNotDeclareIsNotFoundOverTheWireEither(t *testing.T) {
	client, _ := wired(t)
	ctx := t.Context()
	nobody := grantFor("QmNoSuchResourceIdAnywhereInThisProject00000000")

	_, err := client.Read(ctx, nobody, "", 0)
	require.ErrorIs(t, err, wasmtier.ErrNotFound)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

	err = client.Send(ctx, nobody, "job-1", []byte("work"))
	require.ErrorIs(t, err, wasmtier.ErrNotFound)
}

// TestARefusalThisCloudCannotRetryIsUnimplementedAndNotUnavailable, so an
// operator reading it knows to change the release rather than to wait.
func TestARefusalThisCloudCannotRetryIsUnimplementedAndNotUnavailable(t *testing.T) {
	client, _ := wired(t)

	_, err := client.Append(t.Context(), grantFor(gossipID), []byte("record"))
	require.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
}

// TestAnOversizedRecordIsRefusedOverTheWireWithTheTiersOwnArm.
func TestAnOversizedRecordIsRefusedOverTheWireWithTheTiersOwnArm(t *testing.T) {
	client, _ := wired(t)

	_, err := client.Append(t.Context(), grantFor(auditID), make([]byte, MaxRecord+1))
	require.ErrorIs(t, err, wasmtier.ErrRecordTooLarge)
}

// TestAMalformedHandleNeverLeavesTheCaller. Three empty strings on the wire
// would be a refusal the service has to write; refusing here is the same answer
// without the round trip.
func TestAMalformedHandleNeverLeavesTheCaller(t *testing.T) {
	client, _ := wired(t)

	err := client.Send(t.Context(), invoke.Grant{Handle: "shop"}, "job-1", []byte("work"))
	require.ErrorIs(t, err, ErrNoID)
}

// TestACallBeforeTheConsensusHasFormedIsToldSoRatherThanHeld.
//
// raft.New blocks for up to thirty seconds looking for peers, which is right at
// boot and wrong on a request path — the day the first send in a cloud paid it,
// it held every other caller behind one lock and timed the invocation out.
func TestACallBeforeTheConsensusHasFormedIsToldSoRatherThanHeld(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	host, err := mn.GenPeer()
	require.NoError(t, err)

	s, err := New(host,
		WithRegister(&published{revision: 1, object: compiled()}),
		WithConsensus(func(string) (raft.Cluster, error) { return newCluster(), nil }),
		WithRecords(kvdbmock.New()))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	// Never started, so nothing has begun forming. It is the same instruction
	// as "not yet" and a different fact; both are a caller that should be
	// somewhere else.
	err = s.Local().Send(t.Context(), grantFor(jobsID), "job-1", []byte("resize"))
	require.ErrorIs(t, err, ErrForming)
}

// TestAWINDOWCROSSESTHEWIREANDISGENERATEDWHERITLANDS — which is the whole of
// the clock's second act being a MESSAGE rather than a function call. The
// leader that handed this window out never opened a queue or looked at a
// schedule; the node that took it did both.
func TestAWindowCrossesTheWireAndIsGeneratedWhereItLands(t *testing.T) {
	fired := &fires{status: 200}
	held := &listing{schedules: []Schedule{nightly("0 3 * * *")}}
	client, h := wiredWith(t, WithSchedules(held.of), WithFire(fired.serve))

	// The clock the tasked node reads, because everything a queue decides —
	// when a claim expires, when a backoff is up — is decided against it.
	h.clock.set(at(3, 0))

	require.NoError(t, client.CronTask(t.Context(), at(2, 59), at(3, 0)))
	require.Eventually(t, func() bool { return len(fired.seen()) == 1 },
		5*time.Second, 10*time.Millisecond, "the window crossed and nothing was generated")
	require.Equal(t, []string{"2026-09-05T03:00:00Z"}, fired.seen()[0].Headers[headerFiredAt])
}

// TestAWINDOWTHENODEATTHEOTHERENDCANNOTGENERATECOMESBACKASAREFUSAL, by name
// and over the wire. A node that holds the namespace and runs no compute has no
// deployments to enumerate crons from, and an empty answer would tell the
// leader a minute had been fired that never was.
func TestAWindowHandedOverTheWireToANodeWithNoClockIsRefusedByName(t *testing.T) {
	client, _ := wired(t)

	err := client.CronTask(t.Context(), at(2, 59), at(3, 0))
	require.Error(t, err)
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	require.ErrorContains(t, err, ErrNoClock.Error())
}

// TestTHEHOPCOUNTTRAVELSWITHTHETASKINGANDONLYGOESUP, which is what bounds a
// window to the two nodes the fleet actually has: the one the leader chose, and
// the one its group says may write.
func TestATaskingSaysHowFarItHasCome(t *testing.T) {
	fired := &fires{status: 200}
	held := &listing{schedules: []Schedule{nightly("0 3 * * *")}}
	client, h := wiredWith(t, WithSchedules(held.of), WithFire(fired.serve))
	h.clock.set(at(3, 0))

	// A tasking already at the ceiling arrives at the ceiling, so the node it
	// reaches is the last one that may serve it. This one leads, so it does.
	ctx := hopsIn(t.Context(), http.Header{hopHeader: []string{strconv.Itoa(maxHops - 1)}})
	require.NoError(t, client.CronTask(ctx, at(2, 59), at(3, 0)))
	require.Eventually(t, func() bool { return len(fired.seen()) == 1 },
		5*time.Second, 10*time.Millisecond)

	// AND A FOLLOWER AT THE CEILING REFUSES INSTEAD OF SENDING IT ON, which is
	// the arm the client restores from the wire status: somebody else may
	// write, so the leader retries next minute rather than reading a broken
	// cloud.
	h.cluster.stepDown()
	err := client.CronTask(ctx, at(3, 0), at(3, 1))
	require.ErrorIs(t, err, ErrNotLeading)
}
