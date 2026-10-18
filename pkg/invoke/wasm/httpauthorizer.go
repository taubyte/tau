package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/specs"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// httpauthorizer.go is the host half of `taubyte:authorizer/http-authorizer` —
// the function a route names to decide whether a request may pass, and with
// what.
//
// WHY IT IS NOT THE `authorizer` WORLD. That one is asked `who: caller`, a
// WORKLOAD with a project, an environment, a kind and a scope. The caller here
// is whoever opened a socket to the edge, which has none of those, so asking
// that world about ingress means handing it a caller nobody filled in.
//
// AND WHY IT IS NOT A TRANSFORMER. A transformer shapes traffic and may answer
// instead of forwarding, so a chain CAN refuse — but the refusal is then a side
// effect of a pipeline stage, and what guards a route is discoverable only by
// reading that stage's code. This is the policy: asked about the request,
// answering with the ops the caller gets.

// WorldHTTPAuthorizer is `implements: http-authorizer`.
const WorldHTTPAuthorizer = specs.WorkloadBlockImplementsHttpAuthorizer

// The decision variant's arms, in the order the WIT declares them.
const (
	admissionGranted uint32 = iota
	admissionDenied
)

// ErrNotAnHTTPAuthorizer is a component that answered with a shape this world
// does not have. It is never read as a grant: a guard whose answer cannot be
// understood is a guard that did not answer.
var ErrNotAnHTTPAuthorizer = errors.New("wasm: that component did not answer as an http authorizer")

// AuthorizeRequest instantiates spec as an http authorizer and asks it about
// one request.
//
// AN INSTANCE PER QUESTION, for Authorize's reason: a decision is a pure
// function of the question and whatever it reads, and a warm pool of guards
// would be a second lifecycle to reason about. The set it answers with is what
// stops a cold start landing on every operation the caller then performs.
func (r *Runtime) AuthorizeRequest(ctx context.Context, spec invoke.Spec, req *invoke.Request) (*invoke.Admission, error) {
	instance, runtime, err := r.instantiate(ctx, spec, WorldHTTPAuthorizer)
	if err != nil {
		return nil, err
	}
	defer runtime.Close(ctx)

	// THE SAME `request` EVERY OTHER HTTP WORLD IS HANDED, lowered by the same
	// function — which is what keeps this world's record from drifting from the
	// handler's and the transformer's.
	out, err := instance.Call(ctx, authorizeExport, requestValue(req))
	if err != nil {
		return nil, fmt.Errorf("wasm: asking %s: %w", spec.Name, err)
	}
	if len(out) != 1 {
		return nil, fmt.Errorf("%w: %s returned %d values", ErrNotAnHTTPAuthorizer, spec.Name, len(out))
	}
	return admissionFrom(out[0], spec.Name)
}

// admissionFrom lifts the world's `decision` variant back.
//
// EVERY FIELD IS CHECKED, for decisionFrom's reason: what came back was
// produced by somebody else's component, and a shape that is not this world's
// must read as an error naming the guard rather than as permission.
func admissionFrom(value component.Value, name string) (*invoke.Admission, error) {
	var variant *component.VariantValue
	switch v := value.(type) {
	case *component.VariantValue:
		variant = v
	case component.VariantValue:
		variant = &v
	default:
		return nil, fmt.Errorf("%w: %s answered with a %T rather than a decision", ErrNotAnHTTPAuthorizer, name, value)
	}

	switch variant.Disc {
	case admissionDenied:
		why, ok := variant.Payload.(string)
		if !ok {
			return nil, fmt.Errorf("%w: %s denied with a %T rather than a reason",
				ErrNotAnHTTPAuthorizer, name, variant.Payload)
		}
		return &invoke.Admission{Denied: why}, nil

	case admissionGranted:
		// A GRANT OF NOTHING IS STILL A GRANT, and it is the commonest refusal
		// this world has. An empty list arrives as a nil payload rather than an
		// empty slice, and reading that as malformed would turn "you get
		// nothing" into an author's bug — decisionFrom says the same.
		if variant.Payload == nil {
			return &invoke.Admission{Granted: []string{}}, nil
		}
		list, ok := variant.Payload.([]component.Value)
		if !ok {
			return nil, fmt.Errorf("%w: %s granted a %T rather than a list",
				ErrNotAnHTTPAuthorizer, name, variant.Payload)
		}
		ops := make([]string, 0, len(list))
		for _, entry := range list {
			op, ok := entry.(string)
			if !ok {
				return nil, fmt.Errorf("%w: %s granted an op that is not a string",
					ErrNotAnHTTPAuthorizer, name)
			}
			ops = append(ops, op)
		}
		return &invoke.Admission{Granted: ops}, nil
	}

	return nil, fmt.Errorf("%w: %s answered with arm %d", ErrNotAnHTTPAuthorizer, name, variant.Disc)
}
