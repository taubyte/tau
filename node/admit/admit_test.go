package admit_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node/admit"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/services/worker"
)

// The decision with all three collaborators faked: which of three answers a caller
// gets, and that none is silently a yes. A real validator end to end is
// node/dream/instance_test.go.

// storing is a Rows double: the replicated row an admission is recorded in.
type storing struct {
	mu   sync.Mutex
	held map[string][]byte

	getErr  error
	putErr  error
	listErr error
}

func (s *storing) Get(_ context.Context, key string) ([]byte, bool, error) {
	if s.getErr != nil {
		return nil, false, s.getErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, held := s.held[key]
	return value, held, nil
}

func (s *storing) Put(_ context.Context, key string, value []byte) error {
	if s.putErr != nil {
		return s.putErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.held == nil {
		s.held = map[string][]byte{}
	}
	s.held[key] = value
	return nil
}

func (s *storing) List(_ context.Context, prefix string) ([]string, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for key := range s.held {
		if strings.HasPrefix(key, prefix) {
			out = append(out, key)
		}
	}
	return out, nil
}

// opening is a Stores double, and it counts: the database is opened once on the
// first admission that needs one, not per decision.
type opening struct {
	rows  admit.Rows
	err   error
	asked int
}

func (o *opening) Store() (admit.Rows, error) {
	o.asked++
	if o.err != nil {
		return nil, o.err
	}
	return o.rows, nil
}

// deciding records what a control was asked and answers with what it was told
// to. It stands in for the component tier and not for the validator: what a REAL
// validator does with a real opening is proven against fixtures/admitter in
// pkg/invoke/wasm, and end to end in node/dream.
type deciding struct {
	verdict wasmtier.Verdict
	err     error

	admission *invoke.Admission
	admitErr  error

	mu       sync.Mutex
	openings []wasmtier.Opening
	ran      []invoke.Spec
	requests []*invoke.Request
}

func (d *deciding) Admit(_ context.Context, spec invoke.Spec, o wasmtier.Opening) (wasmtier.Verdict, error) {
	d.mu.Lock()
	d.openings = append(d.openings, o)
	d.ran = append(d.ran, spec)
	d.mu.Unlock()
	return d.verdict, d.err
}

func (d *deciding) AuthorizeRequest(_ context.Context, spec invoke.Spec, req *invoke.Request) (*invoke.Admission, error) {
	d.mu.Lock()
	d.ran = append(d.ran, spec)
	d.requests = append(d.requests, req)
	d.mu.Unlock()
	return d.admission, d.admitErr
}

func (d *deciding) seen() []wasmtier.Opening {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]wasmtier.Opening(nil), d.openings...)
}

func (d *deciding) instantiated() []invoke.Spec {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]invoke.Spec(nil), d.ran...)
}

// resolving is a Functions double: the spec of the function a target named.
type resolving struct {
	spec invoke.Spec
	err  error

	mu      sync.Mutex
	targets []worker.Target
}

func (r *resolving) Resolve(_ context.Context, t worker.Target) (invoke.Spec, error) {
	r.mu.Lock()
	r.targets = append(r.targets, t)
	r.mu.Unlock()
	return r.spec, r.err
}

func (r *resolving) asked() []worker.Target {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]worker.Target(nil), r.targets...)
}

// The paths the composer writes for an `instances:` block. They are spelled
// literally here and held to admit.Settings below, which node/settings_test.go
// holds to the vocabulary — so a renamed attribute fails somewhere instead of
// silently reading as an absent key.
const (
	keyAdmission = "instances/admission"
	keyLimit     = "instances/limit"
	keyValidator = "instances/validator/name"
	keyScope     = "instances/validator/namespace"
)

func TestTheSettingsItPublishesAreTheOnesItReads(t *testing.T) {
	require.ElementsMatch(t, []string{keyAdmission, keyLimit, keyValidator, keyScope}, admit.Settings)
}

// manyGrant is a store that holds many instances, as the composer writes one.
func manyGrant(limit string) invoke.Grant {
	return invoke.Grant{
		Kind: "cache", Label: "sessions", Handle: specs.Handle("shop", "prod", "QmSessions"),
		Ops: []string{"get", "put"},
		Settings: map[string]string{
			keyAdmission: "validator",
			keyValidator: "may_open",
			keyLimit:     limit,
		},
	}
}

