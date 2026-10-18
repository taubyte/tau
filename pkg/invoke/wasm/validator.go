package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/specs"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// validator.go is the host half of `taubyte:instance-validator` — the function
// a store that holds many names to decide who may open one.
//
// # It is the authorizer's sibling and the third world this tier drives
//
// The two ask different questions — "what may this caller do to this store" and
// "may this caller open THIS instance of it" — and the vocabulary names them
// separately, so they are separate worlds with separate exports. What they share
// is the shape of the answer: a decision, a refusal to decide, and nothing in
// between.
//
// # Nothing is kept here
//
// An authorizer's answer is the complete set for a caller and a target, so a
// host may keep it. An admission is about ONE instance and the node remembers
// which instances it admitted rather than what the validator said — see
// node/admit, which had that memory before this world existed.

// WorldValidator is `implements: instance-validator`.
const WorldValidator = specs.WorkloadBlockImplementsInstanceValidator

// admitExport is fixed: a store names the function, not an export within it.
const admitExport = "admit"

// The verdict variant's arms, in the order the WIT declares them.
const (
	verdictAdmitted uint32 = iota
	verdictRefused
	verdictUndecided
)

// ErrNotAValidator is a component that answered with a shape this world does not
// have. It is the author's bug rather than the caller's, and nobody should be
// told their instance was turned down because somebody else's component is
// malformed.
var ErrNotAValidator = errors.New("wasm: that component did not answer as an instance validator")

// Store is what is being opened.
type Store struct {
	Kind  string
	Label string
}

// Opening is one admission question.
type Opening struct {
	Who      Caller
	What     Store
	Instance string
}

// Verdict is what came back. Exactly one of Refused and Undecided is set when
// Admitted is false, and the difference is who the string is for: a refusal is
// the caller's to read, and an undecided is an operator's.
type Verdict struct {
	Admitted  bool
	Refused   string
	Undecided string
}

// Admit instantiates spec as an instance validator and asks it about one
// instance.
//
// AN INSTANCE PER QUESTION, for the reason Authorize gives: a decision has no
// state worth keeping between calls, and a warm pool of validators would be a
// second lifecycle to reason about for something asked once per instance.
func (r *Runtime) Admit(ctx context.Context, spec invoke.Spec, o Opening) (Verdict, error) {
	instance, runtime, err := r.instantiate(ctx, spec, WorldValidator)
	if err != nil {
		return Verdict{}, err
	}
	defer runtime.Close(ctx)

	out, err := instance.Call(ctx, admitExport, openingValue(o))
	if err != nil {
		return Verdict{}, fmt.Errorf("wasm: asking %s: %w", spec.Name, err)
	}
	if len(out) != 1 {
		return Verdict{}, fmt.Errorf("%w: %s returned %d values", ErrNotAValidator, spec.Name, len(out))
	}
	return verdictFrom(out[0], spec.Name)
}

// openingValue lowers an Opening into the world's `opening` record. A record is
// its fields in order — see questionValue, which says why that matters.
func openingValue(o Opening) component.Value {
	scope := make([]component.Value, 0, len(o.Who.Scope))
	for _, name := range o.Who.Scope {
		scope = append(scope, name)
	}

	who := []component.Value{o.Who.Project, o.Who.Environment, o.Who.Kind, o.Who.Name, scope}
	what := []component.Value{o.What.Kind, o.What.Label}
	return []component.Value{who, what, o.Instance}
}

// verdictFrom lifts the world's `verdict` variant back.
//
// A SHAPE NOBODY UNDERSTANDS ADMITS NOTHING. What came back was produced by
// somebody else's component, and a malformed answer read as an admission is the
// failure this control exists to prevent.
func verdictFrom(value component.Value, name string) (Verdict, error) {
	var variant *component.VariantValue
	switch v := value.(type) {
	case *component.VariantValue:
		variant = v
	case component.VariantValue:
		variant = &v
	default:
		return Verdict{}, fmt.Errorf("%w: %s answered with no verdict", ErrNotAValidator, name)
	}

	reason := func() (string, bool) {
		// AN ARM DECLARED WITH A TYPE ALWAYS HAS ONE, but an empty string
		// lowers as a nil payload — which is a legitimate "no reason given"
		// rather than a malformed answer.
		if variant.Payload == nil {
			return "", true
		}
		why, ok := variant.Payload.(string)
		return why, ok
	}

	switch variant.Disc {
	case verdictAdmitted:
		return Verdict{Admitted: true}, nil

	case verdictRefused:
		why, ok := reason()
		if !ok {
			return Verdict{}, fmt.Errorf("%w: %s refused and said why in a %T", ErrNotAValidator, name, variant.Payload)
		}
		return Verdict{Refused: why}, nil

	case verdictUndecided:
		why, ok := reason()
		if !ok {
			return Verdict{}, fmt.Errorf("%w: %s did not decide and said why in a %T", ErrNotAValidator, name, variant.Payload)
		}
		return Verdict{Undecided: why}, nil
	}

	return Verdict{}, fmt.Errorf("%w: %s answered with arm %d", ErrNotAValidator, name, variant.Disc)
}
