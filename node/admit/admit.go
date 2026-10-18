// Package admit decides whether one instance of a store that holds many may
// exist, and whether one request may pass a route's guard.
//
// # Why it is its own package
//
// It is an ADAPTER: it sits between two things that ask — a wasm tier that
// knows a grant, and a gateway that knows a browser — and three things that
// answer: a worker that can find the function a target named, a component tier
// that can run it, and a replicated row that remembers what was decided. The
// composition root is the only place that may NAME all of those — but naming
// them and implementing the decision are different jobs, and conflating them is
// how node/ grew to eight thousand lines with twenty-two of these in one
// namespace.
//
// So the root hands this three things it can ask. Everything this package needs
// is in its own signature, which is the property a type taking *Node does not
// have: that one can reach anything the node has, so nothing about it says what
// it actually depends on, and it cannot be built anywhere except beside the node.
//
// # The validator is a workload, so this is not in the tier
//
// The vocabulary's `instances:` block names a FUNCTION that admits an instance,
// which the wasm tier cannot invoke: it runs one workload and has no way to
// reach another. Something that holds BOTH a resolver and a tier can, and that
// is what this takes.
//
// # An admitted instance is remembered, because a validator is a request
//
// Asking on every call would put an invocation in front of every get, which is
// a store nobody would use. So a decision is recorded, and what records it is a
// replicated row: any node may admit, every node sees it, and the row is the
// same value whoever wrote it — which is the rule AGENTS.md states for every
// kvdb layout here.
//
// # The ceiling is eventually exact, and that is a real limit
//
// `instances/limit` is counted by scanning what has been admitted. Under a CRDT
// two nodes admitting at the same moment can both see room, so a limit may be
// exceeded by however many admissions raced — and then it holds. THE ALTERNATIVE
// IS A COUNTER, which is precisely the key a kvdb must never hold: a value two
// nodes write differently. The validator is the control that decides WHICH
// instances exist; the limit is a ceiling against runaway, and a ceiling that
// is occasionally one over is still a ceiling.
//
// A limit of 0 is "no ceiling", which the vocabulary makes a required sentence
// rather than a default nobody reached.
//
// # An error here never reads as permission
//
// There are three answers and not two. REFUSED is the validator saying no, and
// it is the ordinary answer: the target declared who decides and this is that
// decision. UNDECIDED is the validator answering that nothing decided — a plan
// service that did not reply — and telling a caller their instance was turned
// down when nothing decided sends them to argue with a policy that never fired.
// COULD NOT BE RUN is neither: admitting anyway would grant what the target said
// somebody else decides.
//
// Only the first carries [wasmtier.ErrNotAdmitted]. The other two are errors of
// their own, and every one of the three is an error — nothing here answers "yes"
// on a path that did not reach a yes.
//
// # What the validator is asked, and what it answers with
//
// It is invoked as its own world, and what it RETURNS beyond yes or no — an
// instance's ttl, its size — is deliberately not modelled, for the vocabulary's
// own reason: config declares which function decides and the parameters are the
// decision's.
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

// Tier runs a control: a validator asked about an instance, or a guard asked
// about a request.
//
// TWO METHODS AND NOT THE COMPONENT TIER, and not even the node's own narrowing
// of it: this decides nothing about a workload's ops and nothing about a name,
// so Authorize and Resolve would be two worlds it could instantiate for no
// reason. A control whose job is to be able to say no is the last place to hand
// a wider surface than the job needs.
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

// Functions finds the workload a target named.
//
// ONE METHOD AND NOT THE WORKER, because that one is all a control needs: the
// spec of the function the config names. A parameter typed as the worker would
// carry Invoke, Transform, Route, Direct, Shape and Conn along with it, none of
// which a decision about who may open a store has any business reaching.
type Functions interface {
	Resolve(ctx context.Context, t worker.Target) (invoke.Spec, error)
}

// Rows is where a decision is recorded.
//
// THREE METHODS AND NOT A kvdb, whose New also takes a logger and a rebroadcast
// interval — a node's decisions about a store, which an admission holds no
// opinion about — and whose result has to be registered with the closers the
// root keeps. What is left is what the decision needs: read one row, count a
// prefix, write one row.
//
// "NOT THERE" IS A bool AND NOT AN ERROR, because it is the ordinary answer here:
// an instance nobody has admitted yet is the case this whole package exists for,
// and a backing that reported it as an error would make every first open
// indistinguishable from a store that could not be read.
type Rows interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Put(ctx context.Context, key string, value []byte) error
	List(ctx context.Context, prefix string) ([]string, error)
}

