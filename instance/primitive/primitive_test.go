package primitive_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance/primitive"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	primitives "github.com/taubyte/tau/services/primitives"
	"github.com/taubyte/tau/services/worker"
)

// The adapter alone: every call resolves the reach, a refusing reach refuses every
// call, and nothing is lost between the two schedule shapes. Which namespace an item
// lands in is node/'s test.

// answering stands in for whichever arm the reach handed back — this node's own
// primitives service, or a stream to one across the swarm. The backing cannot
// tell which it got, which is the whole point of the seam, so one double serves
// for both.
type answering struct {
	err error

	sends    []sent
	claims   []claim
	settles  []settle
	depths   []invoke.Grant
	appends  []sent
	reads    []read
	delivery primitives.Delivery
	depth    primitives.Depth
	cursor   string
	records  []wasmtier.Record
}

type sent struct {
	grant invoke.Grant
	id    string
	data  []byte
}

type claim struct {
	grant invoke.Grant
	max   int
}

type settle struct {
	grant    invoke.Grant
	delivery primitives.Delivery
	done     bool
}

type read struct {
	grant invoke.Grant
	after string
	limit uint32
}

func (a *answering) Send(_ context.Context, grant invoke.Grant, id string, data []byte) error {
	a.sends = append(a.sends, sent{grant, id, data})
	return a.err
}

func (a *answering) Claim(_ context.Context, grant invoke.Grant, max int) ([]primitives.Delivery, error) {
	a.claims = append(a.claims, claim{grant, max})
	return []primitives.Delivery{a.delivery}, a.err
}

func (a *answering) Settle(_ context.Context, grant invoke.Grant, delivery primitives.Delivery, done bool) error {
	a.settles = append(a.settles, settle{grant, delivery, done})
	return a.err
}

func (a *answering) Depth(_ context.Context, grant invoke.Grant) (primitives.Depth, error) {
	a.depths = append(a.depths, grant)
	return a.depth, a.err
}

func (a *answering) Append(_ context.Context, grant invoke.Grant, data []byte) (string, error) {
	a.appends = append(a.appends, sent{grant: grant, data: data})
	return a.cursor, a.err
}

func (a *answering) Read(_ context.Context, grant invoke.Grant, after string, limit uint32) ([]wasmtier.Record, error) {
	a.reads = append(a.reads, read{grant, after, limit})
	return a.records, a.err
}

// reaching is a Reach double: the arm it hands back, or a refusal, and a count
// of how often it was asked.
type reaching struct {
	seam  primitive.Seam
	err   error
	asked int
}

func (r *reaching) Now(context.Context) (primitive.Seam, error) {
	r.asked++
	if r.err != nil {
		return nil, r.err
	}
	return r.seam, nil
}

// jobs is what a grant carries: the deployment and the resource id, and nothing
// else. Everything about the queue itself is the service's read of the
// declaration.
func jobs() invoke.Grant {
	return invoke.Grant{Kind: "sync", Label: "jobs", Handle: "shop/prod/QmJobs"}
}

func backing(t *testing.T) (*primitive.Backing, *reaching, *answering) {
	t.Helper()

	service := &answering{}
	reach := &reaching{seam: service}
	b, err := primitive.New(reach)
	require.NoError(t, err)
	return b, reach, service
}

type namedCall struct {
	name string
	call func(context.Context, *primitive.Backing) error
}

// every call the seam carries, so the two rules below are asserted about all of
// them rather than about whichever one somebody wrote a test for.
func everyCall() []namedCall {
	return []namedCall{
		{"send", func(ctx context.Context, b *primitive.Backing) error {
			return b.Send(ctx, jobs(), "order-1", []byte("ship it"))
		}},
		{"claim", func(ctx context.Context, b *primitive.Backing) error {
			_, err := b.Claim(ctx, jobs(), 4)
			return err
		}},
		{"settle", func(ctx context.Context, b *primitive.Backing) error {
			return b.Settle(ctx, jobs(), primitives.Delivery{ID: "order-1"}, true)
		}},
		{"depth", func(ctx context.Context, b *primitive.Backing) error {
			_, err := b.Depth(ctx, jobs())
			return err
		}},
		{"append", func(ctx context.Context, b *primitive.Backing) error {
			_, err := b.Append(ctx, jobs(), []byte("first"))
			return err
		}},
		{"read", func(ctx context.Context, b *primitive.Backing) error {
			_, err := b.Read(ctx, jobs(), "", 10)
			return err
		}},
	}
}

