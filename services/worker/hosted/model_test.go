package hosted

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Only the wiring: when each of the backing's collaborators exists.

func TestTheInferenceBackingReadsTheWorkerLate(t *testing.T) {
	n := New()

	// Built BEFORE anything wired a vault or a router.
	backing, err := n.inference()
	require.NoError(t, err)
	require.NotNil(t, backing)

	// AND A NIL ROUTER ARRIVES AS A NIL INTERFACE, which is the whole reason
	// inferenceWorkers exists: n.workers is a concrete pointer, and a nil one put in
	// an interface is not nil.
	require.Nil(t, n.inferenceWorkers(), "nothing places a connection yet")

	with, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}})
	require.NotNil(t, with.inferenceWorkers(), "a worker with placement can reach a port")
	require.NotNil(t, with.vault, "and reads secrets through its vault")
}
