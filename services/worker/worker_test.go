package worker

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"

	workerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/worker/v2"
)

func target(kind, name string, scope ...string) *workerv2.Target {
	return &workerv2.Target{
		Project: "shop", Environment: "prod", Scope: scope, Kind: kind, Name: name,
	}
}

func invokeOver(t *testing.T, h *harness, tgt *workerv2.Target) *workerv2.InvokeResponse {
	t.Helper()
	res, err := h.Client.Invoke(t.Context(), connect.NewRequest(&workerv2.InvokeRequest{
		Target: tgt, Method: "GET", Path: "/orders",
	}))
	require.NoError(t, err)
	return res.Msg
}

// TestTheWholeChain: a name arrives over the wire, the register says what the
// environment runs, the compiled project says what the resource declares, and
// the tier the vocabulary selected is asked to start it with exactly those
// numbers.
func TestTheWholeChain(t *testing.T) {
	h := newHarness(t)

	res := invokeOver(t, h, target("application", "api"))
	require.Equal(t, int32(200), res.GetStatus())
	require.Equal(t, []string{"/orders"}, res.GetHeaders()["X-Path"].GetValues())

	started := h.Runtime.specs()
	require.Len(t, started, 1)
	spec := started[0]

	require.Equal(t, "container", spec.Runtime, "read off the kind's Engines(), not a table here")
	require.Equal(t, "ghcr.io/acme/api:1.4", spec.Artifact)
	require.Equal(t, int64(512_000_000), spec.Memory, "512MB, in bytes, from execution/memory")
	require.Equal(t, 80, spec.Concurrency)
	require.Contains(t, spec.Name, "shop/prod/application/")

	// And the instance answered with the spec it was started from, which is
	// how this test knows the request reached the code and not just the pool.
	var served invoke.Spec
	require.NoError(t, json.Unmarshal(res.GetBody(), &served))
	require.Equal(t, spec.Artifact, served.Artifact)
}

// TestTheTierComesFromTheVocabulary. A function is wasm and an application is a
// container because that is what each kind's Engines() says, and nothing in
// this package repeats it.
func TestTheTierComesFromTheVocabulary(t *testing.T) {
	h := newHarness(t)

	invokeOver(t, h, target("function", "checkout"))

	spec := h.Runtime.specs()[0]
	require.Equal(t, "wasm", spec.Runtime)
	// WHAT THE SEAM RESOLVED FOR THE COMPONENT. This line used to be the bare
	// `QmS4ust…` under the comment "a component's cid", and that comment was the
	// conflation the bug was made of: `source.component` is a `Ref("component")`,
	// so the compiled resource carries the COMPONENT RESOURCE's id — a
	// configuration document id, which the asset store has never held anything
	// under.
	//
	// The harness's resolver answers `registry/<project>/<env>/<kind>@sha256:<id>`,
	// so both halves of the fix are legible here: the seam was asked at all, and
	// it was asked as a `component` about the component rather than as this
	// `function` about itself. The arguments themselves are asserted in
	// artifact_test.go rather than read back out of a fake's string.
	require.Equal(t, "registry/shop/prod/component@sha256:QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVv",
		spec.Artifact)
	require.Equal(t, int64(32_000_000), spec.Memory)
	require.Equal(t, 20*time.Second, spec.Timeout, "a duration compiles to nanoseconds and comes back a duration")
}

// TestANameResolvesInTheScopeThatAsked, then outward.
func TestANameResolvesInTheScopeThatAsked(t *testing.T) {
	h := newHarness(t)

	invokeOver(t, h, target("function", "relay", "shop", "eu"))
	invokeOver(t, h, target("application", "api", "shop", "eu"))

	started := h.Runtime.specs()
	require.Len(t, started, 2)
	require.NotEqual(t, started[0].Name, started[1].Name)

	// The same api reached from the root and from a grandchild scope is one
	// instance, because it is one resource.
	invokeOver(t, h, target("application", "api"))
	require.Len(t, h.Runtime.specs(), 2, "no third cold start: the third call found the api's instance warm")
}