// validatorSpec is what a worker answers with for `may_open`: the function's own
// spec, with its own artifact and its own limits.
func validatorSpec() invoke.Spec {
	return invoke.Spec{
		Project: "shop", Environment: "prod",
		Kind: "function", Label: "may_open", Name: "may_open",
		Artifact: "bafybeigdyrztktx5b5m2y4sogf2hf5uq3k5knv5f4hkk7k6wbcnvzmrrfu",
	}
}

// admitting is the adapter with all three collaborators faked, which is the
// whole of what it depends on.
func admitting(t *testing.T, answers *deciding) (*admit.Admissions, *resolving, *storing) {
	t.Helper()

	found := &resolving{spec: validatorSpec()}
	rows := &storing{}
	a, err := admit.New(
		func() admit.Tier { return answers },
		func() admit.Functions { return found },
		&opening{rows: rows})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, a.Close()) })
	return a, found, rows
}

func saysYes() *deciding { return &deciding{verdict: wasmtier.Verdict{Admitted: true}} }

func TestAnAdmitterWithoutItsCollaboratorsIsRefused(t *testing.T) {
	answers := saysYes()
	tier := func() admit.Tier { return answers }
	functions := func() admit.Functions { return &resolving{} }
	stores := &opening{rows: &storing{}}

	for _, one := range []struct {
		name  string
		build func() (*admit.Admissions, error)
		says  string
	}{
		{"no tier", func() (*admit.Admissions, error) { return admit.New(nil, functions, stores) }, "control"},
		{"no functions", func() (*admit.Admissions, error) { return admit.New(tier, nil, stores) }, "function"},
		{"nowhere to record", func() (*admit.Admissions, error) { return admit.New(tier, functions, nil) }, "record"},
	} {
		t.Run(one.name, func(t *testing.T) {
			_, err := one.build()
			require.Error(t, err)
			require.ErrorContains(t, err, one.says)
		})
	}

	a, err := admit.New(tier, functions, stores)
	require.NoError(t, err)
	require.NotNil(t, a)
}

func TestTheStoreIsOpenedOnceAndNotBeforeItIsNeeded(t *testing.T) {
	answers := saysYes()
	stores := &opening{rows: &storing{}}
	a, err := admit.New(
		func() admit.Tier { return answers },
		func() admit.Functions { return &resolving{spec: validatorSpec()} },
		stores)
	require.NoError(t, err)

	require.Zero(t, stores.asked, "a database was opened before anything asked about an instance")

	for _, name := range []string{"a", "b", "c"} {
		require.NoError(t, a.Admit(t.Context(), invoke.Spec{}, manyGrant("0"), name))
	}
	require.Equal(t, 1, stores.asked)
}

func TestAStoreThatCannotBeOpenedRefusesTheAdmission(t *testing.T) {
	answers := saysYes()
	missing := errors.New("no database factory to open a cache with")
	a, err := admit.New(
		func() admit.Tier { return answers },
		func() admit.Functions { return &resolving{spec: validatorSpec()} },
		&opening{err: missing})
	require.NoError(t, err)

	err = a.Admit(t.Context(), invoke.Spec{}, manyGrant("0"), "room-42")
	require.ErrorIs(t, err, missing)
	require.NotErrorIs(t, err, wasmtier.ErrNotAdmitted, "the store failed; nobody turned the caller down")
	require.Empty(t, answers.seen(), "the validator was asked about an instance that could not be recorded")
}

func TestAdmittingInvokesTheValidator(t *testing.T) {
	answers := saysYes()
	a, found, _ := admitting(t, answers)

	caller := invoke.Spec{Project: "shop", Environment: "prod", Kind: "function", Label: "chat", Scope: []string{"billing"}}
	require.NoError(t, a.Admit(t.Context(), caller, manyGrant("0"), "room-42"))

	seen := answers.seen()
	require.Len(t, seen, 1)
	require.Equal(t, "room-42", seen[0].Instance)
	require.Equal(t, "sessions", seen[0].What.Label, "a workload deciding for two stores has nothing else to tell them apart by")
	require.Equal(t, "cache", seen[0].What.Kind)
	require.Equal(t, "chat", seen[0].Who.Name, "and who was asking")
	require.Equal(t, []string{"billing"}, seen[0].Who.Scope)

	// THE VALIDATOR'S OWN SPEC, resolved like any other function — its own
	// access block, its own limits, its own artifact.
	require.Equal(t, []invoke.Spec{validatorSpec()}, answers.instantiated())
	require.Equal(t, []worker.Target{{
		Project: "shop", Environment: "prod", Kind: "function", Name: "may_open",
	}}, found.asked())
}

