package node

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
)

// channelGrant is what the composer writes onto a `dial`: the resolved server,
// not the name the author wrote. See services/worker's servedBy for where these
// keys come from and its own tests for what fills them.
func channelGrant() invoke.Grant {
	return invoke.Grant{
		Kind:   "channel",
		Label:  "reserve",
		Handle: "shop/prod/QmPZ9gcCEpqKTo6aq61g2nXGUhM4iCL3ewB6LDXZCtioED",
		Ops:    []string{"dial"},
		Settings: map[string]string{
			"protocol":        "reserve",
			"serve/kind":      "function",
			"serve/name":      "checkout",
			"serve/namespace": "/",
		},
	}
}

// TestAChannelWithNoServerIsNoAnswerAndNotAnError — a grant that names nothing
// to reach is the far end not being there, which is the arm a caller RETRIES.
// It is not `unavailable`, which is this cloud being unable to serve the kind
// at all, and telling them apart is the whole reason the variant has both.
func TestAChannelWithNoServerIsNoAnswer(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker}})
	c := &channels{node: n}

	grant := channelGrant()
	grant.Settings = map[string]string{"protocol": "reserve"}

	_, err := c.Dial(t.Context(), grant, []byte("x"))
	require.ErrorIs(t, err, wasmtier.ErrNoAnswer)
}

// TestAHandleThatNamesNoDeploymentIsRefused. The handle is where the project
// and environment come from, so one that is not a deployment cannot be routed
// anywhere — and answering "nothing serves it" would blame the far end for a
// composition fault.
func TestAHandleThatNamesNoDeploymentIsRefused(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker}})
	c := &channels{node: n}

	grant := channelGrant()
	grant.Handle = "nonsense"

	_, err := c.Dial(t.Context(), grant, []byte("x"))
	require.Error(t, err)
	require.False(t, errors.Is(err, wasmtier.ErrNoAnswer), "this is not the far end's fault")
	require.Contains(t, err.Error(), "does not name a deployment")
}

// TestANodeThatRunsNoWorkerAndCannotRouteAnswersNothing — a dial has to reach a
// workload somewhere, and a node with neither a worker of its own nor a way to
// find one has nowhere to send it. It says so rather than hanging.
func TestANodeThatCanReachNoWorkerAnswersNothing(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register}})
	require.Nil(t, n.worker, "this node runs none")
	require.Nil(t, n.workers, "and runs no discovery to find one")

	_, err := (&channels{node: n}).Dial(t.Context(), channelGrant(), []byte("x"))
	require.ErrorIs(t, err, wasmtier.ErrNoAnswer)
}
