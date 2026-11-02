package worker

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/container"
	"github.com/taubyte/tau/pkg/specs"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// Every test here reads the vocabulary's own fixture, compiled: what an access block
// means is the compiler's, and what a grant carries is what a tier enforces.

func cacheGrants(t *testing.T, spec invoke.Spec) map[string]invoke.Grant {
	t.Helper()

	out := map[string]invoke.Grant{}
	for _, g := range spec.Reaches {
		if g.Kind == cacheKind {
			out[g.Label] = g
		}
	}
	return out
}

// reachFor is one entry, for the rules checked directly against a target
// document rather than through the fixture.
func reachFor(label string) specs.Reach { return specs.Reach{Kind: cacheKind, Label: label} }

func resolveIn(t *testing.T, h *harness, scope []string, kind, name string) invoke.Spec {
	t.Helper()

	spec, err := h.Worker.Resolve(t.Context(), Target{
		Project: "shop", Environment: "prod", Scope: scope, Kind: kind, Name: name,
	})
	require.NoError(t, err)
	return spec
}

func TestACacheAccessEntryBecomesAGrant(t *testing.T) {
	h := newHarness(t)
	spec := resolveIn(t, h, []string{"billing"}, "function", "invoice")

	grants := cacheGrants(t, spec)
	require.Contains(t, grants, "sessions")
	require.Equal(t, []string{"get"}, grants["sessions"].Ops, "read only, as declared")
	require.Empty(t, grants["sessions"].Refused, "billing's own sessions is crdt and servable")
}

func TestOneLabelInTwoNamespacesIsTwoHandles(t *testing.T) {
	h := newHarness(t)

	// invoice sits in /billing and writes `sessions` with no namespace, so it
	// gets billing's own. checkout sits at the root and writes the same label
	// with `namespace: /shop`, so it gets shop's.
	billing := cacheGrants(t, resolveIn(t, h, []string{"billing"}, "function", "invoice"))
	root := cacheGrants(t, resolveIn(t, h, nil, "function", "checkout"))

	require.Contains(t, billing, "sessions")
	require.Contains(t, root, "sessions")
	require.NotEqual(t, billing["sessions"].Handle, root["sessions"].Handle,
		"one label, two namespaces, two caches")
}

func TestAHandleIsScopedToTheDeployment(t *testing.T) {
	h := newHarness(t)
	spec := resolveIn(t, h, []string{"billing"}, "function", "invoice")

	handle := cacheGrants(t, spec)["sessions"].Handle
	require.Contains(t, handle, "shop/prod/", "the project and the deployment are in front of the id")
	require.NotContains(t, handle, "sessions", "and the label is not")
}

func TestAGuardedCacheCarriesItsGuard(t *testing.T) {
	h := newHarness(t)

	// checkout reaches `orders`, which is guarded by /shop's tier_check.
	grants := cacheGrants(t, resolveIn(t, h, nil, "function", "checkout"))
	require.Contains(t, grants, "orders")

	guarded := grants["orders"]
	require.Empty(t, guarded.Refused, "a guard is served now, not refused")
	require.Equal(t, []string{"get", "put"}, guarded.Ops, "what was asked for is still the ceiling")
	require.Equal(t, "tier_check", guarded.Settings["authorizer/name"])
	require.Equal(t, "/shop", guarded.Settings["authorizer/namespace"],
		"resolved where the project is open, not left for the tier to walk")
}

func TestTheCanonicalFixtureServesOneCacheAndRefusesTheOther(t *testing.T) {
	h := newHarness(t)
	grants := cacheGrants(t, resolveIn(t, h, nil, "function", "checkout"))

	require.Len(t, grants, 2)
	require.Empty(t, grants["orders"].Refused, "guarded by an authorizer, and that is served")
	require.NotEmpty(t, grants["sessions"].Refused, "raft, which a CRDT store cannot keep")
}

func TestASingleWriterCacheIsRefusedByName(t *testing.T) {
	reason := unservable(reachFor("ledger"), specs.Resource{"consensus": specs.CacheConsensusRaft})

	require.Contains(t, reason, "raft", "it names what was declared")
	require.Contains(t, reason, "last-writer-wins", "and what it would have got instead")
	require.Contains(t, reason, `cache "ledger"`, "and which target")
}

func TestAGossipCacheIsServed(t *testing.T) {
	require.Empty(t, unservable(reachFor("x"), specs.Resource{"consensus": "gossip"}))
	require.Empty(t, unservable(reachFor("x"), specs.Resource{"consensus": "crdt"}))
}

func TestAnUnservableTargetIsStillRefusedWhenItIsGuarded(t *testing.T) {
	both := specs.Resource{
		"consensus": specs.CacheConsensusRaft,
		"authorizer": map[string]any{
			"name": "tier_check",
		},
	}
	require.Contains(t, unservable(reachFor("x"), both), "raft")
}

