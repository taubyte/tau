package database

import (
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
)

// local builds a service without starting a listener, because the whole point
// of this surface is that nothing is dialled.
func local(t *testing.T, opts ...Option) *Service {
	t.Helper()

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	host, err := mn.GenPeer()
	require.NoError(t, err)

	registry := database.NewRegistry()
	require.NoError(t, registry.Register(t.Context(), &accounting{}))

	s, err := New(host, append([]Option{
		WithEngines(registry),
		WithRegister(&published{revision: 1, object: compiled()}),
	}, opts...)...)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

// TestTHESAMESURFACEWITHOUTTHEHOP.
//
// A node that runs the database service already holds the engines, so dialling
// itself would add a stream and a serialization to every call. Nothing above
// the seam can tell which it got, which is what this asserts: the same
// operations, the same answers, the same miss.
func TestTheLocalSurfaceAnswersLikeTheWire(t *testing.T) {
	kvs := local(t).Local()
	ctx := t.Context()

	kv, err := kvs.OpenKV(ctx, sessionsGrant())
	require.NoError(t, err)

	_, err = kv.Get(ctx, "absent")
	require.ErrorIs(t, err, wasmtier.ErrNotFound, "a miss must be the tier's miss, not an outage")

	require.NoError(t, kv.Put(ctx, "session:1", []byte("alice")))
	value, err := kv.Get(ctx, "session:1")
	require.NoError(t, err)
	require.Equal(t, []byte("alice"), value)

	require.NoError(t, kv.Put(ctx, "session:2", []byte("bob")))
	keys, err := kv.Keys(ctx, "session:")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"session:1", "session:2"}, keys)

	require.NoError(t, kv.Delete(ctx, "session:1"))
	_, err = kv.Get(ctx, "session:1")
	require.ErrorIs(t, err, wasmtier.ErrNotFound)
}

func TestTheLocalSurfaceRefusesAMalformedHandle(t *testing.T) {
	kvs := local(t).Local()

	for _, handle := range []string{"", "shop", "shop/prod/id/extra"} {
		_, err := kvs.OpenKV(t.Context(), invoke.Grant{Handle: handle})
		require.ErrorIs(t, err, ErrNoID, "%q", handle)
	}
}

// TestWHATTHISCLOUDSERVESISREFUSEDBYNAME, so an operator reading the error
// knows whether to change the config or the cloud.
func TestTheLocalSurfaceSaysWhyItRefuses(t *testing.T) {
	kvs := local(t).Local()
	ctx := t.Context()

	// A sql database reached through kv: the CALLER's mistake.
	wrong, err := kvs.OpenKV(ctx, invoke.Grant{Handle: projectID + "/prod/" + sqlID})
	require.NoError(t, err)
	_, err = wrong.Get(ctx, "k")
	require.ErrorIs(t, err, ErrModelUnserved)

	// A consensus this cloud does not keep: not the caller's mistake at all.
	raft, err := kvs.OpenKV(ctx, invoke.Grant{Handle: projectID + "/prod/" + raftID})
	require.NoError(t, err)
	_, err = raft.Get(ctx, "k")
	require.ErrorIs(t, err, ErrConsensusUnserved)

	// An id this deployment does not name.
	missing, err := kvs.OpenKV(ctx, invoke.Grant{Handle: projectID + "/prod/QmNope"})
	require.NoError(t, err)
	_, err = missing.Get(ctx, "k")
	require.ErrorIs(t, err, ErrNotDeclared)
}

// TestEVERYREFUSALCROSSESTHEWIREASITSOWNCODE, or a caller retries what will
// never succeed and gives up on what would.
func TestTheWireCodeSaysWhatToDoAboutIt(t *testing.T) {
	for _, c := range []struct {
		err  error
		want connect.Code
	}{
		{ErrNoProject, connect.CodeInvalidArgument},
		{ErrNoEnvironment, connect.CodeInvalidArgument},
		{ErrNoID, connect.CodeInvalidArgument},
		{ErrNoKey, connect.CodeInvalidArgument},
		{ErrNotADatabase, connect.CodeFailedPrecondition},
		{ErrWrongModel, connect.CodeFailedPrecondition},

		// NOTHING THE CALLER CAN CHANGE MAKES THESE SUCCEED: the fix is a
		// release, not a retry.
		{ErrModelUnserved, connect.CodeUnimplemented},
		{ErrConsensusUnserved, connect.CodeUnimplemented},

		{ErrNotDeclared, connect.CodeNotFound},
		{ErrNotSet, connect.CodeNotFound},
	} {
		var wired *connect.Error
		require.True(t, errors.As(wire(c.err), &wired), "%v", c.err)
		require.Equal(t, c.want, wired.Code(), "%v", c.err)
	}

	// AND AN ERROR NOBODY NAMED IS UNAVAILABLE, which is a retry — the safe
	// default for something this package does not recognise.
	var wired *connect.Error
	require.True(t, errors.As(wire(errors.New("the disk is on fire")), &wired))
	require.Equal(t, connect.CodeUnavailable, wired.Code())
}

// TestTHEOPTIONSAREREFUSEDWHENTHEYCANNOTBEMET, at construction rather than on
// the call that would have found out.
func TestTheServiceRefusesOptionsItCannotHonour(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	host, err := mn.GenPeer()
	require.NoError(t, err)

	registry := database.NewRegistry()
	require.NoError(t, registry.Register(t.Context(), &accounting{}))
	good := []Option{WithEngines(registry), WithRegister(&published{revision: 1, object: compiled()})}

	for _, bad := range []Option{
		WithIdle(0),
		WithIdle(-time.Second),
		WithClock(nil),
	} {
		_, err := New(host, append(append([]Option{}, good...), bad)...)
		require.Error(t, err)
	}

	// A service with no engines can answer nothing, and says so here rather
	// than answering every call with the same refusal.
	_, err = New(host, WithRegister(&published{revision: 1, object: compiled()}))
	require.ErrorIs(t, err, ErrNoEngines)

	// And one that cannot read declarations would be a service any caller
	// could point somewhere else.
	_, err = New(host, WithEngines(registry))
	require.ErrorIs(t, err, ErrNoRegister)
}

// TestTHEOPTIONSTHATARETAKEN, so a composer's choices reach the service rather
// than being silently the default.
func TestTheServiceTakesWhatItIsGiven(t *testing.T) {
	s := local(t,
		WithSweep(17*time.Second),
		WithMaxResident(3),
		WithRegion("eu"),
		WithClock(func() time.Time { return time.Unix(0, 0) }),
	)

	require.Equal(t, 17*time.Second, s.sweep)
	require.Equal(t, "eu", s.region)
	require.Zero(t, s.Held(), "nothing is open until something is asked for")
}