// TestABuiltResourceIsRefusedRatherThanGuessedAt. A resource whose source is a
// path in the repository names the source, not what came out of building it.
func TestABuiltResourceIsRefusedRatherThanGuessedAt(t *testing.T) {
	h := newBareHarness(t)

	_, err := h.Client.Invoke(t.Context(), connect.NewRequest(&workerv2.InvokeRequest{
		Target: target("application", "worker_pool"),
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	require.Contains(t, connect.CodeOf(err).String()+err.Error(), "worker_pool")

	// With a resolver, the same target runs.
	with := newHarness(t)
	invokeOver(t, with, target("application", "worker_pool"))
	require.Contains(t, with.Runtime.specs()[0].Artifact, "registry/shop/prod/application@sha256:")
}

// TestASecretIsNeverAnEmptyString. A workload started without its database
// password does not fail where the password is missing; it fails later, in a
// way that reads as a broken application.
func TestASecretIsNeverAnEmptyString(t *testing.T) {
	h := newBareHarness(t, WithArtifacts(artifactFunc(func(context.Context, string, string, string, string) (string, error) {
		return "an-image", nil
	})))

	_, err := h.Client.Invoke(t.Context(), connect.NewRequest(&workerv2.InvokeRequest{
		Target: target("application", "api"),
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))

	with := newHarness(t)
	invokeOver(t, with, target("application", "api"))

	env := with.Runtime.specs()[0].Env
	require.Equal(t, "info", env["LOG_LEVEL"])
	require.Equal(t, "eu-west", env["REGION"])
	require.Contains(t, env["STRIPE_KEY"], "sk_live_")
}

// TestAFailingSecretFailsTheInvocation rather than being dropped.
func TestAFailingSecretFailsTheInvocation(t *testing.T) {
	h := newHarness(t, WithSecrets(secretFunc(func(context.Context, string, string, string) (string, error) {
		return "", errRefused
	})))

	_, err := h.Client.Invoke(t.Context(), connect.NewRequest(&workerv2.InvokeRequest{
		Target: target("application", "api"),
	}))
	require.Error(t, err)
	require.Empty(t, h.Runtime.specs(), "nothing was started")
}

// TestTheCompiledProjectIsFetchedOnce. It is named by its content, so one that
// has been fetched is correct forever — and it is a whole project on the wire,
// so fetching it per request is the thing this cache exists to not do.
func TestTheCompiledProjectIsFetchedOnce(t *testing.T) {
	h := newHarness(t)

	for range 5 {
		invokeOver(t, h, target("application", "api"))
	}

	resolves, objects := h.Register.counts()
	require.Equal(t, 1, objects, "the object crossed the wire once")
	require.Equal(t, 1, resolves, "and the pointer was read once inside its ttl")
}

// TestThePointerIsRereadWhenItsBudgetIsSpent, and what a deploy changed is what
// runs. This is the whole meaning of PointerTTL.
func TestADeployTakesEffectWhenTheBudgetIsSpent(t *testing.T) {
	h := newHarnessWith(t, Config{PointerTTL: 5 * time.Second})

	invokeOver(t, h, target("application", "api"))
	require.Equal(t, int64(512_000_000), h.Runtime.specs()[0].Memory)

	// A second deploy: the same project with one number changed.
	next := withAPIMemory(t, compiled(t), 1_024_000_000)
	_, err := h.Register.Register.Publish(t.Context(), register.Publication{
		Project: "shop", Environment: "prod", Revision: 2, Compiled: next,
	})
	require.NoError(t, err)

	// Inside the budget nothing changes, which is the cost being paid for.
	invokeOver(t, h, target("application", "api"))
	require.Len(t, h.Runtime.specs(), 1, "still warm on the old revision")

	h.clock.advance(6 * time.Second)

	invokeOver(t, h, target("application", "api"))
	started := h.Runtime.specs()
	require.Len(t, started, 2, "a new revision never shares an instance with the old")
	require.Equal(t, int64(1_024_000_000), started[1].Memory)
	require.NotEqual(t, started[0].Revision, started[1].Revision)
}

// TestOnlyOneFetchPerObjectUnderLoad. A deploy makes every in-flight request
// want the same compiled project at the same instant.
func TestOnlyOneFetchPerObjectUnderLoad(t *testing.T) {
	h := newHarness(t)

	done := make(chan error, 16)
	for range 16 {
		go func() {
			_, err := h.Worker.Resolve(t.Context(), Target{
				Project: "shop", Environment: "prod", Kind: "function", Name: "checkout",
			})
			done <- err
		}()
	}
	for range 16 {
		require.NoError(t, <-done)
	}

	_, objects := h.Register.counts()
	require.Equal(t, 1, objects)
}

// TestTheProjectCacheIsBounded. Every deploy mints a new content address and
// the old one stays valid forever, so "still correct" is not a bound.
func TestTheProjectCacheIsBounded(t *testing.T) {
	h := newHarnessWith(t, Config{MaxProjects: 2, PointerTTL: time.Nanosecond})

	for revision := range uint64(5) {
		next := withAPIMemory(t, compiled(t), 100_000_000+int64(revision))
		_, err := h.Register.Register.Publish(t.Context(), register.Publication{
			Project: "shop", Environment: "prod", Revision: revision + 2, Compiled: next,
		})
		require.NoError(t, err)

		h.clock.advance(time.Second)
		invokeOver(t, h, target("application", "api"))
	}

	require.LessOrEqual(t, h.Worker.Loaded(), 2)
}

// TestAFailedLoadIsNotRemembered. The object is immutable so a success is good
// forever, but a failure is usually the register being unreachable — and
// caching that would turn one bad moment into a project that never loads again.
func TestAFailedLoadIsNotRemembered(t *testing.T) {
	h := newHarness(t)
	h.Register.breakObjects(true)

	_, err := h.Worker.Resolve(t.Context(), Target{
		Project: "shop", Environment: "prod", Kind: "application", Name: "api",
	})
	require.Error(t, err)
	require.Zero(t, h.Worker.Loaded())

	h.Register.breakObjects(false)

	_, err = h.Worker.Resolve(t.Context(), Target{
		Project: "shop", Environment: "prod", Kind: "application", Name: "api",
	})
	require.NoError(t, err)
}

func TestWhatIsMissingSaysWhichKindOfMissing(t *testing.T) {
	h := newHarness(t)

	for _, c := range []struct {
		name string
		req  *workerv2.InvokeRequest
		want connect.Code
	}{
		{"no target at all", &workerv2.InvokeRequest{}, connect.CodeInvalidArgument},
		{"an empty target", &workerv2.InvokeRequest{Target: &workerv2.Target{}}, connect.CodeInvalidArgument},
		{"no such resource", &workerv2.InvokeRequest{Target: target("application", "nope")}, connect.CodeNotFound},
		{"no such environment", &workerv2.InvokeRequest{Target: &workerv2.Target{
			Project: "shop", Environment: "staging", Kind: "application", Name: "api",
		}}, connect.CodeNotFound},
		{"a kind nothing runs", &workerv2.InvokeRequest{Target: target("cache", "orders")}, connect.CodeFailedPrecondition},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := h.Client.Invoke(t.Context(), connect.NewRequest(c.req))
			require.Error(t, err)
			require.Equal(t, c.want, connect.CodeOf(err))
		})
	}
}

// TestTheCodeTableCoversWhatItClaims. The distinction the table exists for is
// "ask somebody else" versus "stop", and a caller that could not tell them
// apart would either retry forever on a typo or give up on a blip.
func TestTheCodeTable(t *testing.T) {
	for _, c := range []struct {
		err  error
		want connect.Code
	}{
		{ErrNoTarget, connect.CodeInvalidArgument},
		{register.ErrNotPublished, connect.CodeNotFound},
		{register.ErrNoObject, connect.CodeNotFound},
		{specs.ErrNotFound, connect.CodeNotFound},
		{ErrNoRuntime, connect.CodeFailedPrecondition},
		{ErrAmbiguous, connect.CodeFailedPrecondition},
		{ErrNoArtifact, connect.CodeFailedPrecondition},
		{ErrNoSecrets, connect.CodeFailedPrecondition},
		{invoke.ErrNoRuntime, connect.CodeFailedPrecondition},
		{invoke.ErrAtCapacity, connect.CodeResourceExhausted},
		{context.DeadlineExceeded, connect.CodeDeadlineExceeded},
		{register.ErrMalformedPointer, connect.CodeDataLoss},
		{specs.ErrBadIndex, connect.CodeDataLoss},
		{specs.ErrNoObject, connect.CodeDataLoss},
		{specs.ErrNoID, connect.CodeDataLoss},
		{context.Canceled, connect.CodeUnavailable},
	} {
		require.Equal(t, c.want, connect.CodeOf(wire(c.err)), "%v", c.err)
	}
}

// withAPIMemory is one deploy's worth of change: the same project with a
// different number on one resource, which is what makes it a different object.
func withAPIMemory(t *testing.T, base []byte, memory int64) []byte {
	t.Helper()

	var doc map[string]any
	require.NoError(t, json.Unmarshal(base, &doc))

	object := doc["object"].(map[string]any)
	for _, r := range object["application"].(map[string]any) {
		res := r.(map[string]any)
		if res["name"] == "api" {
			res["execution"].(map[string]any)["memory"] = memory
		}
	}

	out, err := json.Marshal(doc)
	require.NoError(t, err)
	return out
}

var errRefused = errors.New("the secrets service refused")

type artifactFunc func(ctx context.Context, project, environment, kind, resourceID string) (string, error)

func (f artifactFunc) Artifact(ctx context.Context, project, environment, kind, resourceID string) (string, error) {
	return f(ctx, project, environment, kind, resourceID)
}

type secretFunc func(ctx context.Context, project, environment, secretID string) (string, error)

func (f secretFunc) Secret(ctx context.Context, project, environment, secretID string) (string, error) {
	return f(ctx, project, environment, secretID)
}

// TestTheClientIsTheSameWorker — a gateway holds one interface and one error
// path whether the worker is in this process or across a stream, and the
// distinction it depends on is "this deployment is missing a piece" versus
// "this node cannot do it right now".
func TestTheClientIsTheSameWorker(t *testing.T) {
	h := newHarness(t)
	c := newWorkerClient(t, h)

	res, err := c.Invoke(t.Context(), specs.Address{
		Project: "shop", Environment: "prod", Kind: "application", Name: "api",
	}, &invoke.Request{Method: "GET", Path: "/orders"})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status)
	require.Equal(t, []string{"/orders"}, res.Headers["X-Path"])

	// A nil request is one with nothing in it rather than a panic: a queue
	// delivery and a channel dial both carry no method and no path.
	_, err = c.Invoke(t.Context(), specs.Address{
		Project: "shop", Environment: "prod", Kind: "application", Name: "api",
	}, nil)
	require.NoError(t, err)
}

func TestEverySentinelSurvivesTheWireToTheClient(t *testing.T) {
	h := newBareHarness(t)
	c := newWorkerClient(t, h)

	at := func(kind, name string) specs.Address {
		return specs.Address{Project: "shop", Environment: "prod", Kind: kind, Name: name}
	}

	_, err := c.Invoke(t.Context(), specs.Address{}, nil)
	require.ErrorIs(t, err, ErrNoTarget)

	_, err = c.Invoke(t.Context(), at("application", "nope"), nil)
	require.ErrorIs(t, err, specs.ErrNotFound)

	_, err = c.Invoke(t.Context(), specs.Address{
		Project: "shop", Environment: "staging", Kind: "application", Name: "api",
	}, nil)
	require.ErrorIs(t, err, register.ErrNotPublished)

	_, err = c.Invoke(t.Context(), at("cache", "orders"), nil)
	require.ErrorIs(t, err, ErrNoRuntime)

	_, err = c.Invoke(t.Context(), at("application", "worker_pool"), nil)
	require.ErrorIs(t, err, ErrNoArtifact)

	// A status this package did not build passes through rather than being
	// forced into a sentinel it does not mean.
	plain := errors.New("no route to peer")
	require.Equal(t, plain, unwire(plain))
	require.Equal(t, connect.CodeUnavailable,
		connect.CodeOf(unwire(connect.NewError(connect.CodeUnavailable, errors.New("something else")))))
}

// A TIER GETS THE `serve` BLOCK, flattened, and nothing else off the resource.
// It is by definition how a thing is served — a static site's mode and index
// live there — and passing the whole resource would put every access rule and
// every variable into every Spec, which is a wider thing to hold for one that
// is not read.
func TestTheServeBlockReachesTheTier(t *testing.T) {
	h := newHarness(t)

	invokeOver(t, h, target("website", "shop"))
	spec := h.Static.specs()[0]

	require.Equal(t, "static", spec.Runtime, "read off the kind's Engines()")
	require.Equal(t, "spa", spec.Settings["serve/mode"])

	// A resource with none of the three carries no settings, rather than an
	// empty map every tier has to check for.
	h2 := newHarness(t)
	invokeOver(t, h2, target("application", "api"))
	require.Nil(t, h2.Runtime.specs()[0].Settings)
}

// THE ENTRYPOINT AND THE WORLD ARE THE VOCABULARY'S OWN WORDS, and a wasm tier
// cannot run anything without them: without the entrypoint it calls the world's
// default name instead of the author's, and without the world it calls a
// component built for another interface with the wrong signature.
func TestTheEntrypointAndTheWorldReachTheTier(t *testing.T) {
	h := newHarness(t)

	invokeOver(t, h, target("function", "checkout"))
	spec := h.Runtime.specs()[0]

	require.Equal(t, "checkout", spec.Settings["execution/call"])
	require.Equal(t, "handler", spec.Settings["implements"],
		"the schema defaults it, so a resource that says nothing still says handler")
}

// WHERE A WORKLOAD MAY REACH IS THE RESOURCE'S, and empty is nowhere. The tier
// enforces it; the worker only carries it, because which mechanism does the
// enforcing is the tier's business and the allowlist is the author's.
func TestTheEgressAllowlistReachesTheTier(t *testing.T) {
	h := newHarness(t)

	invokeOver(t, h, target("function", "charge_card"))
	require.Equal(t, []string{"api.stripe.com"}, h.Runtime.specs()[0].Egress)

	// A resource that declared none carries none, which the tier reads as
	// nowhere rather than as anywhere.
	h2 := newHarness(t)
	invokeOver(t, h2, target("function", "reserve_stock"))
	require.Empty(t, h2.Runtime.specs()[0].Egress)
}

// ── what this service accounted for ──────────────────────────────────────

// AN INVOCATION IS THE OTHER OBVIOUSLY BILLED THING A CLOUD DOES. Requests and
// GB-seconds are what every serverless platform charges in, and this service
// knew neither — it resolved a spec, ran it, and answered.
type meter struct {
	mu     sync.Mutex
	events []Accounted
}

func (m *meter) Record(_ context.Context, ev Accounted) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, ev)
}

