package worker

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
)

func channelGrants(t *testing.T, spec invoke.Spec) map[string]invoke.Grant {
	t.Helper()

	out := map[string]invoke.Grant{}
	for _, g := range spec.Reaches {
		if g.Kind == channelKind {
			out[g.Label] = g
		}
	}
	return out
}

func TestAChannelGrantCarriesTheResolvedServer(t *testing.T) {
	h := newHarness(t)
	grants := channelGrants(t, resolveIn(t, h, nil, "function", "checkout"))

	require.Contains(t, grants, "reserve")
	serve := grants["reserve"]
	require.Empty(t, serve.Refused, "a function answers it and no port is named")
	require.Equal(t, []string{"dial"}, serve.Ops)

	require.Equal(t, "function", serve.Settings["serve/kind"])
	require.Equal(t, "reserve_stock", serve.Settings["serve/name"])
	require.Equal(t, "reserve", serve.Settings["protocol"],
		"the author's own word for the conduit, so a server reached on two can tell them apart")
}

func TestAnOmittedNamespaceResolvesToTheChannelsScope(t *testing.T) {
	h := newHarness(t)
	grants := channelGrants(t, resolveIn(t, h, nil, "function", "checkout"))

	require.Equal(t, "/", grants["reserve"].Settings["serve/namespace"],
		"the project root, stated rather than left empty for a reader to guess")
}

func TestANamedContainerPortTravelsOnTheGrant(t *testing.T) {
	h := newHarness(t)
	grants := channelGrants(t, resolveIn(t, h, nil, "function", "checkout"))

	require.Len(t, grants, 2)
	sync := grants["sync"]
	require.Empty(t, sync.Refused)
	require.True(t, sync.Allows("dial"))
	require.Equal(t, "service", sync.Settings["serve/kind"])
	require.NotEmpty(t, sync.Settings["serve/port"], "the port's name, for the dial to connect to")
	require.Empty(t, grants["reserve"].Settings["serve/port"], "and a channel naming none carries none")

	require.True(t, sync.Connects(), "a service on the container tier takes the bytes on its socket")
	require.False(t, grants["reserve"].Connects(), "a function is dispatched")
}

// The same kind connects on one tier and is dispatched on another, so how a dial
// lands is the served workload's tier, and a port named on a tier without one is
// refused rather than dialled.
func TestAChannelToAServiceOnWarpIsDispatchedAndNamesNoPort(t *testing.T) {
	h := build(t, Config{}, resolvers()...)
	h.Register.Register = publishedDocs(t, map[string]string{
		"/package/relay_code.yaml": "id: QmV3ceCw39hUuKnd1f6KQhFcwUau324w8YTBC5fm8b1WKd\ntype: warp\n",
		"/service/relay.yaml":      "id: QmVMMKg3r23SLroK7LTp67W5wb77whTbG8wycPbPGejC7H\npackage:\n    name: relay_code\nexecution:\n    memory: 16MB\n",
		"/channel/pipe.yaml":       "id: QmSec27fYecbHmzLEMgEtqgbSpmqoj1dkDq7JJ7XJdNFMv\nprotocol: pipe\nserve:\n    service:\n        name: relay\n",
		"/channel/ported.yaml":     "id: QmUvgtbFcQwTH74rExzuBGePwFW7utsJLksx5A2axkxnKN\nprotocol: ported\nserve:\n    service:\n        name: relay\n        port: http\n",
		"/function/caller.yaml": "id: QmWHZqnzh9nDihfNGAgWf4tnrcNdXA4opMVGJiqeZeVnFh\npackage:\n    name: relay_code\n" +
			"execution:\n    timeout: 5s\n    memory: 16MB\nchannel:\n    pipe:\n        ops: [dial]\n    ported:\n        ops: [dial]\n",
	})

	spec, err := h.Worker.Resolve(t.Context(), specs.Address{Project: "shop", Environment: "prod", Kind: "function", Name: "caller"})
	require.NoError(t, err)
	grants := channelGrants(t, spec)

	pipe := grants["pipe"]
	require.Empty(t, pipe.Refused)
	require.Equal(t, specs.PackageTypeWarp, pipe.Settings[invoke.SettingServeRuntime])
	require.False(t, pipe.Connects(), "a service on warp listens on no socket and is dispatched")

	require.Contains(t, grants["ported"].Refused, "listens on no port",
		"a port named on a tier that has none is refused, not dialled")
}

func TestAnAuthorizerIsHonouredOnEveryKindThatCanDeclareOne(t *testing.T) {
	h := newHarness(t)

	// A cache, which was the one kind the check could ever reach.
	cache := cacheGrants(t, resolveIn(t, h, nil, "function", "checkout"))
	require.Equal(t, "tier_check", cache["orders"].Settings["authorizer/name"])

	// And a database, which it could not.
	var profiles invoke.Grant
	for _, g := range resolveIn(t, h, []string{"billing"}, "function", "invoice").Reaches {
		if g.Kind == databaseKind && g.Label == "profiles" {
			profiles = g
		}
	}
	require.Equal(t, "profiles", profiles.Label, "the fixture's guarded database")
	require.Equal(t, "tier_check", profiles.Settings["authorizer/name"],
		"a control enforced on one kind and dropped on ten is worse than one that is missing")
	require.Equal(t, "/shop", profiles.Settings["authorizer/namespace"])
}
