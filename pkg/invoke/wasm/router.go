package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/specs"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// router.go is the host half of `taubyte:gateway-router`, the function a gateway
// names instead of a static route or a route's handler; pooled like a transformer.

// WorldGatewayRouter is `implements: gateway-router`.
const WorldGatewayRouter = specs.WorkloadBlockImplementsGatewayRouter

// routeExport is fixed: a gateway names the function, not an export inside it.
const routeExport = "route"

// The decision variant's arms, in the order the WIT declares them.
const (
	routeTo uint32 = iota
	routeAnswer
	routeNone
)

// ErrNotARouter is a component answering in a shape this world lacks; an error,
// since any default would invent a destination or a false 404.
var ErrNotARouter = errors.New("wasm: that component did not answer as a router")

// Route asks this instance where one request should go.
func (f *function) Route(ctx context.Context, req *invoke.Request) (*invoke.Routing, error) {
	if f.world != WorldGatewayRouter {
		return nil, fmt.Errorf("%w: this is a %s and routes nothing", ErrUnsupportedWorld, f.world)
	}

	out, err := f.instance.Call(ctx, f.export, requestValue(req))
	if err != nil {
		return nil, fmt.Errorf("wasm: calling %s: %w", f.export, err)
	}
	if len(out) != 1 {
		return nil, fmt.Errorf("%w: %s returned %d values", ErrNotARouter, f.export, len(out))
	}
	return routingFrom(out[0], f.export)
}

// routingFrom lifts the world's `decision` variant back.
func routingFrom(value component.Value, export string) (*invoke.Routing, error) {
	var variant *component.VariantValue
	switch v := value.(type) {
	case *component.VariantValue:
		variant = v
	case component.VariantValue:
		variant = &v
	default:
		return nil, fmt.Errorf("%w: %s answered with no decision", ErrNotARouter, export)
	}

	switch variant.Disc {
	case routeTo:
		fields, ok := variant.Payload.([]component.Value)
		if !ok || len(fields) != 3 {
			return nil, fmt.Errorf("%w: %s routed to something that is not a destination", ErrNotARouter, export)
		}
		kind, kindOK := fields[0].(string)
		namespace, nsOK := fields[1].(string)
		name, nameOK := fields[2].(string)
		if !kindOK || !nsOK || !nameOK {
			return nil, fmt.Errorf("%w: %s routed to a destination that is not three strings", ErrNotARouter, export)
		}
		if kind == "" || name == "" {
			// A DESTINATION WITH NO KIND OR NO NAME ADDRESSES NOTHING, and
			// resolving it would search for the empty name in every kind.
			return nil, fmt.Errorf("%w: %s routed to %q/%q", ErrNotARouter, export, kind, name)
		}
		return &invoke.Routing{To: &invoke.Destination{Kind: kind, Namespace: namespace, Name: name}}, nil

	case routeAnswer:
		res, err := responseFrom(variant.Payload, export)
		if err != nil {
			return nil, err
		}
		return &invoke.Routing{Answer: res}, nil

	case routeNone:
		// AN EMPTY REASON IS STILL A DECISION. The arm is what says nothing
		// handles this; the string is for whoever reads the log.
		why, _ := variant.Payload.(string)
		if why == "" {
			why = "the router matched nothing"
		}
		return &invoke.Routing{None: why}, nil
	}

	return nil, fmt.Errorf("%w: %s answered with arm %d", ErrNotARouter, export, variant.Disc)
}
