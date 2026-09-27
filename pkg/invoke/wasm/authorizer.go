package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// authorizer.go is the host half of `taubyte:authorizer` — the world a
// resource's own `authorizer:` block names.
//
// # It is the second world this tier can drive, and the first was the only one
//
// `implements:` names seven and only the handler was built, so a component
// declaring any other was refused at Start rather than called with a handler's
// signature. That refusal is still right and it is still there; what changed is
// that one more world now has an entry point of its own to be refused INTO.
//
// # Asked about one operation, answered with a set
//
// See the WIT. The host is free to keep the set because the contract says it is
// the whole answer for this caller on this target — which is what keeps a
// decision off the hot path of a cache. Where that keeping happens is the
// composer's, not this package's: see the Authorizers seam below.

// The vocabulary's own paths, as the composer writes them onto a guarded
// grant. The namespace arrives RESOLVED — the outward walk an omitted one means
// happens where the compiled project is open, which is the rule servedBy states
// for a channel.
const (
	settingAuthorizer      = "authorizer/name"
	settingAuthorizerScope = "authorizer/namespace"
)

// Authorizers answers what one caller may do to one guarded target.
//
// IT IS A SEAM BECAUSE THIS TIER RUNS ONE WORKLOAD AND CANNOT REACH ANOTHER —
// the argument node/admit makes for the instance validator, and it holds
// harder here: asking means resolving a second function, instantiating it as a
// different world and calling it, none of which a running guest's tier has any
// business doing on its own.
type Authorizers interface {
	// Ops is every op this caller gets on this grant's target. The op being
	// attempted is passed because the WIT asks about one — what comes back is
	// the whole set, which is what keeps a decision off a cache's hot path.
	//
	// ErrUndecided means the guard ran and declined to answer, which is a
	// different thing for a caller to read than a guard that could not be run.
	Ops(ctx context.Context, spec invoke.Spec, grant invoke.Grant, op string) ([]string, error)
}

// WithAuthorizers is what runs a resource's `authorizer:` block.
func WithAuthorizers(a Authorizers) Option {
	return func(r *Runtime) { r.authorizers = a }
}

// contains reports whether ops holds op.
func contains(ops []string, op string) bool {
	for _, have := range ops {
		if have == op {
			return true
		}
	}
	return false
}

// WorldAuthorizer is `implements: authorizer`.
const WorldAuthorizer = "authorizer"

// authorizeExport is fixed, unlike a handler's. A resource names the FUNCTION
// that guards it, not an export inside it, so there is no `execution/call` here
// to carry a second name.
const authorizeExport = "authorize"

// The decision variant's arms, in the order the WIT declares them.
const (
	decisionGranted uint32 = iota
	decisionUndecided
)

var (
	// ErrNotAnAuthorizer is a component that answered with a shape this world
	// does not have. It is separate from a refusal because it is the AUTHOR's
	// bug rather than the caller's, and nobody should be told they were denied
	// because somebody else's component is malformed.
	ErrNotAnAuthorizer = errors.New("wasm: that component did not answer as an authorizer")

	// ErrNoAuthorizers is a spec with a guarded grant on a tier composed with
	// nothing to ask. It is a construction fault rather than a guest's, so it
	// is refused at Start — the same trade every capability's backing makes,
	// and a stronger one here: the alternative to refusing early is denying
	// every operation on that target later, which reads to an author as their
	// own authorizer saying no.
	ErrNoAuthorizers = errors.New("wasm: this resource is guarded by an authorizer and this tier has nothing to ask")

	// ErrUndecided is an authorizer that ran and declined to decide. It is not
	// a refusal: a refusal is a decision, and telling a caller it was turned
	// down when nothing decided sends them to argue with a policy that never
	// fired.
	ErrUndecided = errors.New("wasm: the authorizer did not decide")
)

// Caller is the workload doing the reaching.
//
// IT IS THE SAME SHAPE `taubyte:workload/identity` HANDS A GUEST ABOUT ITSELF,
// because "who am I" and "who is calling me" are one question asked from two
// ends and two spellings of it would drift.
type Caller struct {
	Project     string
	Environment string
	Kind        string
	Name        string
	// Scope is the namespace chain from the project root, outermost first.
	Scope []string
}

// Target is what is being reached. Label is what the CALLER wrote in its access
// block — not the target's name and not its id, because an authorizer keyed off
// the id would be deciding about a resource the caller cannot name.
type Target struct {
	Kind     string
	Label    string
	Instance string
}

// Question is one authorization, about one verb.
type Question struct {
	Who  Caller
	What Target
	Op   string
}