func TestEveryCallResolvesTheReachAgain(t *testing.T) {
	for _, one := range everyCall() {
		t.Run(one.name, func(t *testing.T) {
			b, reach, _ := backing(t)

			require.NoError(t, one.call(t.Context(), b))
			require.NoError(t, one.call(t.Context(), b))
			require.Equal(t, 2, reach.asked, "the arm was resolved once and kept")
		})
	}
}

func TestAReachThatRefusesRefusesEveryCall(t *testing.T) {
	unreachable := errors.New("no primitives service is reachable")

	for _, one := range everyCall() {
		t.Run(one.name, func(t *testing.T) {
			service := &answering{}
			b, err := primitive.New(&reaching{seam: service, err: unreachable})
			require.NoError(t, err)

			require.ErrorIs(t, one.call(t.Context(), b), unreachable)
			require.Empty(t, service.sends)
			require.Empty(t, service.claims)
			require.Empty(t, service.settles)
			require.Empty(t, service.depths)
			require.Empty(t, service.appends)
			require.Empty(t, service.reads)
		})
	}
}

func TestTheServicesOwnRefusalIsWhatTheCallerGets(t *testing.T) {
	refused := errors.New("that queue is not declared")

	for _, one := range everyCall() {
		t.Run(one.name, func(t *testing.T) {
			service := &answering{err: refused}
			b, err := primitive.New(&reaching{seam: service})
			require.NoError(t, err)

			got := one.call(t.Context(), b)
			require.ErrorIs(t, got, refused)
			require.Equal(t, refused, got, "the refusal was wrapped and dispatch matches it by name")
		})
	}
}

func TestABackingWithNoReachIsRefused(t *testing.T) {
	_, err := primitive.New(nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "reach")
}

func TestASendCarriesWhatItWasHanded(t *testing.T) {
	b, _, service := backing(t)

	require.NoError(t, b.Send(t.Context(), jobs(), "order-1", []byte("ship it")))
	require.Len(t, service.sends, 1)
	require.Equal(t, jobs(), service.sends[0].grant)
	require.Equal(t, "order-1", service.sends[0].id)
	require.Equal(t, []byte("ship it"), service.sends[0].data)
}

func TestAClaimCarriesItsCeilingAndAnswersWithTheItems(t *testing.T) {
	b, _, service := backing(t)
	service.delivery = primitives.Delivery{ID: "order-1", Data: []byte("ship it"), Attempt: 2, Token: "t"}

	got, err := b.Claim(t.Context(), jobs(), 1)
	require.NoError(t, err)
	require.Equal(t, []primitives.Delivery{service.delivery}, got)
	require.Equal(t, []claim{{jobs(), 1}}, service.claims)
}

func TestASettleCarriesTheDeliveryAndTheVerdict(t *testing.T) {
	item := primitives.Delivery{ID: "order-1", Attempt: 3, Token: "round-trip-me", Deadline: time.Unix(1, 0)}

	for _, done := range []bool{true, false} {
		b, _, service := backing(t)

		require.NoError(t, b.Settle(t.Context(), jobs(), item, done))
		require.Len(t, service.settles, 1)
		require.Equal(t, item, service.settles[0].delivery)
		require.Equal(t, done, service.settles[0].done)
	}
}

func TestADepthAnswersWithWhatTheServiceHolds(t *testing.T) {
	b, _, service := backing(t)
	service.depth = primitives.Depth{Ready: 3, Claimed: 1, Dead: 2}

	got, err := b.Depth(t.Context(), jobs())
	require.NoError(t, err)
	require.Equal(t, primitives.Depth{Ready: 3, Claimed: 1, Dead: 2}, got)
	require.Equal(t, []invoke.Grant{jobs()}, service.depths)
}

func TestAnAppendAnswersWithItsCursor(t *testing.T) {
	b, _, service := backing(t)
	service.cursor = "00000000000000000001"

	got, err := b.Append(t.Context(), jobs(), []byte("first"))
	require.NoError(t, err)
	require.Equal(t, "00000000000000000001", got)
	require.Len(t, service.appends, 1)
	require.Equal(t, []byte("first"), service.appends[0].data)
	require.Equal(t, jobs(), service.appends[0].grant)
}

