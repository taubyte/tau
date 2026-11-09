package rpc_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
)

// reach_test.go holds the assertion every service used to repeat: a call that
// belongs in this process opens nothing. It is asserted here once, on the
// mechanism, so a service converting to it inherits the guarantee instead of
// re-proving it.

// errNobody is what a view with nothing in it hands back, standing in for the
// ErrNo… sentinel every composition has one of.
var errNobody = errors.New("nothing runs it")

// arm is what either half of a reach hands back. It counts its own closing,
// because "the stream was released" is otherwise invisible from outside.
type arm struct {
	name   string
	mu     sync.Mutex
	closed int
}

func (a *arm) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed++
	return nil
}

func (a *arm) closes() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.closed
}

// dials records every peer a reach dialled, and hands back one arm per peer so
// a caller can tell a kept client from a re-dialled one.
type dials struct {
	mu   sync.Mutex
	made []peer.ID
	arms map[peer.ID]*arm
}

func newDials() *dials { return &dials{arms: map[peer.ID]*arm{}} }

func (d *dials) dial(id peer.ID) (*arm, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.made = append(d.made, id)
	a := &arm{name: "remote " + id.String()}
	d.arms[id] = a
	return a, nil
}

func (d *dials) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.made)
}

// refuse is a dial that must never happen. It is the shape of the whole point:
// a test that passes because nothing called it.
func refuse(t *testing.T) func(peer.ID) (*arm, error) {
	return func(id peer.ID) (*arm, error) {
		t.Helper()
		t.Fatalf("it dialled %s from the host that already runs the service", id)
		return nil, nil
	}
}

func at(id peer.ID) func(context.Context) (peer.ID, error) {
	return func(context.Context) (peer.ID, error) { return id, nil }
}

func nobody(context.Context) (peer.ID, error) { return "", errNobody }

// quick is a retry short enough for a test that expects to be refused.
var quick = rpc.Retry{First: time.Millisecond, Max: time.Millisecond, For: 20 * time.Millisecond}

func runs(a *arm) func() (*arm, bool) {
	return func() (*arm, bool) { return a, true }
}

func runsNothing() (*arm, bool) { return nil, false }

// TestAROUTETOTHISHOSTCONSTRUCTSNOCLIENT — the short-circuit is structural. A
// reach whose route names the host it reaches FROM hands back the local value
// and dials nothing, which is what v1's client-pointed-at-itself did not.
func TestARouteToThisHostConstructsNoClient(t *testing.T) {
	h := newHost(t)
	mine := &arm{name: "local"}

	r := rpc.ReachRouted(h, "clock", quick, at(h.ID()), runs(mine), refuse(t))
	defer r.Close()

	got, err := r.Now(t.Context())
	require.NoError(t, err)
	assert.Same(t, mine, got, "the local service is what the caller got")

	_, held := r.At()
	assert.False(t, held, "it holds a client to reach a service in its own process")
}

// TestAREACHTHATPREFERSLOCALNEVERASKSWHOELSE, which is why a node holding what
// it needs works with no view of a fleet at all.
func TestAReachThatPrefersLocalNeverAsksWhoElse(t *testing.T) {
	h := newHost(t)
	mine := &arm{name: "local"}

	asked := false
	pick := func(context.Context) (peer.ID, error) {
		asked = true
		return "", errNobody
	}

	r := rpc.Reach(h, "register", quick, pick, runs(mine), refuse(t))
	defer r.Close()

	got, err := r.Now(t.Context())
	require.NoError(t, err)
	assert.Same(t, mine, got)
	assert.False(t, asked, "it routed a call it could answer in this process")
}

// TestANODETHATRUNSNONEDIALSTHEONETHEROUTENAMES.
func TestANodeThatRunsNoneDialsTheOneTheRouteNames(t *testing.T) {
	h, other := newHost(t), newHost(t)
	d := newDials()

	r := rpc.Reach(h, "register", quick, at(other.ID()), runsNothing, d.dial)
	defer r.Close()

	got, err := r.Now(t.Context())
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, 1, d.count())

	id, held := r.At()
	assert.True(t, held)
	assert.Equal(t, other.ID(), id)
}

// TestTHESTREAMISKEPTWHILETHEROUTEHOLDS, so a read on the request path is not
// a dial on the request path.
func TestTheStreamIsKeptWhileTheRouteHolds(t *testing.T) {
	h, other := newHost(t), newHost(t)
	d := newDials()

	r := rpc.Reach(h, "register", quick, at(other.ID()), runsNothing, d.dial)
	defer r.Close()

	first, err := r.Now(t.Context())
	require.NoError(t, err)
	again, err := r.Now(t.Context())
	require.NoError(t, err)

	assert.Same(t, first, again, "a second call opened a second stream")
	assert.Equal(t, 1, d.count())
}

// TestAMOVEDROUTERELEASESWHATITDIALLED. The answer moving is the one signal
// that a held stream is to nobody in particular.
func TestAMovedRouteReleasesWhatItDialled(t *testing.T) {
	h, first, second := newHost(t), newHost(t), newHost(t)
	d := newDials()

	answer := first.ID()
	pick := func(context.Context) (peer.ID, error) { return answer, nil }

	r := rpc.Reach(h, "register", quick, pick, runsNothing, d.dial)
	defer r.Close()

	one, err := r.Now(t.Context())
	require.NoError(t, err)

	answer = second.ID()
	two, err := r.Now(t.Context())
	require.NoError(t, err)

	assert.NotSame(t, one, two)
	assert.Equal(t, 1, one.closes(), "the stream to the node that is no longer the answer was left open")
	assert.Equal(t, 0, two.closes())
	assert.Equal(t, 2, d.count())

	id, held := r.At()
	assert.True(t, held)
	assert.Equal(t, second.ID(), id)
}

