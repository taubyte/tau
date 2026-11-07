package worker

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
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