func TestAReadCarriesTheCursorAndTheLimit(t *testing.T) {
	b, _, service := backing(t)
	service.records = []wasmtier.Record{{Cursor: "2", At: 7, Data: []byte("second")}}

	got, err := b.Read(t.Context(), jobs(), "1", 10)
	require.NoError(t, err)
	require.Equal(t, service.records, got)
	require.Equal(t, []read{{jobs(), "1", 10}}, service.reads)
}

// holding is a Deployments double: the crons a worker resolved.
type holding struct {
	held  []worker.Schedule
	asked int
}

func (h *holding) Schedules(context.Context) []worker.Schedule {
	h.asked++
	return h.held
}

// aCron is one schedule with EVERY FIELD SET, so the translation below is
// checked against the shape rather than against the fields somebody remembered.
func aCron() worker.Schedule {
	return worker.Schedule{
		Handle:    "shop/prod/QmNightly",
		ID:        "QmNightly",
		Name:      "nightly",
		Scope:     []string{"billing"},
		Path:      "/billing/nightly",
		Spec:      "0 3 * * *",
		Timezone:  "Europe/Paris",
		OnOverlap: "skip",
		Jitter:    30 * time.Second,
		Fires: []worker.Target{{
			Project:     "shop",
			Environment: "prod",
			Scope:       []string{"billing"},
			Kind:        "function",
			Name:        "invoice",
		}},
		Starts:  []invoke.Grant{{Kind: "sync", Label: "steps", Handle: "shop/prod/QmSteps"}},
		Refused: "one edge named nothing",
	}
}

func TestEveryFieldOfAScheduleCrosses(t *testing.T) {
	got := primitive.Schedules(t.Context(), &holding{held: []worker.Schedule{aCron()}})
	require.Len(t, got, 1)

	// THE ASSERTION IS OVER THE DESTINATION'S OWN FIELDS, so a field added to
	// the service's shape and forgotten here fails rather than being a zero
	// nobody noticed.
	value := reflect.ValueOf(got[0])
	for i := range value.NumField() {
		require.False(t, value.Field(i).IsZero(),
			"%s did not cross from the worker's schedule", value.Type().Field(i).Name)
	}

	from, to := aCron(), got[0]
	require.Equal(t, from.Handle, to.Handle)
	require.Equal(t, from.ID, to.ID)
	require.Equal(t, from.Name, to.Name)
	require.Equal(t, from.Path, to.Path)
	require.Equal(t, from.Spec, to.Spec)
	require.Equal(t, from.Timezone, to.Timezone)
	require.Equal(t, from.OnOverlap, to.OnOverlap)
	require.Equal(t, from.Jitter, to.Jitter)
	require.Equal(t, from.Starts, to.Starts)
	require.Equal(t, from.Refused, to.Refused)
}

func TestEveryFieldOfATargetCrosses(t *testing.T) {
	got := primitive.Schedules(t.Context(), &holding{held: []worker.Schedule{aCron()}})
	require.Len(t, got[0].Fires, 1)

	fired := reflect.ValueOf(got[0].Fires[0])
	for i := range fired.NumField() {
		require.False(t, fired.Field(i).IsZero(),
			"a target's %s did not cross", fired.Type().Field(i).Name)
	}

	want := aCron().Fires[0]
	require.Equal(t, primitives.Target{
		Project:     want.Project,
		Environment: want.Environment,
		Scope:       want.Scope,
		Kind:        want.Kind,
		Name:        want.Name,
	}, got[0].Fires[0])
}

func TestANodeWithNoWorkerHoldsNoSchedules(t *testing.T) {
	require.Nil(t, primitive.Schedules(t.Context(), nil))
}

func TestAWorkerHoldingNoCronsAnswersEmpty(t *testing.T) {
	holds := &holding{}
	got := primitive.Schedules(t.Context(), holds)
	require.Empty(t, got)
	require.Equal(t, 1, holds.asked)
}

func TestACronThatFiresNothingStillCrosses(t *testing.T) {
	bare := worker.Schedule{Handle: "shop/prod/QmIdle", Name: "idle", Refused: "every edge was refused"}

	got := primitive.Schedules(t.Context(), &holding{held: []worker.Schedule{bare}})
	require.Len(t, got, 1)
	require.Empty(t, got[0].Fires)
	require.Empty(t, got[0].Starts)
	require.Equal(t, "every edge was refused", got[0].Refused)
}