func (m *meter) seen() []Accounted {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Accounted(nil), m.events...)
}

func TestAnInvocationIsAccounted(t *testing.T) {
	m := &meter{}
	h := newHarness(t, WithMeter(m))

	invokeOver(t, h, target("application", "api"))

	events := m.seen()
	require.Len(t, events, 1)
	ev := events[0]

	require.Equal(t, OpInvoke, ev.Op)
	require.Equal(t, "shop", ev.Project)
	require.Equal(t, "prod", ev.Environment)
	require.Equal(t, "application", ev.Kind)
	require.Equal(t, "api", ev.Name)

	// WHAT IT WAS ALLOWED, not what it used — which is what every platform
	// bills, because what it used is not observable from here.
	require.Equal(t, int64(512_000_000), ev.Memory)

	// A BILL SOMEBODY DISPUTES IS RECONCILED AGAINST A DEPLOY.
	require.NotEmpty(t, ev.Revision)
}

// A REFUSAL IS THE EVENT A CALLER MOST WANTS EXPLAINED, and it is the one an
// invocation counter cannot show: the figures look identical whether a request
// was served or turned away.
func TestARefusedInvocationIsAccounted(t *testing.T) {
	m := &meter{}
	h := newHarness(t, WithMeter(m))

	_, err := h.Worker.Invoke(t.Context(), Target{
		Project: "shop", Environment: "prod", Kind: "function", Name: "not-a-function",
	}, &invoke.Request{})
	require.Error(t, err)

	events := m.seen()
	require.Len(t, events, 1)
	require.Equal(t, OpRefuse, events[0].Op)
	require.Equal(t, "not-a-function", events[0].Name)
	require.Empty(t, events[0].Revision, "nothing resolved, so nothing to reconcile against")
}

// A node with no meter invokes exactly as it did, and the events are not
// constructed at all.
func TestNoMeterIsTheOrdinaryCase(t *testing.T) {
	h := newHarness(t)
	res := invokeOver(t, h, target("application", "api"))
	require.Equal(t, int32(200), res.GetStatus())
}

func TestWithMeterRefusesNil(t *testing.T) {
	require.Error(t, WithMeter(nil)(&options{}))
}
