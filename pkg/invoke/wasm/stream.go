package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// stream.go is the host half of `taubyte:handler/stream`.
//
// It is cache.go's shape with one call instead of four, and deliberately so: a
// second capability that resolved its label differently, or reported a refusal
// differently, would be a second contract for authors to learn.
//
// # Only publish crosses
//
// `stream` declares `subscribe` as well, and it is a DELIVERY rather than a
// call — the platform invokes a workload when a message arrives. There is no
// host function for it and there should not be one; a resource that asked for
// it is refused by the op check, which says what the platform does not do
// rather than leaving a guest waiting on something that never arrives.

const (
	streamInterface = "taubyte:handler/stream@0.1.0"
	busInterface    = "taubyte:handler/bus@0.1.0"
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

// Streams is what a guest publishes through.
//
// ONE METHOD, TAKING THE HANDLE. What a handle means and which topic it becomes
// is the composer's; this package's job is the ABI and the grant check.
type Streams interface {
	Publish(ctx context.Context, handle string, message []byte) error
}

// WithStreams is what backs `taubyte:handler/stream`. Optional: a tier without
// one runs everything that publishes to nothing, and refuses at Start anything
// that does.
func WithStreams(s Streams) Option {
	return func(r *Runtime) { r.streams = s }
}

// WithBus is what backs `taubyte:handler/bus`. It is the same interface as a
// stream's because sending is the same act; what differs is the kind, and the
// handle carries that.
func WithBus(s Streams) Option {
	return func(r *Runtime) { r.bus = s }
}

const (
	streamKind = "stream"
	busKind    = "bus"
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

// streamImports registers publish for one instantiation. Like the cache's, it
// is registered even when nothing is granted: an unprovided import is a trap
// that takes the instance with it.
func (r *Runtime) streamImports(spec invoke.Spec) []component.Option {
	return r.publishImports(streamInterface, streamGrantsOf(spec), errNoSuchStream, r.streams)
}

// busImports is the same function on a different kind.
//
// TWO INTERFACES AND ONE IMPLEMENTATION, because the kinds genuinely differ in
// what they KEEP rather than in how a workload sends: a stream reaches every
// subscriber and a bus reaches everything attached and keeps nothing. Sharing
// the host code is right; sharing the interface would let an author reach an
// ephemeral bus through a door labelled with a stream's promise.
func (r *Runtime) busImports(spec invoke.Spec) []component.Option {
	return r.publishImports(busInterface, busGrantsOf(spec), errNoSuchBus, r.bus)
}

// errNoSuchBus is the bus variant's first case, which happens to be the same
// discriminant as a stream's — the two variants have the same shape.
const errNoSuchBus = errNoSuchStream

func (r *Runtime) publishImports(iface string, grants map[string]invoke.Grant, missing uint32, to Streams) []component.Option {

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

		grant, ok := grants[label]
		if !ok {
			return errorResult(streamFailure(missing, label)), nil
		}
		// A REFUSED GRANT SAYS WHY — an engine this cloud has no driver for
		// reads as a typo otherwise.
		if grant.Refused != "" {
			return errorResult(streamFailure(errStreamDenied, grant.Refused)), nil
		}
		if !grant.Allows(opPublish) {
			return errorResult(streamFailure(errStreamDenied, label+" cannot publish")), nil
		}
		if to == nil {
			// Unreachable while Start refuses a granted spec on a tier with no
			// backing; kept because the alternative traps.
			return errorResult(streamFailure(errStreamUnavailable, "this node has nothing to publish with")), nil
		}

		if err := to.Publish(ctx, grant.Handle, message); err != nil {
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