func TestTheValidatorIsLookedForInTheNamespaceTheBlockNames(t *testing.T) {
	for _, one := range []struct {
		namespace string
		scope     []string
	}{
		{"", nil},
		{"/", nil},
		{"/billing", []string{"billing"}},
		{"billing/local", []string{"billing", "local"}},
		{"/billing/local/", []string{"billing", "local"}},
	} {
		t.Run(one.namespace, func(t *testing.T) {
			a, found, _ := admitting(t, saysYes())

			grant := manyGrant("0")
			grant.Settings[keyScope] = one.namespace
			require.NoError(t, a.Admit(t.Context(), invoke.Spec{}, grant, "room-42"))

			require.Len(t, found.asked(), 1)
			require.Equal(t, one.scope, found.asked()[0].Scope)
		})
	}
}

func TestAValidatorThatRefuses(t *testing.T) {
	a, _, rows := admitting(t, &deciding{verdict: wasmtier.Verdict{Refused: "over its plan"}})

	err := a.Admit(t.Context(), invoke.Spec{}, manyGrant("0"), "room-42")
	require.ErrorIs(t, err, wasmtier.ErrNotAdmitted)
	require.ErrorContains(t, err, "may_open")
	require.ErrorContains(t, err, "over its plan", "the validator's own words reach the caller")
	require.Empty(t, rows.held, "a refusal was recorded as an admission")
}

func TestAValidatorThatDoesNotDecideIsNotARefusal(t *testing.T) {
	a, _, rows := admitting(t, &deciding{verdict: wasmtier.Verdict{Undecided: "the plan service did not answer"}})

	err := a.Admit(t.Context(), invoke.Spec{}, manyGrant("0"), "room-42")
	require.Error(t, err)
	require.NotErrorIs(t, err, wasmtier.ErrNotAdmitted, "nothing decided, so nobody was turned down")
	require.ErrorContains(t, err, "did not decide")
	require.Empty(t, rows.held)
}

func TestAValidatorThatCannotRunIsNotARefusal(t *testing.T) {
	a, _, rows := admitting(t, &deciding{err: errors.New("the component would not start")})

	err := a.Admit(t.Context(), invoke.Spec{}, manyGrant("0"), "room")
	require.Error(t, err)
	require.NotErrorIs(t, err, wasmtier.ErrNotAdmitted)
	require.ErrorContains(t, err, "could not be run")
	require.Empty(t, rows.held)
}

func TestAValidatorThatCannotBeResolvedIsNotARefusal(t *testing.T) {
	answers := saysYes()
	a, found, rows := admitting(t, answers)
	found.err = errors.New("no such function")

	err := a.Admit(t.Context(), invoke.Spec{}, manyGrant("0"), "room")
	require.Error(t, err)
	require.NotErrorIs(t, err, wasmtier.ErrNotAdmitted)
	require.ErrorContains(t, err, "could not be resolved")
	require.Empty(t, answers.seen(), "a spec that failed to resolve was instantiated anyway")
	require.Empty(t, rows.held)
}

func TestNoFailureHereIsEverAYes(t *testing.T) {
	for _, one := range []struct {
		name  string
		tier  *deciding
		rows  *storing
		found error
	}{
		{name: "refused", tier: &deciding{verdict: wasmtier.Verdict{Refused: "no"}}},
		{name: "undecided", tier: &deciding{verdict: wasmtier.Verdict{Undecided: "nothing answered"}}},
		{name: "could not run", tier: &deciding{err: errors.New("would not start")}},
		{name: "could not resolve", tier: saysYes(), found: errors.New("no such function")},
		{name: "row unreadable", tier: saysYes(), rows: &storing{getErr: errors.New("store is gone")}},
		{name: "count unreadable", tier: saysYes(), rows: &storing{listErr: errors.New("store is gone")}},
		{name: "row unwritable", tier: saysYes(), rows: &storing{putErr: errors.New("store is gone")}},
	} {
		t.Run(one.name, func(t *testing.T) {
			rows := one.rows
			if rows == nil {
				rows = &storing{}
			}
			a, err := admit.New(
				func() admit.Tier { return one.tier },
				func() admit.Functions { return &resolving{spec: validatorSpec(), err: one.found} },
				&opening{rows: rows})
			require.NoError(t, err)

			require.Error(t, a.Admit(t.Context(), invoke.Spec{}, manyGrant("1"), "room-42"),
				"this is not a path that admits anything")
		})
	}
}

