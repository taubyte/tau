// Package admit decides whether one instance of a store that holds many may exist,
// and whether one request may pass a route's guard. It is an adapter between askers
// (a wasm tier with a grant, a gateway with a browser) and answerers (a worker that
// finds the named function, a component tier that runs it, replicated rows that
// remember), taking each as a narrow interface rather than the node.
//
// The validator is a workload, which the wasm tier cannot invoke. An admission is
// recorded in a replicated row, the same value whoever writes it, so a validator is
// asked once per instance per cloud. `instances/limit` is counted by scanning
// admissions, so racing admissions can overshoot once; a counter would be a
// contended key. A limit of 0 is no ceiling.
//
// Three answers, all errors except a yes: refused (the validator said no, carrying
// wasmtier.ErrNotAdmitted), undecided, and could not be run. Nothing answers yes on a
// path that did not reach one. What a validator returns beyond yes or no (a ttl, a
// size) is not modelled.
package admit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/specs"
	gateway "github.com/taubyte/tau/services/gateway"
	"github.com/taubyte/tau/services/worker"
)

// Tier runs a control: a validator asked about an instance, or a guard asked about a
// request. Two methods only, so a control cannot instantiate other worlds.
type Tier interface {
	// Admit runs one instance validator and answers the verdict it gave.
	Admit(ctx context.Context, spec invoke.Spec, o wasmtier.Opening) (wasmtier.Verdict, error)

	// AuthorizeRequest runs one http authorizer and answers the ops the
	// request gets. The world is checked by whatever instantiates: a function
	// that implements anything else is refused before it runs, which for a
	// control is the difference between a refusal and a component answering a
	// question it read as a request.
	AuthorizeRequest(ctx context.Context, spec invoke.Spec, req *invoke.Request) (*invoke.Admission, error)
}

// Functions finds the workload a target named; one method, not the worker.
type Functions interface {
	Resolve(ctx context.Context, t worker.Target) (invoke.Spec, error)
}

// Rows is where a decision is recorded: read one row, count a prefix, write one row.
// "Not there" is a bool, the ordinary first-open answer, not an error.
type Rows interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Put(ctx context.Context, key string, value []byte) error
	List(ctx context.Context, prefix string) ([]string, error)
}

// Stores opens where admissions are recorded, lazily, so a node never asked opens no
// database and one without a store refuses on first admission.
type Stores interface {
	Store() (Rows, error)
}

var (
	// ErrNoValidator is a store that declares an admission this node cannot
	// run — an `instances:` block naming no function, or a node with nothing
	// to run one with.
	ErrNoValidator = errors.New("admit: this store admits instances through a function and this node cannot run one")

	// ErrNoGuard is a route that names an authorizer this node cannot run.
	ErrNoGuard = errors.New("admit: this route is guarded by a function and this node cannot run one")
)

// The setting keys services/worker writes for the `instances:` block.
const (
	settingAdmission = specs.InstancesKeyAdmission
	settingLimit     = specs.InstancesKeyLimit
	settingValidator = specs.InstancesKeyValidatorName
	settingScope     = specs.InstancesKeyValidatorNamespace
)

// Settings is every attribute path this adapter reads off a grant, exported so
// instance/settings_test.go can hold it to the vocabulary: a renamed attribute would
// silently become no ceiling.
var Settings = []string{settingAdmission, settingLimit, settingValidator, settingScope}

// Admissions answers every admission this node makes. Construct it with New.
type Admissions struct {
	tier      func() Tier
	functions func() Functions
	stores    Stores

	mu   sync.Mutex
	rows Rows

	// seen is what this node has already admitted, so a hot instance costs
	// nothing after the first open. It is a cache of the row and not the
	// authority: a node that has not seen one reads the store.
	seen map[string]bool
}

var (
	_ wasmtier.Admitter = (*Admissions)(nil)
	_ gateway.Admits    = (*Admissions)(nil)
)

// New wires the adapter. tier and functions are functions because the component tier
// takes this as a collaborator and the worker is composed around it; each may answer
// nil, but must not answer a typed nil in an interface. stores may not be nil: an
// unrecorded admission would invoke the validator on every open.
func New(tier func() Tier, functions func() Functions, stores Stores) (*Admissions, error) {
	if tier == nil {
		return nil, errors.New("admit: no way to ask whether a control can be run")
	}
	if functions == nil {
		return nil, errors.New("admit: no way to find the function a target named")
	}
	if stores == nil {
		return nil, errors.New("admit: nowhere to record a decision")
	}
	return &Admissions{tier: tier, functions: functions, stores: stores}, nil
}

