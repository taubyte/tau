package node

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// What is left here is the wiring: node/model speaks the three formats and its
// own tests hold them, and what only this package can say is WHEN each of the
// backing's two collaborators exists.

// TestTHEBACKINGREADSTHENODELATERATHERTHANATCONSTRUCTION. Both halves are
// functions and not values copied out of the node: a vault and a placement router
// are wired while services start, so a backing built during composition would
// capture nil and keep it.
func TestTheInferenceBackingReadsTheNodeLate(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register}})

	// Built BEFORE anything wired a resolver or a router.
	backing, err := n.inference()
	require.NoError(t, err)
	require.NotNil(t, backing)

	require.Nil(t, n.secretResolver(), "nothing resolves a secret on this node")
	require.Nil(t, n.inferenceWorkers(), "and nothing places a connection")

	// A node that acquires one answers with it, through the same function the
	// backing holds.
	n.vault = newVault(n)
	require.Equal(t, n.vault, n.secretResolver())

	// AND A NIL ROUTER ARRIVES AS A NIL INTERFACE, which is the whole reason
	// inferenceWorkers exists: n.workers is a concrete pointer, and a nil one put
	// in an interface is not nil.
	with, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Discovery}})
	require.NoError(t, with.Start(t.Context()))
	require.NotNil(t, with.inferenceWorkers(), "a node with placement can reach a port")
}