func TestACeilingThatCannotBeCountedRefuses(t *testing.T) {
	answers := saysYes()
	broken := errors.New("store is gone")
	a, err := admit.New(
		func() admit.Tier { return answers },
		func() admit.Functions { return &resolving{spec: validatorSpec()} },
		&opening{rows: &storing{listErr: broken}})
	require.NoError(t, err)

	require.ErrorIs(t, a.Admit(t.Context(), invoke.Spec{}, manyGrant("2"), "room"), broken)
	require.Empty(t, answers.seen())
}

func TestAnAdmittedInstanceIsNotAskedAgain(t *testing.T) {
	answers := saysYes()
	a, _, rows := admitting(t, answers)

	for range 3 {
		require.NoError(t, a.Admit(t.Context(), invoke.Spec{}, manyGrant("0"), "room-42"))
	}
	require.Len(t, answers.seen(), 1, "the validator was asked more than once")

	// AND THE ROW IS THE AUTHORITY RATHER THAN THIS NODE'S MEMORY: a second
	// admitter over the same rows reads it and asks nobody. That is the property
	// a validator is asked once per instance per CLOUD by, not once per node.
	fresh, err := admit.New(
		func() admit.Tier { return answers },
		func() admit.Functions { return &resolving{spec: validatorSpec()} },
		&opening{rows: rows})
	require.NoError(t, err)
	require.NoError(t, fresh.Admit(t.Context(), invoke.Spec{}, manyGrant("0"), "room-42"))
	require.Len(t, answers.seen(), 1)
}

func TestClosingForgetsTheMemoryAndNotTheRows(t *testing.T) {
	answers := saysYes()
	a, _, rows := admitting(t, answers)

	require.NoError(t, a.Admit(t.Context(), invoke.Spec{}, manyGrant("0"), "room-42"))
	require.NoError(t, a.Close())
	require.NotEmpty(t, rows.held, "closing dropped the decision")

	require.NoError(t, a.Admit(t.Context(), invoke.Spec{}, manyGrant("0"), "room-42"))
	require.Len(t, answers.seen(), 1, "the row was there and the validator was asked again")
}

func TestARowThatCannotBeReadRefuses(t *testing.T) {
	answers := saysYes()
	broken := errors.New("store is gone")
	a, err := admit.New(
		func() admit.Tier { return answers },
		func() admit.Functions { return &resolving{spec: validatorSpec()} },
		&opening{rows: &storing{getErr: broken}})
	require.NoError(t, err)

	require.ErrorIs(t, a.Admit(t.Context(), invoke.Spec{}, manyGrant("0"), "room"), broken)
	require.Empty(t, answers.seen())
}

func TestTheLimitRefusesFurtherInstances(t *testing.T) {
	a, _, _ := admitting(t, saysYes())

	require.NoError(t, a.Admit(t.Context(), invoke.Spec{}, manyGrant("2"), "a"))
	require.NoError(t, a.Admit(t.Context(), invoke.Spec{}, manyGrant("2"), "b"))

	err := a.Admit(t.Context(), invoke.Spec{}, manyGrant("2"), "c")
	require.ErrorIs(t, err, wasmtier.ErrNotAdmitted)
	require.ErrorContains(t, err, "limit is 2")

	// AND ONE ALREADY ADMITTED IS STILL ADMITTED. A full store still serves
	// what is in it.
	require.NoError(t, a.Admit(t.Context(), invoke.Spec{}, manyGrant("2"), "a"))
}

func TestALimitThatSaysNothingIsNoCeiling(t *testing.T) {
	for _, limit := range []string{"0", "", "lots", "-1"} {
		t.Run(limit, func(t *testing.T) {
			a, _, _ := admitting(t, saysYes())

			for _, name := range []string{"a", "b", "c", "d"} {
				require.NoError(t, a.Admit(t.Context(), invoke.Spec{}, manyGrant(limit), name))
			}
		})
	}
}

func TestInstancesAreCountedPerStore(t *testing.T) {
	a, _, _ := admitting(t, saysYes())

	other := manyGrant("1")
	other.Handle = specs.Handle("shop", "prod", "QmOther")

	require.NoError(t, a.Admit(t.Context(), invoke.Spec{}, manyGrant("1"), "only"))
	require.NoError(t, a.Admit(t.Context(), invoke.Spec{}, other, "only"),
		"a second store's first instance was counted against the first store")
}