// Stores opens the one place every admission on this node is recorded.
//
// ONE METHOD, ASKED LAZILY, so a node nothing ever asks about an instance opens
// no database — and a node whose operator gave it no store at all refuses by
// name on the first admission rather than at start, for a resource it may never
// touch.
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

// Settings is every attribute path this adapter reads off a grant.
//
// IT IS EXPORTED SO THE ROOT CAN HOLD IT TO THE VOCABULARY. A renamed attribute
// does not fail here: the key is absent, the read is a zero value, and a
// declared ceiling silently becomes no ceiling at all. node/settings_test.go
// walks this list against the schema, and its own doc carries the post-mortem
// that test came out of.
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

// New wires the adapter.
//
// BOTH FUNCTIONS ARE REQUIRED AND WHAT THEY ANSWER MAY BE NIL, and that is not a
// style choice: the component tier takes THIS as a collaborator, so it cannot
// exist when this is built, and the worker is composed around the tier. A struct
// that took either at construction would take nil on every node there is. What
// each function answers may be nil — a node with no component tier, a node whose
// worker is not open yet — and what it may not be is a typed nil in an
// interface: a nil *wasm.Component put in one is not nil, so every `== nil` here
// would pass and the first admission would panic. The function is the one place
// that check can be made.
//
// stores MAY NOT BE NIL, because an admission nothing records is a validator
// invoked on every open — and the root has something to hand over even on a node
// whose operator gave it no database factory: that store refuses, by name, the
// first time anything asks about an instance.
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

// AdmitInstance is the gateway's Admits: the same decision, asked about a
// BROWSER rather than a workload.
//
// IT IS ONE IMPLEMENTATION AND NOT TWO, which is the whole point. A workload
// opening `chat/room-42` and a browser attaching to `/room-42` are asking the
// same question of the same validator about the same instance, and two answers
// to it would be two security policies for one config block.
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

// AdmitRequest is the gateway's other Admits: not which instance may exist, but
// whether a REQUEST may pass at all, and with what ops.
//
// IT IS ON THIS TYPE AND NOT A NEW ONE for the reason AdmitInstance gives about
// itself -- one type answers the admissions this node makes, and two would be
// two places to look for what said no.
//
// IT RUNS ON THE NODE THAT TOOK THE CONNECTION, like every other admission
// here, and unlike a transformer. A transformer is routed to a worker so it
// lands where the project is warm; a guard is instantiated per question and has
// no warmth to find, so routing it would buy a hop and nothing else.
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

// admit is the decision, whoever asked.
//
// WHO IS ASKING IS A PARAMETER AND NOT A SPEC, because one of the two callers
// has no spec: the gateway asks this same validator about a BROWSER. A caller
// the validator can read either way is what keeps that one implementation
// rather than two.
func (a *Admissions) admit(ctx context.Context, who wasmtier.Caller, grant invoke.Grant, instance string) error {
	// AN INSTANCE NAME IS A KEY AND THE STORE PATH-CLEANS ONE. `..` is not a
	// name that misses — it RESOLVES, to `<project>/<environment>`, so admitting
	// it would write one row that every other store in the deployment then reads
	// as its own admission and every instance of every one of them is admitted
	// without a validator ever being asked.
	//
	// The guard is here rather than in each caller because both meet here: the
	// wasm tier refuses a "/" already, and the gateway takes the instance from
	// a URL path where "." and ".." are ordinary segments.
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

// ask runs the validator the target named, as the world the vocabulary names.
//
// IT USED TO BE A HANDLER WEARING A HEADER. `implements: instance-validator`
// named a world nothing built, so a validator was invoked as an ordinary
// function with the instance in `X-Taubyte-Instance` and the answer read off an
// HTTP status. That worked and it cost something real: a function declaring the
// world the VOCABULARY names could not start at all, so the fixture's own
// `may_open` was a control that compiled, published and failed its first cold
// start. The world exists now and this drives it.
//
// A STATUS CODE HAD NO ROOM FOR THE DIFFERENCE THAT MATTERS. "Refused" and "did
// not decide" both had to be non-2xx, and the second is not a refusal — telling
// a caller their instance was turned down when nothing decided sends them to
// argue with a policy that never fired. The verdict variant carries both, so the
// distinction this function already made in Go is now one the validator makes.
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
