package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/specs"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// stream.go is the host half of `taubyte:stream/publisher`, cache.go's shape with
// one call. Only publish: `subscribe` is a delivery the platform makes, so the op
// check refuses it.

const (
	streamInterface = "taubyte:stream/publisher@0.1.0"
	busInterface    = "taubyte:bus/publisher@0.1.0"
)

const (
	opPublish   = "publish"
	opSubscribe = "subscribe"
)

// The error variant's cases, in the order handler.wit declares them.
const (
	errNoSuchStream uint32 = iota
	errStreamDenied
	errStreamUnavailable
)

var (
	// ErrNoStreams is a spec that grants a stream on a tier composed without a
	// backing. Like ErrNoCaches it is a construction fault, refused at Start.
	ErrNoStreams = errors.New("wasm: this resource publishes to a stream and this tier has nothing to publish with")

	// ErrNoBus is the same for a bus.
	ErrNoBus = errors.New("wasm: this resource publishes to a bus and this tier has nothing to publish with")
)

// Streams is what a guest publishes through. The grant travels whole because where
// a message goes is the stream's engine, which the composer reads off its settings.
// message may be the guest's memory and is only valid for the call.
type Streams interface {
	Publish(ctx context.Context, grant invoke.Grant, message []byte) error
}

// WithStreams is what backs `taubyte:stream/publisher`; without it a publishing
// workload is refused at Start.
func WithStreams(s Streams) Option {
	return func(r *Runtime) { r.streams = s }
}

// WithBus is what backs `taubyte:bus/publisher`, the same interface since sending
// is the same act.
func WithBus(s Streams) Option {
	return func(r *Runtime) { r.bus = s }
}

const (
	streamKind = specs.KindStream
	busKind    = specs.KindBus
)

func streamGrantsOf(spec invoke.Spec) map[string]invoke.Grant {
	return publishableGrants(spec, streamKind)
}

func busGrantsOf(spec invoke.Spec) map[string]invoke.Grant {
	return publishableGrants(spec, busKind)
}

func publishableGrants(spec invoke.Spec, kind string) map[string]invoke.Grant {
	var out map[string]invoke.Grant
	for _, g := range spec.Reaches {
		if g.Kind != kind {
			continue
		}
		if out == nil {
			out = map[string]invoke.Grant{}
		}
		out[g.Label] = g
	}
	return out
}

// streamImports registers publish for one instantiation, even with nothing granted,
// since a missing import traps.
func (r *Runtime) streamImports(spec invoke.Spec) []component.Option {
	return r.publishImports(spec, streamInterface, streamGrantsOf(spec), errNoSuchStream, r.streams)
}

// busImports shares the host code under a separate interface, so an ephemeral bus
// is never reached through a stream's promise.
func (r *Runtime) busImports(spec invoke.Spec) []component.Option {
	return r.publishImports(spec, busInterface, busGrantsOf(spec), errNoSuchBus, r.bus)
}

// errNoSuchBus is the bus variant's first case, which happens to be the same
// discriminant as a stream's — the two variants have the same shape.
const errNoSuchBus = errNoSuchStream

func (r *Runtime) publishImports(spec invoke.Spec, iface string, grants map[string]invoke.Grant, missing uint32, to Streams) []component.Option {

	table := component.NewTypeTable()
	failureRef := table.Variant(
		component.VariantCaseSpec{Name: "no-such-stream", Type: component.Prim("string")},
		component.VariantCaseSpec{Name: "denied", Type: component.Prim("string")},
		component.VariantCaseSpec{Name: "unavailable", Type: component.Prim("string")},
	)
	descriptor := table.Func(
		[]component.TypeRef{component.Prim("string"), table.List(component.Prim("u8"))},
		table.Result(component.TypeRef{}, failureRef),
	)

	publish := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("stream.publish: expected 2 arguments, got %d", len(args))
		}
		label, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("stream.publish: first argument is %T, want string", args[0])
		}
		message, err := readBytes(args[1])
		if err != nil {
			return nil, fmt.Errorf("stream.publish: %w", err)
		}

		// A refused grant says why. A bus may hold many instances, so the shared resolver
		// settles both kinds.
		grant, why := r.addressed(ctx, spec, grants, label, opPublish)
		if why != nil {
			if why.missing {
				return errorResult(streamFailure(missing, why.detail)), nil
			}
			if why.unavailable {
				return errorResult(streamFailure(errStreamUnavailable, why.detail)), nil
			}
			return errorResult(streamFailure(errStreamDenied, why.detail)), nil
		}
		if to == nil {
			// Unreachable while Start refuses a granted spec on a tier with no
			// backing; kept because the alternative traps.
			return errorResult(streamFailure(errStreamUnavailable, "this node has nothing to publish with")), nil
		}

		if err := to.Publish(ctx, grant, message); err != nil {
			return errorResult(streamFailure(errStreamUnavailable, err.Error())), nil
		}
		return []component.Value{component.ResultValue{}}, nil
	}

	return []component.Option{
		component.WithImportCustom(iface, opPublish, publish, descriptor, table.Resolver()),
	}
}

func streamFailure(disc uint32, detail string) *component.VariantValue {
	return &component.VariantValue{Disc: disc, Payload: detail}
}