func TestAResourceWithNoCacheAccessHasNoCacheGrants(t *testing.T) {
	h := newHarness(t)

	// charge_card reaches a secret and nothing else.
	spec := resolveIn(t, h, nil, "function", "charge_card")
	require.Empty(t, cacheGrants(t, spec))

	// Every kind is carried, not only those a tier serves, so a tier sees what it
	// cannot do.
	require.Len(t, spec.Reaches, 1)
	require.Equal(t, "secret", spec.Reaches[0].Kind)
	require.Equal(t, "stripe", spec.Reaches[0].Label)
}

func TestAVolumeGrantCarriesItsMountPathAndQuota(t *testing.T) {
	h := newHarness(t)
	spec := resolveIn(t, h, nil, "application", "db")

	var volume invoke.Grant
	for _, g := range spec.Reaches {
		if g.Kind == volumeKind {
			volume = g
		}
	}
	require.Equal(t, "pgdata", volume.Label)
	require.Equal(t, []string{"mount-rw"}, volume.Ops)
	require.Equal(t, "/var/lib/postgresql/data", volume.Settings[settingMountPath])
	require.NotEmpty(t, volume.Settings[settingQuota], "the target's hard quota")
}

func TestTheFixturesDatabaseIsRefusedRatherThanStartedWithoutItsDisk(t *testing.T) {
	h := newHarness(t)
	spec := resolveIn(t, h, nil, "application", "db")

	tier, err := container.New(func(invoke.Spec) (container.Image, error) {
		return container.Image{Ref: "postgres:16"}, nil
	}, container.WithBackend(refusingBackend{}))
	require.NoError(t, err)

	_, err = tier.Start(t.Context(), spec)
	require.ErrorIs(t, err, container.ErrNoMounts)
	require.ErrorContains(t, err, "pgdata at /var/lib/postgresql/data")
}

func TestTheFixturesDatabaseStartsWhenSomethingCanGiveItTheDisk(t *testing.T) {
	h := newHarness(t)
	spec := resolveIn(t, h, nil, "application", "db")

	tier, err := container.New(func(invoke.Spec) (container.Image, error) {
		return container.Image{Ref: "postgres:16"}, nil
	}, container.WithBackend(refusingBackend{}), container.WithVolumes(handingBack{}))
	require.NoError(t, err)

	// It gets past the mount check and dies at the backend, which is what this
	// harness has instead of a container daemon.
	_, err = tier.Start(t.Context(), spec)
	require.NotErrorIs(t, err, container.ErrNoMounts)
	require.ErrorContains(t, err, "nothing should reach the backend")
}

// handingBack is a volume backing with a directory for everything.
type handingBack struct{}

func (handingBack) Mount(_ context.Context, grant invoke.Grant) (string, error) {
	return "/srv/tau/volumes/" + grant.Label, nil
}

// refusingBackend fails every call, so a test that got past the mount check
// would fail on the backend rather than passing by accident.
type refusingBackend struct{}

func (refusingBackend) Create(context.Context, *core.ContainerConfig) (core.ContainerID, error) {
	return "", errors.New("nothing should reach the backend")
}
func (refusingBackend) Start(context.Context, core.ContainerID) error  { return errNoBackend }
func (refusingBackend) Stop(context.Context, core.ContainerID) error   { return errNoBackend }
func (refusingBackend) Remove(context.Context, core.ContainerID) error { return errNoBackend }
func (refusingBackend) Inspect(context.Context, core.ContainerID) (*core.ContainerInfo, error) {
	return nil, errNoBackend
}
func (refusingBackend) Image(string) core.Image                      { return nil }
func (refusingBackend) Wait(context.Context, core.ContainerID) error { return errNoBackend }
func (refusingBackend) Logs(context.Context, core.ContainerID) (io.ReadCloser, error) {
	return nil, errNoBackend
}

var errNoBackend = errors.New("nothing should reach the backend")

func TestAStoreThatHoldsManyInstancesCarriesItsAdmission(t *testing.T) {
	h := newHarness(t)
	spec := resolveIn(t, h, nil, "function", "checkout")

	type testCase struct{ kind, label string }
	for _, want := range []testCase{
		{databaseKind, "tokens"},
		{storageKind, "receipts"},
	} {
		g, ok := grantFor(spec, want.kind, want.label)
		require.True(t, ok, "checkout reaches %s/%s", want.kind, want.label)
		require.Empty(t, g.Refused, "%s/%s is servable now", want.kind, want.label)

		require.Equal(t, "validator", g.Settings[settingAdmission])
		require.Equal(t, "may_open", g.Settings[settingValidator], "which function decides")
		require.Equal(t, "/shop", g.Settings[settingValidated], "and where it lives")
		require.NotEmpty(t, g.Settings[settingLimit], "the ceiling the author wrote")
	}

	// The kind's own settings survive the merge, or the database loses its data model.
	g, _ := grantFor(spec, databaseKind, "tokens")
	require.Equal(t, "kv", g.Settings[settingType])
}

