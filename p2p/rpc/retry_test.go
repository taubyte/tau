package rpc_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	echov1 "github.com/taubyte/tau/p2p/rpc/testdata/gen/echo/v1"
	"github.com/taubyte/tau/p2p/rpc/testdata/gen/echo/v1/echov1connect"
)

// patient is a retry long enough for something to come up while a test waits.
var patient = rpc.Retry{First: 5 * time.Millisecond, Max: 20 * time.Millisecond, For: 5 * time.Second}

func TestARetryFillsItsDefaultsAndRefusesWhatCannotWork(t *testing.T) {
	d := rpc.Retry{}.Defaults()
	require.Equal(t, rpc.DefaultRetryFirst, d.First)
	require.Equal(t, rpc.DefaultRetryMax, d.Max)
	require.Equal(t, rpc.DefaultRetryFor, d.For)
	require.Equal(t, rpc.Retry{First: time.Second, Max: time.Minute, For: time.Hour}, rpc.Retry{First: time.Second, Max: time.Minute, For: time.Hour}.Defaults())

	require.NoError(t, rpc.Retry{}.Check())
	require.Error(t, rpc.Retry{First: -time.Second}.Check())
	require.Error(t, rpc.Retry{For: -time.Second}.Check())
	require.Error(t, rpc.Retry{First: time.Minute, Max: time.Second}.Check())
}

func TestAServiceNobodyServesYetIsWaitedFor(t *testing.T) {
	h := newHost(t)
	other := newHost(t)
	var found atomic.Bool
	pick := func(context.Context) (peer.ID, error) {
		if found.Load() {
			return other.ID(), nil
		}
		return "", errNobody
	}
	d := newDials()
	r := rpc.Reach(h, "register", patient, pick, runsNothing, d.dial)

	time.AfterFunc(50*time.Millisecond, func() { found.Store(true) })
	got, err := r.Now(t.Context())
	require.NoError(t, err, "a call made before the service was anywhere failed instead of waiting")
	require.NotNil(t, got)
	require.Equal(t, 1, d.count())
}

func TestAServiceThisNodeStartsLaterIsWaitedForHere(t *testing.T) {
	h := newHost(t)
	mine := &arm{}
	var started atomic.Bool
	local := func() (*arm, bool) {
		if started.Load() {
			return mine, true
		}
		return nil, false
	}
	r := rpc.Reach(h, "register", patient, nobody, local, refuse(t))

	time.AfterFunc(50*time.Millisecond, func() { started.Store(true) })
	got, err := r.Now(t.Context())
	require.NoError(t, err)
	require.Same(t, mine, got, "the service this node started later was not the one reached")
}

func TestAWaitEndsWhenTheRetryRunsOut(t *testing.T) {
	h := newHost(t)
	r := rpc.Reach(h, "register", rpc.Retry{First: time.Millisecond, Max: time.Millisecond, For: 50 * time.Millisecond}, nobody, runsNothing, refuse(t))

	began := time.Now()
	_, err := r.Now(t.Context())
	require.ErrorIs(t, err, errNobody, "the reason is the route's, said once the retry ran out")
	require.GreaterOrEqual(t, time.Since(began), 50*time.Millisecond, "it gave up before its retry ran out")
}

func TestAWaitEndsWithItsCaller(t *testing.T) {
	h := newHost(t)
	r := rpc.Reach(h, "register", patient, nobody, runsNothing, refuse(t))

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	began := time.Now()
	_, err := r.Now(ctx)
	require.Error(t, err)
	require.Less(t, time.Since(began), time.Second, "a caller that left was kept waiting for the whole retry")
}

func TestACallThatNeverReachedTheServiceIsMadeAgain(t *testing.T) {
	h := newHost(t)
	other := newHost(t)
	d := newDials()
	r := rpc.Reach(h, "register", patient, at(other.ID()), runsNothing, d.dial)

	calls := 0
	err := r.Do(t.Context(), func(*arm) error {
		calls++
		if calls < 3 {
			return unreachedError(t)
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 3, calls)
	require.Equal(t, 3, d.count(), "each attempt after an unreached call opened a fresh client")
}

func TestACallThatReachedTheServiceIsNeverMadeAgain(t *testing.T) {
	h := newHost(t)
	other := newHost(t)
	r := rpc.Reach(h, "register", patient, at(other.ID()), runsNothing, newDials().dial)

	answered := errors.New("the service said no")
	calls := 0
	err := r.Do(t.Context(), func(*arm) error { calls++; return answered })
	require.ErrorIs(t, err, answered)
	require.Equal(t, 1, calls, "a call the service answered was repeated")

	require.ErrorIs(t, rpc.Reach(h, "register", quick, nobody, runsNothing, refuse(t)).Do(t.Context(), func(*arm) error { return nil }), errNobody)
}

func TestARetryThatRunsOutAnswersTheLastUnreachedCall(t *testing.T) {
	h := newHost(t)
	other := newHost(t)
	r := rpc.Reach(h, "register", quick, at(other.ID()), runsNothing, newDials().dial)
	err := r.Do(t.Context(), func(*arm) error { return unreachedError(t) })
	require.True(t, rpc.Unreached(err))
}

// unreachedError is what a call to a node that does not speak the protocol answers.
func unreachedError(t *testing.T) error {
	t.Helper()
	from, to := twoHosts(t)
	conn, err := rpc.NewClient(from, to.ID(), testProtocol)
	require.NoError(t, err)
	t.Cleanup(conn.Close)
	_, err = echov1connect.NewEchoServiceClient(conn.HTTP(), conn.URL()).Unary(t.Context(), connect.NewRequest(&echov1.EchoRequest{}))
	require.Error(t, err)
	return err
}

func TestACallToANodeThatDoesNotServeTheProtocolNeverReachedIt(t *testing.T) {
	err := unreachedError(t)
	require.True(t, rpc.Unreached(err), "a refused stream was taken for an answer: %v", err)
	require.Contains(t, err.Error(), "not reached")

	h := newHarness(t)
	_, err = h.client.Fail(t.Context(), connect.NewRequest(&echov1.EchoRequest{}))
	require.Error(t, err)
	require.False(t, rpc.Unreached(err), "an answer from the service was taken for an unreached call")
}

// A NODE STILL LISTED AS SERVING A PROTOCOL IT DOES NOT SERVE is handed the stream
// before it answers, so its refusal comes after the dial: on the call, as a reset,
// or as a conn that closed before the call was sent. Which one is timing, so it is
// asked more than once.
func TestACallToANodeListedForAProtocolItDoesNotServeNeverReachedIt(t *testing.T) {
	from, to := twoHosts(t)
	require.Eventually(t, func() bool {
		known, err := from.Peerstore().GetProtocols(to.ID())
		return err == nil && len(known) > 0
	}, 5*time.Second, 10*time.Millisecond, "identify never told the caller what the node serves")
	require.NoError(t, from.Peerstore().AddProtocols(to.ID(), testProtocol))

	for range 20 {
		conn, err := rpc.NewClient(from, to.ID(), testProtocol)
		require.NoError(t, err)
		_, err = echov1connect.NewEchoServiceClient(conn.HTTP(), conn.URL()).Unary(t.Context(), connect.NewRequest(&echov1.EchoRequest{}))
		conn.Close()
		require.True(t, rpc.Unreached(err), "a refusal after the dial was taken for an answer: %v", err)
	}
}
