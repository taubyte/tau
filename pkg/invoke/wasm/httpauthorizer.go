package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/specs"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// httpauthorizer.go is the host half of `taubyte:authorizer/http-authorizer`, the
// function a route names to decide whether a request passes and with which ops.
// Not the `authorizer` world, which is asked about a workload caller; not a
// transformer, whose refusals are a side effect rather than the policy.

// WorldHTTPAuthorizer is `implements: http-authorizer`.
const WorldHTTPAuthorizer = specs.WorkloadBlockImplementsHttpAuthorizer

// The decision variant's arms, in the order the WIT declares them.
const (
	admissionGranted uint32 = iota
	admissionDenied
)

// ErrNotAnHTTPAuthorizer is a component answering in a shape this world lacks;
// never read as a grant.
var ErrNotAnHTTPAuthorizer = errors.New("wasm: that component did not answer as an http authorizer")

// AuthorizeRequest instantiates spec as an http authorizer and asks about one
// request; an instance per question, as for Authorize.
func (r *Runtime) AuthorizeRequest(ctx context.Context, spec invoke.Spec, req *invoke.Request) (*invoke.Admission, error) {
	instance, runtime, err := r.instantiate(ctx, spec, WorldHTTPAuthorizer)
	if err != nil {
		return nil, err
	}
	defer runtime.Close(ctx)

	// The same `request` record every HTTP world gets, lowered by the same function.
	out, err := call(ctx, instance, authorizeExport, requestValue(req))
	if err != nil {
		return nil, fmt.Errorf("wasm: asking %s: %w", spec.Name, err)
	}
	if len(out) != 1 {
		return nil, fmt.Errorf("%w: %s returned %d values", ErrNotAnHTTPAuthorizer, spec.Name, len(out))
	}
	return admissionFrom(out[0], spec.Name)
}

// admissionFrom lifts the `decision` variant back, checking every field.
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
		// A nil payload is an empty grant, the commonest refusal, not malformed.
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
