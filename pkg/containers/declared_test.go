package containers

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnUndeclaredDaemonIsNone(t *testing.T) {
	require.False(t, Declared{}.Any())
	require.True(t, Declared{Backend: Docker}.Any())
}

func TestTheDaemonIsTheOperatorsChoice(t *testing.T) {
	_, err := Open(Declared{Backend: "not-a-backend"})
	require.ErrorIs(t, err, ErrUnknownBackend)
	require.ErrorContains(t, err, Docker)
	require.ErrorContains(t, err, Containerd)

	// A NAMED ONE IS OPENED RATHER THAN DETECTED, and the two backends differ in
	// when they find out whether the daemon is there: docker builds a client
	// and asks later, containerd checks its socket now. Either way the answer
	// arrives at BOOT rather than at the first push.
	_, err = Open(Declared{Backend: Docker})
	require.NoError(t, err)

	_, err = Open(Declared{Backend: Containerd, Socket: "/nowhere/containerd.sock"})
	if err != nil {
		require.ErrorContains(t, err, "containerd",
			"the message names the socket it looked for, so an operator sees which path was tried")
	}
}
