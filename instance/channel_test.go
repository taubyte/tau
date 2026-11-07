package instance

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	pkgregister "github.com/taubyte/tau/pkg/register"
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

func TestAChannelWithNoServerIsNoAnswer(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Worker}})
	c := &channels{node: n}

	grant := channelGrant()
	grant.Settings = map[string]string{"protocol": "reserve"}

	_, err := c.Dial(t.Context(), grant, []byte("x"))
	require.ErrorIs(t, err, wasmtier.ErrNoAnswer)
}

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

func TestANodeThatCanReachNoWorkerAnswersNothing(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register}})
	require.Nil(t, n.worker, "this node runs none")
	require.Nil(t, n.workers, "and runs no stats to place through")

	_, err := (&channels{node: n}).Dial(t.Context(), channelGrant(), []byte("x"))
	require.ErrorIs(t, err, wasmtier.ErrNoAnswer)
}

// dialingNode is a worker with no stats — so a dial goes to its own worker,
// the only workload it can reach — serving the fixture's checkout function.
func dialingNode(t *testing.T, answers *answering) *channels {
	t.Helper()
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker}}, WithRuntimes(answers), prebuilt())
	require.NoError(t, n.Start(t.Context()))
	require.Nil(t, n.workers, "no stats, so nothing to place through")
	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled(),
	})
	require.NoError(t, err)
	return &channels{node: n}
}

func TestAChannelToAFunctionIsItsAnswer(t *testing.T) {
	c := dialingNode(t, &answering{name: "wasm", status: 200})

	got, err := c.Dial(t.Context(), channelGrant(), []byte("x"))
	require.NoError(t, err)
	require.Equal(t, []byte("200"), got)
}

func TestAFailingServerIsAFailedChannel(t *testing.T) {
	c := dialingNode(t, &answering{name: "wasm", status: 503})

	_, err := c.Dial(t.Context(), channelGrant(), []byte("x"))
	require.ErrorIs(t, err, wasmtier.ErrChannelFailed)
	require.Contains(t, err.Error(), "503")
}

func TestAConnectedKindIsDialled(t *testing.T) {
	grant := channelGrant()
	grant.Settings["serve/kind"] = "application"
	grant.Settings["serve/name"] = "absent"

	n, _, _ := makeNode(t, Config{Services: []string{Register}})
	_, err := (&channels{node: n}).Dial(t.Context(), grant, []byte("x"))
	require.ErrorIs(t, err, wasmtier.ErrNoAnswer)
	require.Contains(t, err.Error(), "application/absent")

	c := dialingNode(t, &answering{name: "wasm", status: 200})
	_, err = c.Dial(t.Context(), grant, []byte("x"))
	require.ErrorIs(t, err, wasmtier.ErrNoAnswer)
	require.Contains(t, err.Error(), "application/absent")
	require.NotContains(t, err.Error(), "dialling")
}

func TestAFunctionNothingDeploysIsNoAnswer(t *testing.T) {
	c := dialingNode(t, &answering{name: "wasm", status: 200})
	grant := channelGrant()
	grant.Settings["serve/name"] = "absent"

	_, err := c.Dial(t.Context(), grant, []byte("x"))
	require.ErrorIs(t, err, wasmtier.ErrNoAnswer)
}

func TestAPlacedDialToNothingIsNoAnswer(t *testing.T) {
	n, _ := consuming(t, &answering{name: "wasm", status: 200}, compiled())
	require.NotNil(t, n.workers)
	grant := channelGrant()
	grant.Settings["serve/kind"] = "application"
	grant.Settings["serve/name"] = "absent"

	_, err := (&channels{node: n}).Dial(t.Context(), grant, []byte("x"))
	require.ErrorIs(t, err, wasmtier.ErrNoAnswer)
}