func TestAnInstanceNameThatIsNotOneSegmentIsRefused(t *testing.T) {
	for _, name := range []string{"", ".", "..", "a/b", "../QmOther"} {
		t.Run(name, func(t *testing.T) {
			answers := saysYes()
			a, _, rows := admitting(t, answers)

			err := a.Admit(t.Context(), invoke.Spec{}, manyGrant("0"), name)
			require.ErrorIs(t, err, wasmtier.ErrNotAdmitted)
			require.Empty(t, answers.seen(), "a validator was asked about a name that is not one")
			require.Empty(t, rows.held)
		})
	}
}

func TestAStoreWithNoValidatorIsRefused(t *testing.T) {
	a, _, _ := admitting(t, saysYes())

	grant := manyGrant("0")
	delete(grant.Settings, keyValidator)

	require.ErrorIs(t, a.Admit(t.Context(), invoke.Spec{}, grant, "room"), admit.ErrNoValidator)
}

func TestANodeThatCannotRunAValidatorRefusesByName(t *testing.T) {
	answers := saysYes()

	for _, one := range []struct {
		name      string
		tier      func() admit.Tier
		functions func() admit.Functions
	}{
		{"no component tier", func() admit.Tier { return nil }, func() admit.Functions { return &resolving{} }},
		{"no worker", func() admit.Tier { return answers }, func() admit.Functions { return nil }},
		{"neither", func() admit.Tier { return nil }, func() admit.Functions { return nil }},
	} {
		t.Run(one.name, func(t *testing.T) {
			a, err := admit.New(one.tier, one.functions, &opening{rows: &storing{}})
			require.NoError(t, err)

			err = a.Admit(t.Context(), invoke.Spec{}, manyGrant("0"), "room")
			require.ErrorIs(t, err, admit.ErrNoValidator)
			require.ErrorContains(t, err, "cannot run one")
		})
	}
}

func TestAMalformedHandleIsNotInvoked(t *testing.T) {
	for _, handle := range []string{"nonsense", "shop/prod", "shop//QmSessions", "/prod/QmSessions"} {
		t.Run(handle, func(t *testing.T) {
			answers := saysYes()
			a, found, _ := admitting(t, answers)

			grant := manyGrant("0")
			grant.Handle = handle

			err := a.Admit(t.Context(), invoke.Spec{}, grant, "room")
			require.Error(t, err)
			require.ErrorContains(t, err, "does not name a deployment")
			require.Empty(t, answers.seen())
			require.Empty(t, found.asked(), "a target was built out of a handle that names nothing")
		})
	}
}

func TestABrowserAndAWorkloadAskTheSameValidator(t *testing.T) {
	answers := saysYes()
	a, _, _ := admitting(t, answers)

	require.NoError(t, a.AdmitInstance(t.Context(), specs.Handle("shop", "prod", "QmSessions"),
		manyGrant("0").Settings, "room-42"))

	seen := answers.seen()
	require.Len(t, seen, 1)
	require.Equal(t, "room-42", seen[0].Instance)

	// A BROWSER IS NOT A WORKLOAD AND SAYS SO BY ITS KIND BEING EMPTY. The
	// deployment it is attaching to is known — it is in the handle — and
	// nothing else about the caller is, because it came from the internet.
	require.Equal(t, "shop", seen[0].Who.Project)
	require.Equal(t, "prod", seen[0].Who.Environment)
	require.Empty(t, seen[0].Who.Kind)
	require.Empty(t, seen[0].Who.Name)

	// The store is named by the id, because a browser never saw the author's
	// label and the id is better than nothing in a log.
	require.Equal(t, "QmSessions", seen[0].What.Label)
	require.Equal(t, "bus", seen[0].What.Kind)

	// AND THE CEILING IS THE SAME ONE. A browser admitted here is an instance
	// held, so a workload asking next is asking about an instance that exists.
	require.NoError(t, a.Admit(t.Context(),
		invoke.Spec{Project: "shop", Environment: "prod"}, manyGrant("0"), "room-42"))
	require.Len(t, answers.seen(), 1, "the same instance was not validated twice")
}

