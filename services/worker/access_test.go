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

// EVERY TEST HERE READS THE VOCABULARY'S OWN FIXTURE, compiled. What an access
// block means is the compiler's, and what a grant carries is what a tier
// enforces — so a hand-written project would be this package's idea of both.

// cacheGrants are the cache entries of a resolved spec, by label.
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

// TestTheAccessBlockBecomesGrants — the compiled `cache:` block reaches the
// tier as something it can enforce, with the verbs the author asked for.
func TestACacheAccessEntryBecomesAGrant(t *testing.T) {
	h := newHarness(t)
	spec := resolveIn(t, h, []string{"billing"}, "function", "invoice")

	grants := cacheGrants(t, spec)
	require.Contains(t, grants, "sessions")
	require.Equal(t, []string{"get"}, grants["sessions"].Ops, "read only, as declared")
	require.Empty(t, grants["sessions"].Refused, "billing's own sessions is crdt and servable")
}

// TestTheHandleCarriesTheRESOLVEDId, not the label. The fixture has three
// caches called `sessions` in three namespaces, so a handle built from the
// label would hand all three the same store.
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

// TestAHandleNamesTheDeployment, because a cache is per environment: staging's
// `sessions` and production's are the same declaration and different data.
func TestAHandleIsScopedToTheDeployment(t *testing.T) {
	h := newHarness(t)
	spec := resolveIn(t, h, []string{"billing"}, "function", "invoice")

	handle := cacheGrants(t, spec)["sessions"].Handle
	require.Contains(t, handle, "shop/prod/", "the project and the deployment are in front of the id")
	require.NotContains(t, handle, "sessions", "and the label is not")
}

// ── what this cloud will not serve ───────────────────────────────────────────

// TestAnAuthorizedCacheIsARefusedGrantAndNotARefusedWorkload.
//
// The fixture's `orders` cache names an authorizer, and nothing here calls one.
// Serving it would grant access the target's own configuration says somebody
// else decides; refusing the WORKLOAD would take a function down over one of
// the things it reaches. So the resource runs and that one target does not.
// TestAGuardedCacheCarriesItsGuardRatherThanARefusal. This used to assert the
// refusal, which was the honest answer for as long as nothing could run an
// authorizer. What it asserts now is the thing that replaced it: the guard is
// RESOLVED at compose time, because the tier holds a handle and not the project.
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

// TestOneRefusedCacheDoesNotTakeTheOtherDown, which is worth stating: the
// vocabulary's richest function reaches two caches, one of them unservable, and
// the function still runs with the other.
func TestTheCanonicalFixtureServesOneCacheAndRefusesTheOther(t *testing.T) {
	h := newHarness(t)
	grants := cacheGrants(t, resolveIn(t, h, nil, "function", "checkout"))

	require.Len(t, grants, 2)
	require.Empty(t, grants["orders"].Refused, "guarded by an authorizer, and that is served")
	require.NotEmpty(t, grants["sessions"].Refused, "raft, which a CRDT store cannot keep")
}

// TestSingleWriterIsRefusedRatherThanSilentlyDowngraded. An author who wrote
// raft asked for "nothing is silently discarded"; a CRDT merges
// concurrent writes last-writer-wins and the loser is gone with no error.
func TestASingleWriterCacheIsRefusedByName(t *testing.T) {
	reason := unservable(reachFor("ledger"), specs.Resource{"consensus": raftConsensus})

	require.Contains(t, reason, "raft", "it names what was declared")
	require.Contains(t, reason, "last-writer-wins", "and what it would have got instead")
	require.Contains(t, reason, `cache "ledger"`, "and which target")
}

// TestGossipIsServedRatherThanRefused, because a CRDT is STRONGER than gossip:
// gossip is eventually consistent and lossy, a CRDT is eventually consistent
// and not. An author who accepted a dropped entry gets one that arrives.
func TestAGossipCacheIsServed(t *testing.T) {
	require.Empty(t, unservable(reachFor("x"), specs.Resource{"consensus": "gossip"}))
	require.Empty(t, unservable(reachFor("x"), specs.Resource{"consensus": "crdt"}))
}

// TestAGuardDoesNotRESCUEAnUnservableTarget. A guarded cache that is also raft
// is still raft: the guard decides who may reach a thing, not whether this
// cloud can serve it, and saying "ask tier_check" about a store that would lose
// the write anyway answers the wrong question.
func TestAnUnservableTargetIsStillRefusedWhenItIsGuarded(t *testing.T) {
	both := specs.Resource{
		"consensus": raftConsensus,
		"authorizer": map[string]any{
			"name": "tier_check",
		},
	}
	require.Contains(t, unservable(reachFor("x"), both), "raft")
}

// TestAResourceThatReachesNothingCarriesNoGrants, which is most of them.
func TestAResourceWithNoCacheAccessHasNoCacheGrants(t *testing.T) {
	h := newHarness(t)

	// charge_card reaches a secret and nothing else.
	spec := resolveIn(t, h, nil, "function", "charge_card")
	require.Empty(t, cacheGrants(t, spec))

	// EVERY KIND IS CARRIED, not just the ones a tier serves — a tier has to
	// see what it cannot do for a workload, and it cannot see that from a list
	// filtered down to what already works.
	require.Len(t, spec.Reaches, 1)
	require.Equal(t, "secret", spec.Reaches[0].Kind)
	require.Equal(t, "stripe", spec.Reaches[0].Label)
}