// TestANEMPTYROUTEISAREFUSALWHENLOCALISPREFERRED, and the refusal is the
// caller's own: a composition tests for its sentinel and must still find it.
func TestAnEmptyRouteIsARefusalWhenLocalIsPreferred(t *testing.T) {
	h := newHost(t)

	r := rpc.Reach(h, "register", quick, nobody, runsNothing, refuse(t))
	defer r.Close()

	_, err := r.Now(t.Context())
	assert.ErrorIs(t, err, errNobody)
}

// TestANEMPTYROUTEISTHISNODEWHENTHEANSWERISROUTED — a node with no view of a
// fleet is the whole fleet as far as it knows, which is the opposite of the
// answer above and is why there are two constructors.
func TestAnEmptyRouteIsThisNodeWhenTheAnswerIsRouted(t *testing.T) {
	h := newHost(t)
	mine := &arm{name: "local"}

	r := rpc.ReachRouted(h, "clock", quick, nobody, runs(mine), refuse(t))
	defer r.Close()

	got, err := r.Now(t.Context())
	require.NoError(t, err)
	assert.Same(t, mine, got)
}

// TestAROUTEDREACHWITHNOLOCALANDNOROUTEREFUSESWITHTHEPICKSERROR, so a node
// that can neither serve nor find is told which of the two it is.
func TestARoutedReachWithNoLocalAndNoRouteIsRefused(t *testing.T) {
	h := newHost(t)

	r := rpc.ReachRouted(h, "clock", quick, nobody, runsNothing, refuse(t))
	defer r.Close()

	_, err := r.Now(t.Context())
	assert.ErrorIs(t, err, errNobody)
}

// TestAROUTENAMINGTHISHOSTWITHNOTHINGLOCALISREFUSED rather than dialled. The
// dial would be this process to this process — the exact shape the whole file
// exists to end — and it would hide a view claiming this node serves something
// it does not.
func TestARouteNamingThisHostWithNothingLocalIsRefused(t *testing.T) {
	h := newHost(t)

	r := rpc.Reach(h, "register", quick, at(h.ID()), runsNothing, refuse(t))
	defer r.Close()

	_, err := r.Now(t.Context())
	require.ErrorIs(t, err, rpc.ErrNoLocalService)
	assert.ErrorContains(t, err, "register", "the refusal does not name the service")
}

// TestCLOSERELEASESTHESTREAMANDNOTTHELOCALSERVICE. The local value belongs to
// the node that runs it; closing a reach onto it would close a service its
// owner is still using.
func TestCloseReleasesTheStreamAndNotTheLocalService(t *testing.T) {
	h, other := newHost(t), newHost(t)
	d := newDials()
	mine := &arm{name: "local"}

	// One reach that dials, one that short-circuits, closed the same way.
	remote := rpc.Reach(h, "register", quick, at(other.ID()), runsNothing, d.dial)
	dialled, err := remote.Now(t.Context())
	require.NoError(t, err)
	require.NoError(t, remote.Close())
	assert.Equal(t, 1, dialled.closes())

	local := rpc.ReachRouted(h, "clock", quick, at(h.ID()), runs(mine), refuse(t))
	_, err = local.Now(t.Context())
	require.NoError(t, err)
	require.NoError(t, local.Close())
	assert.Equal(t, 0, mine.closes(), "closing the reach closed the service behind it")
}

// TestACALLAFTERCLOSEOPENSANEWSTREAM, so a composition may release its streams
// without ending the reach a still-running node holds.
func TestACallAfterCloseOpensANewStream(t *testing.T) {
	h, other := newHost(t), newHost(t)
	d := newDials()

	r := rpc.Reach(h, "register", quick, at(other.ID()), runsNothing, d.dial)
	defer r.Close()

	_, err := r.Now(t.Context())
	require.NoError(t, err)
	require.NoError(t, r.Close())

	_, err = r.Now(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 2, d.count())
}

// TestACALLERTHATHASALREADYGONEGETSNOSTREAM.
func TestACallerThatHasAlreadyGoneGetsNoStream(t *testing.T) {
	h, other := newHost(t), newHost(t)

	r := rpc.Reach(h, "register", quick, at(other.ID()), runsNothing, refuse(t))
	defer r.Close()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := r.Now(ctx)
	assert.ErrorIs(t, err, context.Canceled)
}

// TestADIALTHATFAILEDISNOTHELD, so the next call tries again rather than
// handing back whatever the failure left behind.
func TestADialThatFailedIsNotHeld(t *testing.T) {
	h, other := newHost(t), newHost(t)

	broken := errors.New("the stream would not open")
	r := rpc.Reach(h, "register", quick, at(other.ID()),
		runsNothing,
		func(peer.ID) (*arm, error) { return nil, broken })
	defer r.Close()

	_, err := r.Now(t.Context())
	require.ErrorIs(t, err, broken)

	_, held := r.At()
	assert.False(t, held)
}