// controls is the pair every decision here needs: something to run and
// something to find what to run. Either being absent is one fact — this node
// cannot run a control — so it is answered once rather than at four call sites.
func (a *Admissions) controls() (Tier, Functions, bool) {
	tier, functions := a.tier(), a.functions()
	if tier == nil || functions == nil {
		return nil, nil, false
	}
	return tier, functions, true
}

// Admit runs the target's validator, once per instance, and holds the ceiling.
// It is what the wasm tier asks.
func (a *Admissions) Admit(ctx context.Context, spec invoke.Spec, grant invoke.Grant, instance string) error {
	return a.admit(ctx, wasmtier.Caller{
		Project:     spec.Project,
		Environment: spec.Environment,
		Kind:        spec.Kind,
		Name:        spec.Label,
		Scope:       spec.Scope,
	}, grant, instance)
}

// AdmitInstance is the gateway's Admits: the same decision asked about a browser, one
// implementation so one config block has one policy.
func (a *Admissions) AdmitInstance(ctx context.Context, handle string, settings map[string]string, instance string) error {
	// A BROWSER IS NOT A WORKLOAD AND SAYS SO BY ITS KIND BEING EMPTY, which
	// the world declares as the rule rather than leaving it to be inferred.
	// The deployment it is attaching to is known — it is in the handle — and
	// nothing else about the caller is, because there is nothing else: it came
	// from the internet.
	project, environment, _ := specs.SplitHandle(handle)
	return a.admit(ctx, wasmtier.Caller{Project: project, Environment: environment}, invoke.Grant{
		Kind: specs.KindBus, Label: labelOf(handle), Handle: handle, Settings: settings,
	}, instance)
}