// TestAMountPathComesOffTheENTRY, because two workloads mounting one volume can
// put it in different places.
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

// TestTheFixturesPostgresIsRefusedAllTheWayThrough.
//
// `application/db` mounts a volume at /var/lib/postgresql/data — the canonical
// case where an ephemeral filesystem is not a degraded service but a lost
// database. It is worth following end to end because every link had to carry
// something for the refusal to happen at all: the compiler wrote the entry, the
// access reader told it from a lowered child, the worker resolved it into a
// grant with its mount path, and the tier recognised the kind.
//
// BEFORE THIS, IT STARTED. Postgres came up, wrote into the container's own
// layer, and lost everything when the container went — with nothing anywhere
// saying so.
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

// TestAndWithADiskItSTARTS, which is the other half of the same story and the
// reason the refusal is conditional rather than permanent. A compose import
// whose database keeps its data in a named volume is the most common shape
// there is, and while nothing could give it one, an imported stack could not
// run at all.
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

// ── instances ────────────────────────────────────────────────────────────────

// TestASTOREThatHoldsManyCarriesTheBlockRatherThanARefusal.
//
// `instances:` was a refusal for as long as nothing honoured it: the target
// named who admits an instance and how many there may be, and serving the grant
// anyway handed a workload ONE store where the config says many. What honours it
// now is the tier — the instance travels in the label, the validator is invoked,
// the ceiling is counted — so what the composer owes is the block, intact.
//
// FOUR KINDS CAN DECLARE IT — cache, database, storage and bus — which is why
// it is merged onto every grant rather than living in one kind's settings.
func TestAStoreThatHoldsManyInstancesCarriesItsAdmission(t *testing.T) {
	h := newHarness(t)
	spec := resolveIn(t, h, nil, "function", "checkout")

	for _, want := range []struct{ kind, label string }{
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

	// AND THE KIND'S OWN SETTINGS SURVIVE THE MERGE. The two maps carry
	// different keys by construction, and a merge that dropped one would be a
	// database with no data model.
	g, _ := grantFor(spec, databaseKind, "tokens")
	require.Equal(t, "kv", g.Settings[settingType])
}

// TestASTOREThatHoldsONEDeclaresNothing, so a target with no `instances:` block
// is addressed as itself and the tier knows the difference.
func TestAStoreThatHoldsOneCarriesNoAdmission(t *testing.T) {
	h := newHarness(t)
	spec := resolveIn(t, h, nil, "function", "checkout")

	g, ok := grantFor(spec, streamKind, "orders")
	require.True(t, ok)
	require.Empty(t, g.Settings[settingAdmission])
}

// TestAKINDSOwnReasonStillWins. shop's `sessions` is raft AND a store
// of many instances; the refusal is about the first, because that is the one an
// author can act on and the second is no longer a refusal at all.
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

// ── streams ──────────────────────────────────────────────────────────────────

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

// TestAStreamAccessEntryBecomesAGrant. checkout publishes to `orders`, which
// the fixture puts on the swarm's own engine.
func TestAStreamAccessEntryBecomesAGrant(t *testing.T) {
	h := newHarness(t)
	grants := streamGrants(t, resolveIn(t, h, nil, "function", "checkout"))

	require.Contains(t, grants, "orders")
	require.Equal(t, []string{"publish"}, grants["orders"].Ops)
	require.Empty(t, grants["orders"].Refused, "the distributed engine is the one this cloud holds")
	require.Contains(t, grants["orders"].Handle, "shop/prod/")
}

// TestAnExternalStreamEngineIsRefusedByName. Kafka and NATS are each a wire
// protocol and a driver — a release, not a config value, which is what the
// schema says where it closes the set — so publishing into nothing would be
// the silent kind of failure.
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

// ── storage ──────────────────────────────────────────────────────────────────

// TestASTORAGEAccessEntryBecomesAGrant carrying the data model and the ceiling,
// which is the same shape a database's does — the type is the key to the block
// the rest lives under.
func TestAStorageAccessEntryCarriesItsTypeAndSize(t *testing.T) {
	h := newHarness(t)
	spec := resolveIn(t, h, nil, "function", "checkout")

	g, ok := grantFor(spec, storageKind, "receipts")
	require.True(t, ok, "checkout reaches receipts")
	require.Equal(t, "object", g.Settings[settingStorageType])
	require.Equal(t, "1000000000", g.Settings[settingStorageSize], "the 1GB the author wrote")
	require.Equal(t, "true", g.Settings[settingVersioning])
}

// TestASTREAMINGStoreIsRefusedByName. Its verbs move bytes a caller does not
// hold all of at once, so serving it through the object functions would mean a
// guest holding a whole stream in memory to write it.
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