// Decision is what came back. Granted is every op this caller gets on this
// target — an empty one is a refusal that was DECIDED, which is why it is not
// the same as Undecided being set.
type Decision struct {
	Granted   []string
	Undecided string
}

// guardedGrants are the grants of a spec whose target names an authorizer. A
// REFUSED grant is not among them, for the reason servableStorage states: a
// per-target refusal must not become a per-workload one.
func guardedGrants(spec invoke.Spec) []invoke.Grant {
	var out []invoke.Grant
	for _, g := range spec.Reaches {
		if g.Refused == "" && g.Settings[settingAuthorizer] != "" {
			out = append(out, g)
		}
	}
	return out
}

// Authorize instantiates spec as an authorizer and asks it one question.
//
// AN INSTANCE PER QUESTION, AND THAT IS WHY THE ANSWER IS A SET. A decision has
// no state worth keeping between calls — it is a pure function of the question
// and whatever it reads — while a warm pool of authorizers would be a second
// lifecycle to reason about for something asked once per caller per resource.
// The cost is a cold start, and the contract's set is what stops that landing
// on every operation.
func (r *Runtime) Authorize(ctx context.Context, spec invoke.Spec, q Question) (Decision, error) {
	instance, runtime, err := r.instantiate(ctx, spec, WorldAuthorizer)
	if err != nil {
		return Decision{}, err
	}
	defer runtime.Close(ctx)

	out, err := instance.Call(ctx, authorizeExport, questionValue(q))
	if err != nil {
		return Decision{}, fmt.Errorf("wasm: asking %s: %w", spec.Name, err)
	}
	if len(out) != 1 {
		return Decision{}, fmt.Errorf("%w: %s returned %d values", ErrNotAnAuthorizer, spec.Name, len(out))
	}
	return decisionFrom(out[0], spec.Name)
}

// questionValue lowers a Question into the world's `question` record.
//
// A RECORD IS ITS FIELDS IN ORDER — the same thing requestValue says about
// handler.wit, and the same hazard: reordering the record in the WIT without
// reordering it here transposes two strings silently.
func questionValue(q Question) component.Value {
	scope := make([]component.Value, 0, len(q.Who.Scope))
	for _, name := range q.Who.Scope {
		scope = append(scope, name)
	}

	who := []component.Value{q.Who.Project, q.Who.Environment, q.Who.Kind, q.Who.Name, scope}
	what := []component.Value{q.What.Kind, q.What.Label, q.What.Instance}
	return []component.Value{who, what, q.Op}
}

// decisionFrom lifts the world's `decision` variant back.
//
// EVERY FIELD IS CHECKED, for the reason responseFrom gives: what came back was
// produced by somebody else's component, and a shape that is not this world's is
// an error naming the authorizer rather than a panic three frames later. An
// authorizer that answers nonsense must not read as a grant.
func decisionFrom(value component.Value, name string) (Decision, error) {
	var variant *component.VariantValue
	switch v := value.(type) {
	case *component.VariantValue:
		variant = v
	case component.VariantValue:
		variant = &v
	default:
		return Decision{}, fmt.Errorf("%w: %s answered with no decision", ErrNotAnAuthorizer, name)
	}

	switch variant.Disc {
	case decisionUndecided:
		why, ok := variant.Payload.(string)
		if !ok {
			return Decision{}, fmt.Errorf("%w: %s did not decide and said why in a %T",
				ErrNotAnAuthorizer, name, variant.Payload)
		}
		return Decision{Undecided: why}, nil

	case decisionGranted:
		// A GRANT OF NOTHING IS STILL A GRANT. An empty list arrives as a nil
		// payload rather than an empty slice, and reading that as "malformed"
		// would turn the commonest refusal into an author's bug.
		if variant.Payload == nil {
			return Decision{Granted: []string{}}, nil
		}
		list, ok := variant.Payload.([]component.Value)
		if !ok {
			return Decision{}, fmt.Errorf("%w: %s granted a %T rather than a list",
				ErrNotAnAuthorizer, name, variant.Payload)
		}
		ops := make([]string, 0, len(list))
		for _, entry := range list {
			op, ok := entry.(string)
			if !ok {
				return Decision{}, fmt.Errorf("%w: %s granted an op that is not a string",
					ErrNotAnAuthorizer, name)
			}
			ops = append(ops, op)
		}
		return Decision{Granted: ops}, nil
	}

	return Decision{}, fmt.Errorf("%w: %s answered with arm %d", ErrNotAnAuthorizer, name, variant.Disc)
}
