package worker

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
)

// channelGrants are the channel entries of a resolved spec, by label.
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

// TestAChannelGrantCarriesTheResolvedServer — a dial has to reach a workload,
// and nothing on the request path can turn a name into one: the tier holds a
// handle and not the compiled project. So the composer resolves it here.
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

// TestAnOmittedNamespaceResolvesToTheCHANNELsScope, not the dialer's. The
// fixture's `reserve` names no namespace and sits at the project root, so the
// chain that travels is the root — and it is a RESOLVED chain rather than the
// sentence that produced it, because the outward walk belongs where the project
// is open.
func TestAnOmittedNamespaceResolvesToTheChannelsScope(t *testing.T) {
	h := newHarness(t)
	grants := channelGrants(t, resolveIn(t, h, nil, "function", "checkout"))

	require.Equal(t, "/", grants["reserve"].Settings["serve/namespace"],
		"the project root, stated rather than left empty for a reader to guess")
}

// TestANamedContainerPortIsRefusedByName — a channel served by a container may
// say which of its ports the bytes go to, and an instance here is given exactly
// one. Delivering to the wrong socket and timing out is the failure this
// refusal exists to replace.
func TestANamedContainerPortIsRefusedByName(t *testing.T) {
	h := newHarness(t)
	grants := channelGrants(t, resolveIn(t, h, nil, "function", "checkout"))

	require.Contains(t, grants, "sync")
	require.Contains(t, grants["sync"].Refused, "port",
		"the sentence names what cannot be done")
	require.False(t, grants["sync"].Allows("dial"))
}

// TestOneRefusedChannelDoesNotTakeTheOthersDown — the whole reason
// Grant.Refused exists. `checkout` dials both, one is servable, and the
// workload still runs with the one that is.
func TestOneRefusedChannelDoesNotTakeTheOthersDown(t *testing.T) {
	h := newHarness(t)
	grants := channelGrants(t, resolveIn(t, h, nil, "function", "checkout"))

	require.Len(t, grants, 2)
	require.True(t, grants["reserve"].Allows("dial"))
	require.False(t, grants["sync"].Allows("dial"))
}

// TestAnAuthorizerIsHONOUREDOnEveryKindThatCanDeclareOne — attachAccess gives
// an `authorizer:` block to every kind with Ops(), and the check for one used to
// sit after unservableKind's switch, which every kind but `cache` returns from
// first. So a guarded cache was refused and a guarded DATABASE was served wide
// open, with nothing said either way.
//
// It is resolved for all of them now, which is the same claim pointed the other
// way: if the cross-kind wiring regresses, the database loses its guard silently
// and this fails.
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