// AdmitRequest is the gateway's other Admits: whether a request may pass, and with
// what ops. It runs on the node that took the connection: a guard has no warmth to
// route toward.
func (a *Admissions) AdmitRequest(ctx context.Context, at specs.Address, req *invoke.Request) (*invoke.Admission, error) {
	tier, functions, can := a.controls()
	if !can {
		return nil, fmt.Errorf("%w: %s", ErrNoGuard, at.Name)
	}

	// THE GUARD'S OWN SPEC, resolved like any other function -- its own access
	// block, its own limits, its own artifact. Nothing about being an
	// authorizer changes how it is found.
	guard, err := functions.Resolve(ctx, worker.Target{
		Project:     at.Project,
		Environment: at.Environment,
		Scope:       at.Scope,
		Kind:        specs.KindFunction,
		Name:        at.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("admit: resolving the authorizer %q: %w", at.Name, err)
	}
	return tier.AuthorizeRequest(ctx, guard, req)
}

// admit is the decision, whoever asked; the caller is a parameter since a browser has
// no spec.
func (a *Admissions) admit(ctx context.Context, who wasmtier.Caller, grant invoke.Grant, instance string) error {
	// The instance name is a key the store path-cleans: `..` resolves to the deployment's
	// prefix and would admit every instance of every store. Checked here, where the wasm
	// tier and the gateway (with URL segments) meet.
	if err := singleSegment(instance); err != nil {
		return err
	}

	key := grant.Handle + "/" + instance

	a.mu.Lock()
	known := a.seen[key]
	a.mu.Unlock()
	if known {
		return nil
	}

	rows, err := a.store()
	if err != nil {
		return err
	}

	// ALREADY ADMITTED SOMEWHERE. The row is the decision and it replicates, so
	// a validator is asked once per instance per cloud rather than once per
	// node — which matters because a validator may have side effects.
	if _, found, err := rows.Get(ctx, key); err != nil {
		return err
	} else if found {
		a.remember(key)
		return nil
	}

	if err := a.room(ctx, rows, grant); err != nil {
		return err
	}
	if err := a.ask(ctx, who, grant, instance); err != nil {
		return err
	}

	// THE ROW IS A MARKER AND NOT A RECORD OF THE DECISION'S PARAMETERS. What a
	// validator returns beyond yes is not modelled, so writing it here would be
	// inventing a shape; what is written is that this instance exists, which is
	// what the ceiling counts and what the next open reads.
	if err := rows.Put(ctx, key, []byte(instance)); err != nil {
		return err
	}
	a.remember(key)
	return nil
}

// singleSegment refuses an instance name that is not one clean path segment.
func singleSegment(instance string) error {
	if instance == "" || instance == "." || instance == ".." || strings.Contains(instance, "/") {
		return fmt.Errorf("%w: %q is not an instance name", wasmtier.ErrNotAdmitted, instance)
	}
	return nil
}

// room refuses an admission that would take the store past its ceiling.
func (a *Admissions) room(ctx context.Context, rows Rows, grant invoke.Grant) error {
	limit := grant.Number(settingLimit)
	if limit <= 0 {
		// Unparseable is treated as absent, and absent is no ceiling — the
		// vocabulary requires the field, so a missing one means the composer
		// did not read it, and refusing every instance over a field nobody
		// wrote would be an outage where this is a bug to find in a log. The
		// reading is [invoke.Grant.Number]'s, which every backing shares.
		return nil
	}

	held, err := rows.List(ctx, grant.Handle+"/")
	if err != nil {
		return err
	}
	if int64(len(held)) >= limit {
		return fmt.Errorf("%w: %s holds %d instances and its limit is %d",
			wasmtier.ErrNotAdmitted, grant.Label, len(held), limit)
	}
	return nil
}

// ask runs the validator as the vocabulary's validator world, whose verdict tells
// refused from undecided, which an HTTP status could not.
func (a *Admissions) ask(ctx context.Context, who wasmtier.Caller, grant invoke.Grant, instance string) error {
	name := grant.Settings[settingValidator]
	if name == "" {
		return fmt.Errorf("%w: %s", ErrNoValidator, grant.Label)
	}

	tier, functions, can := a.controls()
	if !can {
		return fmt.Errorf("%w: %s, and this node cannot run one", ErrNoValidator, grant.Label)
	}

	project, environment, id := specs.SplitHandle(grant.Handle)
	if project == "" || environment == "" || id == "" {
		return fmt.Errorf("admit: the handle %q does not name a deployment", grant.Handle)
	}

	validator, err := functions.Resolve(ctx, worker.Target{
		Project:     project,
		Environment: environment,
		Scope:       specs.ScopeOf(nil, grant.Settings[settingScope]),
		Kind:        specs.KindFunction,
		Name:        name,
	})
	if err != nil {
		return fmt.Errorf("admit: the validator %q for %s could not be resolved: %w", name, grant.Label, err)
	}

	verdict, err := tier.Admit(ctx, validator, wasmtier.Opening{
		Who:      who,
		What:     wasmtier.Store{Kind: grant.Kind, Label: grant.Label},
		Instance: instance,
	})
	if err != nil {
		// A VALIDATOR THAT COULD NOT BE RUN IS NOT A REFUSAL. Admitting anyway
		// would be granting what the target said somebody else decides; saying
		// "not admitted" would tell the caller their instance was turned down,
		// which is a different thing to act on.
		return fmt.Errorf("admit: the validator %q for %s could not be run: %w", name, grant.Label, err)
	}

	switch {
	case verdict.Admitted:
		return nil
	case verdict.Undecided != "":
		// Same distinction as above, now made by the validator rather than
		// inferred from a status code.
		return fmt.Errorf("admit: the validator %q for %s did not decide: %s", name, grant.Label, verdict.Undecided)
	}
	return fmt.Errorf("%w: %s refused %q: %s", wasmtier.ErrNotAdmitted, name, instance, verdict.Refused)
}

func (a *Admissions) remember(key string) {
	a.mu.Lock()
	if a.seen == nil {
		a.seen = map[string]bool{}
	}
	a.seen[key] = true
	a.mu.Unlock()
}

// store asks for the one place decisions are recorded, once. It runs under the
// same lock as the memory of them so two concurrent first-admissions cannot open
// two.
func (a *Admissions) store() (Rows, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.rows != nil {
		return a.rows, nil
	}
	rows, err := a.stores.Store()
	if err != nil {
		return nil, err
	}
	a.rows = rows
	return rows, nil
}

// labelOf is something to name the store by in a message. The handle carries
// the resolved id and a browser never saw the author's label, so the id is what
// there is — and it is better than nothing in a log.
func labelOf(handle string) string {
	_, _, id := specs.SplitHandle(handle)
	return id
}

// Close forgets what this node had admitted. The rows stay: they are the
// decision and they are not this node's.
func (a *Admissions) Close() error {
	a.mu.Lock()
	a.seen = nil
	a.mu.Unlock()
	return nil
}