func TestABrowserTurnedAwayIsARefusal(t *testing.T) {
	a, _, _ := admitting(t, &deciding{verdict: wasmtier.Verdict{Refused: "that room is full"}})

	err := a.AdmitInstance(t.Context(), specs.Handle("shop", "prod", "QmSessions"), manyGrant("0").Settings, "room-42")
	require.ErrorIs(t, err, wasmtier.ErrNotAdmitted)
	require.ErrorContains(t, err, "that room is full")
}

func TestAGuardedRouteAsksTheFunctionItNames(t *testing.T) {
	answers := saysYes()
	answers.admission = &invoke.Admission{Granted: []string{"listen"}}
	a, found, _ := admitting(t, answers)

	at := specs.Address{Project: "shop", Environment: "prod", Scope: []string{"billing"}, Kind: "function", Name: "doorman"}
	request := &invoke.Request{Method: "GET", Path: "/rooms/42"}

	got, err := a.AdmitRequest(t.Context(), at, request)
	require.NoError(t, err)
	require.Equal(t, []string{"listen"}, got.Granted)

	// THE GUARD'S OWN SPEC, resolved like any other function, and in the scope
	// the route named — a guard found under the project root instead would be a
	// different function with the same name.
	require.Equal(t, []worker.Target{{
		Project: "shop", Environment: "prod", Scope: []string{"billing"},
		Kind: "function", Name: "doorman",
	}}, found.asked())
	require.Equal(t, []invoke.Spec{validatorSpec()}, answers.instantiated())
	require.Equal(t, []*invoke.Request{request}, answers.requests)
}

func TestARouteGuardedByAFunctionThisNodeCannotRun(t *testing.T) {
	answers := saysYes()

	for _, one := range []struct {
		name      string
		tier      func() admit.Tier
		functions func() admit.Functions
	}{
		{"no component tier", func() admit.Tier { return nil }, func() admit.Functions { return &resolving{} }},
		{"no worker", func() admit.Tier { return answers }, func() admit.Functions { return nil }},
	} {
		t.Run(one.name, func(t *testing.T) {
			a, err := admit.New(one.tier, one.functions, &opening{rows: &storing{}})
			require.NoError(t, err)

			got, err := a.AdmitRequest(t.Context(), specs.Address{Name: "doorman"}, &invoke.Request{})
			require.Nil(t, got, "a guard that cannot run answered with ops")
			require.ErrorIs(t, err, admit.ErrNoGuard)
			require.ErrorContains(t, err, "doorman")
		})
	}
}

func TestAGuardThatCannotBeResolvedRefusesTheRequest(t *testing.T) {
	answers := saysYes()
	a, found, _ := admitting(t, answers)
	found.err = errors.New("no such function")

	got, err := a.AdmitRequest(t.Context(), specs.Address{Name: "doorman"}, &invoke.Request{})
	require.Nil(t, got)
	require.ErrorContains(t, err, "resolving the authorizer")
	require.Empty(t, answers.requests)
}

func TestAGuardThatFailsToRunRefusesTheRequest(t *testing.T) {
	answers := saysYes()
	answers.admitErr = errors.New("the component would not start")
	a, _, _ := admitting(t, answers)

	got, err := a.AdmitRequest(t.Context(), specs.Address{Name: "doorman"}, &invoke.Request{})
	require.Nil(t, got)
	require.Error(t, err)
}

func TestConcurrentFirstOpensOpenOneStore(t *testing.T) {
	answers := saysYes()
	stores := &opening{rows: &storing{}}
	a, err := admit.New(
		func() admit.Tier { return answers },
		func() admit.Functions { return &resolving{spec: validatorSpec()} },
		stores)
	require.NoError(t, err)

	var mu sync.Mutex
	var failed []error

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// THE ASSERTION IS MADE ON THE TEST'S OWN GOROUTINE, because
			// FailNow from one of these would stop that goroutine and leave
			// this one waiting on a group that never completes.
			if err := a.Admit(t.Context(), invoke.Spec{}, manyGrant("0"), "room-42"); err != nil {
				mu.Lock()
				failed = append(failed, err)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	require.Empty(t, failed)
	require.Equal(t, 1, stores.asked, "two concurrent cold opens opened two stores")

	// HOW MANY TIMES THE VALIDATOR WAS ASKED IS NOT ASSERTED, and that is the
	// truth rather than a gap in this test: the lock is not held across the
	// invocation — holding it would serialise every cold open on the node
	// behind one component start — so sixteen opens of one instance that all
	// miss the row can each ask. It is the same race two NODES have, and the
	// row that lands is the same value whoever wrote it.
}
