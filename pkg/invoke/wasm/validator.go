package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/specs"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// validator.go is the host half of `taubyte:instance-validator`, the function a
// many-instance store names to decide who may open one. The authorizer's sibling
// with its own question and export. Nothing is kept here; instance/admit remembers
// admitted instances.

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

// ErrNotAValidator is a component answering in a shape this world lacks: the
// author's bug, not a refusal.
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

// Verdict is what came back. When not Admitted, exactly one of Refused (for the
// caller) and Undecided (for an operator) is set.
type Verdict struct {
	Admitted  bool
	Refused   string
	Undecided string
}

// Admit instantiates spec as an instance validator and asks about one instance; an
// instance per question, as for Authorize.
func (r *Runtime) Admit(ctx context.Context, spec invoke.Spec, o Opening) (Verdict, error) {
	instance, runtime, err := r.instantiate(ctx, spec, WorldValidator)
	if err != nil {
		return Verdict{}, err
	}
	defer runtime.Close(ctx)

	out, err := call(ctx, instance, admitExport, openingValue(o))
	if err != nil {
		return Verdict{}, fmt.Errorf("wasm: asking %s: %w", spec.Name, err)
	}
	if len(out) != 1 {
		return Verdict{}, fmt.Errorf("%w: %s returned %d values", ErrNotAValidator, spec.Name, len(out))
	}
	return verdictFrom(out[0], spec.Name)
}

// openingValue lowers an Opening into the `opening` record, fields in order.
func openingValue(o Opening) component.Value {
	scope := make([]component.Value, 0, len(o.Who.Scope))
	for _, name := range o.Who.Scope {
		scope = append(scope, name)
	}

	who := []component.Value{o.Who.Project, o.Who.Environment, o.Who.Kind, o.Who.Name, scope}
	what := []component.Value{o.What.Kind, o.What.Label}
	return []component.Value{who, what, o.Instance}
}

// verdictFrom lifts the `verdict` variant back; a malformed answer admits nothing.
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
		// An empty reason lowers as a nil payload: no reason given, not malformed.
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
