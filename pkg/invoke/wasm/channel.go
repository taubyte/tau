package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// channel.go is the host half of `taubyte:channel/dialer` — the vocabulary's
// `channel` kind, which is what v1 called p2p.
//
// # The name is the vocabulary's and the rename is load-bearing
//
// `p2p` names the TRANSPORT rather than the thing, the way `http` would be a
// poor name for a route. What this is, is a named, ordered conduit between
// exactly two workloads inside one project — the way in that is not an ingress
// — and libp2p is how it is carried. Anyone reading v1's
// `services/substrate/components/p2p/` is reading this.
//
// # One verb, and it is a write
//
// `dial` is the kind's only op and the vocabulary classifies it as a write,
// because what travels is opaque: nothing on this side can look at the bytes
// and decide the call only read something.
//
// # Request and response, and the conduit that is not here yet
//
// `dial` sends a body and answers with one. That is the shape almost every use
// of v1's p2p actually had — ask the sidecar, get the answer — and it needs no
// handle outliving the call.
//
// A GENUINE CONDUIT IS A WIT `resource`, and it is deliberately not declared:
// streaming both ways means an own<channel> over `wasi:io/streams`, and the far
// end has to hold a connection open per caller. Declaring that before anything
// serves it would publish a promise no backing keeps. `dial` is not a stepping
// stone that gets thrown away either — it stays exactly as it reads when a
// `connect` returning a resource lands beside it.

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

// Channels dials the one a grant names.
//
// IT IS A SEAM BECAUSE A CHANNEL IS A LIBP2P PROTOCOL and this package's job is
// the ABI. What a handle means on the wire, how the project's identity is
// prepended to the protocol name and which peer answers are the composer's and
// the transport's.
type Channels interface {
	// Dial sends a body to whatever serves this channel and answers with what
	// came back. It is bounded by ctx, which carries the CALLER's timeout: a
	// channel declares no deadline of its own, and the workload waiting is the
	// one whose budget is being spent.
	Dial(ctx context.Context, grant invoke.Grant, body []byte) ([]byte, error)
}

// WithChannels is what backs `taubyte:channel/dialer`.
func WithChannels(c Channels) Option {
	return func(r *Runtime) { r.channels = c }
}

// channelKind is the vocabulary's own name for the kind.
const channelKind = "channel"

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

// servableChannels is the channel grants this tier would have to serve — the
// refused ones do not count, for the reason servableStorage states: a
// per-target refusal must not become a per-workload one.
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