func TestAStoreThatHoldsOneCarriesNoAdmission(t *testing.T) {
	h := newHarness(t)
	spec := resolveIn(t, h, nil, "function", "checkout")

	g, ok := grantFor(spec, streamKind, "orders")
	require.True(t, ok)
	require.Empty(t, g.Settings[settingAdmission])
}

func TestAKindsOwnReasonIsStillReported(t *testing.T) {
	h := newHarness(t)

	g, ok := grantFor(resolveIn(t, h, nil, "function", "checkout"), cacheKind, "sessions")
	require.True(t, ok)
	require.Contains(t, g.Refused, "raft")

	// And it still carries the block, so a cloud that served raft
	// would need no second change to serve its instances.
	require.Equal(t, "validator", g.Settings[settingAdmission])
}

// grantFor is one grant by kind and label.
func grantFor(spec invoke.Spec, kind, label string) (invoke.Grant, bool) {
	for _, g := range spec.Reaches {
		if g.Kind == kind && g.Label == label {
			return g, true
		}
	}
	return invoke.Grant{}, false
}

func streamGrants(t *testing.T, spec invoke.Spec) map[string]invoke.Grant {
	t.Helper()

	out := map[string]invoke.Grant{}
	for _, g := range spec.Reaches {
		if g.Kind == streamKind {
			out[g.Label] = g
		}
	}
	return out
}

func TestAStreamAccessEntryBecomesAGrant(t *testing.T) {
	h := newHarness(t)
	grants := streamGrants(t, resolveIn(t, h, nil, "function", "checkout"))

	require.Contains(t, grants, "orders")
	require.Equal(t, []string{"publish"}, grants["orders"].Ops)
	require.Empty(t, grants["orders"].Refused, "the distributed engine is the one this cloud holds")
	require.Contains(t, grants["orders"].Handle, "shop/prod/")
}

func TestAStreamOnAnEngineThisCloudLacksIsRefused(t *testing.T) {
	for _, engine := range []string{"kafka", "nats"} {
		reason := unservable(
			specs.Reach{Kind: streamKind, Label: "orders"},
			specs.Resource{"engine": engine},
		)
		require.Contains(t, reason, engine, "it names which engine")
		require.Contains(t, reason, "no driver")
		require.Contains(t, reason, "gossipsub", "and what it does have")
	}
}

func TestAStreamOnTheSwarmsOwnEngineIsServed(t *testing.T) {
	require.Empty(t, unservable(
		specs.Reach{Kind: streamKind, Label: "orders"},
		specs.Resource{"engine": schema.TauEngine()}))

	// An engine nobody wrote is the swarm's own — the vocabulary's default.
	require.Empty(t, unservable(
		specs.Reach{Kind: streamKind, Label: "orders"}, specs.Resource{}))
}

func TestAStorageAccessEntryCarriesItsTypeAndSize(t *testing.T) {
	h := newHarness(t)
	spec := resolveIn(t, h, nil, "function", "checkout")

	g, ok := grantFor(spec, storageKind, "receipts")
	require.True(t, ok, "checkout reaches receipts")
	require.Equal(t, "object", g.Settings[settingStorageType])
	require.Equal(t, "1000000000", g.Settings[settingStorageSize], "the 1GB the author wrote")
	require.Equal(t, "true", g.Settings[settingVersioning])
}

func TestAStreamingStorageIsRefused(t *testing.T) {
	require.Equal(t, "", unservable(
		specs.Reach{Kind: storageKind, Label: "receipts"},
		specs.Resource{"type": "object"}))

	why := unservable(
		specs.Reach{Kind: storageKind, Label: "events"},
		specs.Resource{"type": "streaming"})
	require.Contains(t, why, "streaming")
	require.Contains(t, why, "object storage")
}

func TestALogIsRefusedUnlessItIsRaft(t *testing.T) {
	reach := specs.Reach{Kind: specs.KindSync, Label: "events"}
	log := func(consensus string) specs.Resource {
		return specs.Resource{"type": specs.SyncTypeLog, "log": map[string]any{"consensus": consensus}}
	}

	require.Empty(t, unservable(reach, log(specs.SyncLogConsensusRaft)))
	require.Contains(t, unservable(reach, log(specs.SyncLogConsensusCrdt)), `asks for "crdt" consensus`)
	require.Empty(t, unservable(reach, specs.Resource{"type": specs.SyncTypeQueue, "queue": map[string]any{"consensus": specs.SyncQueueConsensusCrdt}}),
		"a queue is not a log")
}
