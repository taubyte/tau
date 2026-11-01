package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/specs"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// channel.go is the host half of `taubyte:channel/dialer`, the `channel` kind (v1's
// p2p): a named conduit between two workloads in one project, carried by libp2p.
// `dial`, a write since the bytes are opaque, sends a body and returns one. A
// streaming conduit would be a WIT resource over `wasi:io/streams`, not declared
// until something serves it; `dial` stays beside it.

const channelInterface = "taubyte:channel/dialer@0.1.0"

// opDial is the vocabulary's verb.
const opDial = "dial"

// The error variant's cases, in the order the WIT declares them.
const (
	errNoSuchChannel uint32 = iota
	errChannelDenied
	errChannelNoAnswer
	errChannelFailed
	errChannelUnavailable
)

var (
	// ErrNoChannels is a spec that dials a channel on a tier composed without a
	// transport. It is a construction fault rather than a guest's, so it is
	// refused at Start rather than at the first call.
	ErrNoChannels = errors.New("wasm: this resource dials a channel and this tier has nothing to dial with")

	// ErrNoAnswer is a channel nothing is serving: the workload that answers it
	// is not running, or this cloud has no transport for it. It is its own arm
	// because a caller RETRIES it — the name is right and the far end is not
	// there yet, which on a cold deploy is a matter of seconds.
	ErrNoAnswer = errors.New("wasm: nothing is serving that channel")

	// ErrChannelFailed is the far end answering with a failure, as against a
	// failure to reach it. A caller fixes one and retries the other.
	ErrChannelFailed = errors.New("wasm: the channel's far end refused")
)

// Channels dials the channel a grant names; the wire protocol and peer selection
// are the composer's and the transport's.
type Channels interface {
	// Dial sends a body to whatever serves this channel and returns the answer, bounded
	// by the caller's ctx.
	Dial(ctx context.Context, grant invoke.Grant, body []byte) ([]byte, error)
}

// WithChannels is what backs `taubyte:channel/dialer`.
func WithChannels(c Channels) Option {
	return func(r *Runtime) { r.channels = c }
}

// channelKind is the vocabulary's own name for the kind.
const channelKind = specs.KindChannel

// channelGrantsOf is the channel entries of a spec's access block, by label.
func channelGrantsOf(spec invoke.Spec) map[string]invoke.Grant {
	var out map[string]invoke.Grant
	for _, g := range spec.Reaches {
		if g.Kind != channelKind {
			continue
		}
		if out == nil {
			out = map[string]invoke.Grant{}
		}
		out[g.Label] = g
	}
	return out
}

// servableChannels is the channel grants this tier must serve; refused ones don't
// count.
func servableChannels(spec invoke.Spec) map[string]invoke.Grant {
	var out map[string]invoke.Grant
	for label, g := range channelGrantsOf(spec) {
		if g.Refused != "" {
			continue
		}
		if out == nil {
			out = map[string]invoke.Grant{}
		}
		out[label] = g
	}
	return out
}

// channelImports registers the one function for one instantiation.
func (r *Runtime) channelImports(spec invoke.Spec) []component.Option {
	grants := channelGrantsOf(spec)

	tbl := component.NewTypeTable()
	fd := tbl.Func(
		[]component.TypeRef{component.Prim("string"), tbl.List(component.Prim("u8"))},
		tbl.Result(tbl.List(component.Prim("u8")), tbl.Variant(
			component.VariantCaseSpec{Name: "no-such-channel", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "denied", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "no-answer", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "failed", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "unavailable", Type: component.Prim("string")},
		)),
	)

	dial := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("channel.dial: expected 2 arguments, got %d", len(args))
		}
		label, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("channel.dial: label is %T, want a string", args[0])
		}
		body, err := readBytes(args[1])
		if err != nil {
			return nil, fmt.Errorf("channel.dial: %w", err)
		}

		grant, why := r.addressed(ctx, spec, grants, label, opDial)
		if why != nil {
			switch {
			case why.missing:
				return errorResult(channelFailure(errNoSuchChannel, why.detail)), nil
			case why.unavailable:
				return errorResult(channelFailure(errChannelUnavailable, why.detail)), nil
			}
			return errorResult(channelFailure(errChannelDenied, why.detail)), nil
		}
		if r.channels == nil {
			return errorResult(channelFailure(errChannelUnavailable,
				"this node has nothing to dial a channel with")), nil
		}

		answer, err := r.channels.Dial(ctx, grant, body)
		if err != nil {
			return errorResult(channelArm(err)), nil
		}
		return []component.Value{component.ResultValue{Payload: byteList(answer)}}, nil
	}

	return []component.Option{
		component.WithImportCustom(channelInterface, opDial, dial, fd, tbl.Resolver()),
	}
}

// channelArm maps a Go error onto the arm the guest sees.
func channelArm(err error) *component.VariantValue {
	switch {
	case errors.Is(err, ErrNoAnswer):
		return channelFailure(errChannelNoAnswer, err.Error())
	case errors.Is(err, ErrChannelFailed):
		return channelFailure(errChannelFailed, err.Error())
	}
	return channelFailure(errChannelUnavailable, err.Error())
}

func channelFailure(disc uint32, detail string) *component.VariantValue {
	return &component.VariantValue{Disc: disc, Payload: detail}
}
