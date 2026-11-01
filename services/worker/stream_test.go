package worker

import (
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/specs"
)

// A connection across a real libp2p stream, answered by the worker's raw listener.

func dbTarget() specs.Address {
	return specs.Address{Project: "shop", Environment: "prod", Kind: "application", Name: "db"}
}

func TestAConnectionReachesAWorkload(t *testing.T) {
	h := newHarness(t)

	conn, err := DialWorkload(t.Context(), h.CallerHost, h.ServerHost.ID(), dbTarget(), "pg")
	require.NoError(t, err)
	defer conn.Close()

	_, err = conn.Write([]byte("select 1"))
	require.NoError(t, err)

	// Half-close then read, the shape of a query; a splice treating the first EOF as a
	// close would never deliver the answer.
	require.NoError(t, conn.(interface{ CloseWrite() error }).CloseWrite())

	answer, err := io.ReadAll(conn)
	require.NoError(t, err)
	require.Equal(t, "PG:SELECT 1", string(answer),
		"the port it was told, and the bytes it was sent, both crossed")
}

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

func TestAMissingTargetIsNotWorthAnotherNode(t *testing.T) {
	h := newHarness(t)

	_, err := DialWorkload(t.Context(), h.CallerHost, h.ServerHost.ID(),
		specs.Address{Project: "shop", Environment: "prod", Kind: "application", Name: "ghost"}, "pg")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNoConnection)
	require.NotErrorIs(t, err, ErrUnreachableNode, "another node would say the same thing")
}

func TestAFailedDialIsWorthAnotherNode(t *testing.T) {
	h := newHarness(t)

	_, err := DialWorkload(t.Context(), h.CallerHost, h.ServerHost.ID(), dbTarget(), "nope")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrUnreachableNode)
}

func TestTheStreamProtocolIsItsOwn(t *testing.T) {
	require.NotEqual(t, Protocol, StreamProtocol)
	require.Contains(t, string(StreamProtocol), string(Protocol))
}
