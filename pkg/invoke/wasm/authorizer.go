package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/specs"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// authorizer.go is the host half of `taubyte:authorizer`, the world a resource's
// `authorizer:` block names. The authorizer is asked about one op and answers with
// the full set the caller gets on the target, so the host may keep it and keep
// decisions off the hot path; where it is kept is the composer's (Authorizers).

// The vocabulary's paths the composer writes onto a guarded grant; the namespace
// arrives resolved.
const (
	settingAuthorizer      = specs.AuthorizerKeyName
	settingAuthorizerScope = specs.AuthorizerKeyNamespace
)

// Authorizers answers what one caller may do to one guarded target: a seam, since
// asking means running a second function as another world, which this tier must
// not do itself.
type Authorizers interface {
	// Ops is every op this caller gets on the grant's target; op is the one attempted.
	// ErrUndecided means the guard ran and declined to answer.
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
const WorldAuthorizer = specs.WorkloadBlockImplementsAuthorizer

// authorizeExport is fixed: a resource names the guarding function, not an export.
const authorizeExport = "authorize"

// The decision variant's arms, in the order the WIT declares them.
const (
	decisionGranted uint32 = iota
	decisionUndecided
)

var (
	// ErrNotAnAuthorizer is a component answering in a shape this world lacks: the
	// author's bug, not a denial.
	ErrNotAnAuthorizer = errors.New("wasm: that component did not answer as an authorizer")

	// ErrNoAuthorizers is a guarded grant on a tier with nothing to ask, refused at
	// Start rather than denying every op later.
	ErrNoAuthorizers = errors.New("wasm: this resource is guarded by an authorizer and this tier has nothing to ask")

	// ErrUndecided is an authorizer that ran and declined to decide, which is not a
	// refusal.
	ErrUndecided = errors.New("wasm: the authorizer did not decide")
)

// Caller is the workload doing the reaching, the same shape
// `taubyte:workload/identity` gives a guest about itself.
type Caller struct {
	Project     string
	Environment string
	Kind        string
	Name        string
	// Scope is the namespace chain from the project root, outermost first.
	Scope []string
}

// Target is what is being reached. Label is what the caller wrote, not the target's
// name or id.
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

// Decision is what came back: Granted is every op the caller gets, empty being a
// decided refusal, unlike Undecided.
type Decision struct {
	Granted   []string
	Undecided string
}

// guardedGrants are a spec's grants whose target names an authorizer; refused
// grants excluded, so a per-target refusal stays per target.
func guardedGrants(spec invoke.Spec) []invoke.Grant {
	var out []invoke.Grant
	for _, g := range spec.Reaches {
		if g.Refused == "" && g.Settings[settingAuthorizer] != "" {
			out = append(out, g)
		}
	}
	return out
}

// Authorize instantiates spec as an authorizer and asks it one question. An
// instance per question: a decision has no state worth keeping, and the returned
// set keeps the cold start off every op.
func (r *Runtime) Authorize(ctx context.Context, spec invoke.Spec, q Question) (Decision, error) {
	instance, runtime, err := r.instantiate(ctx, spec, WorldAuthorizer)
	if err != nil {
		return Decision{}, err
	}
	defer runtime.Close(ctx)

	out, err := call(ctx, instance, authorizeExport, questionValue(q))
	if err != nil {
		return Decision{}, fmt.Errorf("wasm: asking %s: %w", spec.Name, err)
	}
	if len(out) != 1 {
		return Decision{}, fmt.Errorf("%w: %s returned %d values", ErrNotAnAuthorizer, spec.Name, len(out))
	}
	return decisionFrom(out[0], spec.Name)
}

// questionValue lowers a Question into the world's record: fields in WIT order, so
// a reordered WIT must be matched here.
func questionValue(q Question) component.Value {
	scope := make([]component.Value, 0, len(q.Who.Scope))
	for _, name := range q.Who.Scope {
		scope = append(scope, name)
	}

	who := []component.Value{q.Who.Project, q.Who.Environment, q.Who.Kind, q.Who.Name, scope}
	what := []component.Value{q.What.Kind, q.What.Label, q.What.Instance}
	return []component.Value{who, what, q.Op}
}

// decisionFrom lifts the world's `decision` variant back, checking every field: a
// malformed answer must never read as a grant.
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
		// An empty list arrives as a nil payload; it is a grant of nothing, not malformed.
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
