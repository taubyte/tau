package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/container"
	"github.com/taubyte/tau/pkg/project"
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
func reachFor(label string) project.Reach { return project.Reach{Kind: cacheKind, Label: label} }

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
func TestACacheWithAnAuthorizerIsRefusedByName(t *testing.T) {
	h := newHarness(t)

	// checkout reaches `orders`, which is guarded. IT RESOLVES — that is the
	// point: the function runs, and one of the things it touches does not.
	spec := resolveIn(t, h, nil, "function", "checkout")

	grants := cacheGrants(t, spec)
	require.Contains(t, grants, "orders")

	refused := grants["orders"]
	require.NotEmpty(t, refused.Refused)
	require.Contains(t, refused.Refused, "authorizer")
	require.Contains(t, refused.Refused, "tier_check", "it names which one")
	require.Equal(t, []string{"get", "put"}, refused.Ops, "what was asked for is still carried")
	require.False(t, refused.Allows("get"), "a refused grant allows nothing, whatever its ops say")
}

// TestTheFixtureItselfHitsBothRefusals, which is worth stating: the
// vocabulary's richest function reaches two caches and this cloud can serve
// neither, one for an authorizer and one for its consensus. Both are one line
// for an author to change, and neither stops the function running.
func TestTheCanonicalFixtureHitsBothRefusals(t *testing.T) {
	h := newHarness(t)
	grants := cacheGrants(t, resolveIn(t, h, nil, "function", "checkout"))

	require.Len(t, grants, 2)
	require.Contains(t, grants["orders"].Refused, "authorizer")
	require.Contains(t, grants["sessions"].Refused, "single-writer")
}

// TestSingleWriterIsRefusedRatherThanSilentlyDowngraded. An author who wrote
// single-writer asked for "nothing is silently discarded"; a CRDT merges
// concurrent writes last-writer-wins and the loser is gone with no error.
func TestASingleWriterCacheIsRefusedByName(t *testing.T) {
	reason := unservable(reachFor("ledger"), project.Resource{"consensus": singleWriter})

	require.Contains(t, reason, "single-writer", "it names what was declared")
	require.Contains(t, reason, "last-writer-wins", "and what it would have got instead")
	require.Contains(t, reason, `cache "ledger"`, "and which target")
}

// TestGossipIsServedRatherThanRefused, because a CRDT is STRONGER than gossip:
// gossip is eventually consistent and lossy, a CRDT is eventually consistent
// and not. An author who accepted a dropped entry gets one that arrives.
func TestAGossipCacheIsServed(t *testing.T) {
	require.Empty(t, unservable(reachFor("x"), project.Resource{"consensus": "gossip"}))
	require.Empty(t, unservable(reachFor("x"), project.Resource{"consensus": "crdt"}))
}

// TestTheAuthorizerCheckComesFirst, so a cache that is both guarded and
// single-writer says the thing an author has to fix first.
func TestAnAuthorizerIsNamedBeforeConsensus(t *testing.T) {
	both := project.Resource{
		"consensus": singleWriter,
		"authorizer": map[string]any{
			"name": "tier_check",
		},
	}
	require.Contains(t, unservable(reachFor("x"), both), "authorizer")
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
func (refusingBackend) Image(string) core.Image { return nil }

var errNoBackend = errors.New("nothing should reach the backend")

// ── instances ────────────────────────────────────────────────────────────────

// TestAStoreThatHoldsManyInstancesIsRefusedByName.
//
// `instances:` says a store holds many, that a named function decides which of
// them may exist, and how many there may be at once. Nothing here calls one and
// nothing counts them — so serving the grant would hand a workload ONE store,
// with the admission control silently absent and the ceiling unenforced. That
// is the authorizer's defect a level up, and it gets the authorizer's answer.
//
// FOUR KINDS CAN DECLARE IT — cache, database, storage and bus — and checkout
// reaches two of them, which is why the check is on the grant rather than on
// any one kind's settings.
func TestAStoreThatHoldsManyInstancesIsRefused(t *testing.T) {
	h := newHarness(t)
	spec := resolveIn(t, h, nil, "function", "checkout")

	for _, want := range []struct{ kind, label string }{
		{databaseKind, "tokens"},
		{"storage", "receipts"},
	} {
		g, ok := grantFor(spec, want.kind, want.label)
		require.True(t, ok, "checkout reaches %s/%s", want.kind, want.label)
		require.NotEmpty(t, g.Refused, "%s/%s admits instances and nothing admits them", want.kind, want.label)
		require.Contains(t, g.Refused, "many instances")
		require.Contains(t, g.Refused, "may_open", "it names which validator")
	}

	// AND THE WORKLOAD STILL RUNS. Access degrades per target: the one grant
	// that needed the unservable thing is refused and the rest are not.
	g, ok := grantFor(spec, streamKind, "orders")
	require.True(t, ok)
	require.Empty(t, g.Refused, "a target with no instances block is untouched")
}

// TestAKindsOwnReasonIsReportedOverInstances. shop's `sessions` is BOTH
// single-writer and a store of many instances, and an author is told the thing
// they can act on rather than whichever check ran first.
func TestAKindsOwnReasonIsReportedOverInstances(t *testing.T) {
	h := newHarness(t)

	// checkout's `sessions` entry names `/shop`, so it resolves to the one
	// that declares both.
	g, ok := grantFor(resolveIn(t, h, nil, "function", "checkout"), cacheKind, "sessions")
	require.True(t, ok)
	require.Contains(t, g.Refused, "single-writer")
	require.NotContains(t, g.Refused, "many instances")
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
			project.Reach{Kind: streamKind, Label: "orders"},
			project.Resource{"engine": engine},
		)
		require.Contains(t, reason, engine, "it names which engine")
		require.Contains(t, reason, "no driver")
		require.Contains(t, reason, "gossipsub", "and what it does have")
	}
}

func TestAStreamOnTheSwarmsOwnEngineIsServed(t *testing.T) {
	require.Empty(t, unservable(
		project.Reach{Kind: streamKind, Label: "orders"},
		project.Resource{"engine": distributedEngine}))

	// An engine nobody wrote is the swarm's own — the vocabulary's default.
	require.Empty(t, unservable(
		project.Reach{Kind: streamKind, Label: "orders"}, project.Resource{}))
}
