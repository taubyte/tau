package worker

import (
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/project"
)

// A CONNECTION CROSSING A REAL libp2p STREAM, between two real hosts. The
// harness's mocknet carries it, the worker's own raw listener answers it, and
// what comes back is what the workload wrote — so a header read wrong, a length
// prefix off by one, or a splice that swallowed the first bytes all fail here.

func dbTarget() project.Address {
	return project.Address{Project: "shop", Environment: "prod", Kind: "application", Name: "db"}
}

// TestBytesCrossAStreamToAWorkloadAndBack.
func TestAConnectionReachesAWorkload(t *testing.T) {
	h := newHarness(t)

	conn, err := DialWorkload(t.Context(), h.CallerHost, h.ServerHost.ID(), dbTarget(), "pg")
	require.NoError(t, err)
	defer conn.Close()

	_, err = conn.Write([]byte("select 1"))
	require.NoError(t, err)

	// HALF-CLOSE AND THEN READ, which is the ordinary shape of a query: the
	// client has finished sending and is waiting to be answered. A splice that
	// took the first EOF as a close would end the connection here and the
	// answer would never arrive.
	require.NoError(t, conn.(interface{ CloseWrite() error }).CloseWrite())

	answer, err := io.ReadAll(conn)
	require.NoError(t, err)
	require.Equal(t, "PG:SELECT 1", string(answer),
		"the port it was told, and the bytes it was sent, both crossed")
}

// TestTHEPORTNAMEREACHESTheFarSide — a connection that ignored it would still
// carry bytes, which is why the fixture echoes the name back.
func TestTheNamedPortTravels(t *testing.T) {
	h := newHarness(t)

	conn, err := DialWorkload(t.Context(), h.CallerHost, h.ServerHost.ID(), dbTarget(), "admin")
	require.NoError(t, err)
	defer conn.Close()

	require.NoError(t, conn.(interface{ CloseWrite() error }).CloseWrite())
	answer, err := io.ReadAll(conn)
	require.NoError(t, err)
	require.Equal(t, "ADMIN:", string(answer))
}

// TestATARGETThatResolvesNowhereIsRefusedAndNotRetried. A name that resolves
// nowhere resolves nowhere on every node, so walking the fleet to be told so N
// times is a slower way to reach the same answer.
func TestAMissingTargetIsNotWorthAnotherNode(t *testing.T) {
	h := newHarness(t)

	_, err := DialWorkload(t.Context(), h.CallerHost, h.ServerHost.ID(),
		project.Address{Project: "shop", Environment: "prod", Kind: "application", Name: "ghost"}, "pg")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNoConnection)
	require.NotErrorIs(t, err, ErrUnreachableNode, "another node would say the same thing")
}

// TestAWORKLOADThatCannotGiveTheSocketIsWorthAnotherNode, which is the other
// half of the same distinction: this node failed, not the name.
func TestAFailedDialIsWorthAnotherNode(t *testing.T) {
	h := newHarness(t)

	_, err := DialWorkload(t.Context(), h.CallerHost, h.ServerHost.ID(), dbTarget(), "nope")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrUnreachableNode)
}

// TestTheRAWProtocolIsNotTheRPCOne, so neither can be mistaken for the other on
// a peer's protocol list — and a caller speaking one at the other fails rather
// than being half-understood.
func TestTheStreamProtocolIsItsOwn(t *testing.T) {
	require.NotEqual(t, Protocol, StreamProtocol)
	require.Contains(t, string(StreamProtocol), string(Protocol))
}
