package wasm

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// transformer.go is the host half of `taubyte:gateway-transformer` — the
// functions a gateway or a proxy declares that traffic passes through.
//
// # It is pooled, and that is what makes it different from the other two
//
// An authorizer and an instance validator are asked once per caller and their
// answers are kept, so each instantiates per question and the cold start
// amortises away. A transformer runs on EVERY request and nothing about its
// answer can be kept — it is a function of the request. Instantiating a small
// component measures at ~13ms on this tree, which is not something to add to
// every request at the edge, so a transformer is an ordinary pooled instance
// and Start brings it up: see invoke.Pool, none of whose warm-slot,
// cold-start-deduplication or scale-to-zero logic is about what gets called.

// WorldGatewayTransformer is `implements: gateway-transformer`.
const WorldGatewayTransformer = "gateway-transformer"

// transformExport is fixed: a gateway names the function, not an export inside
// it.
const transformExport = "transform"

// The outcome variant's arms, in the order the WIT declares them.
const (
	outcomeForward uint32 = iota
	outcomeAnswer
)

// ErrNotATransformer is a component that answered with a shape this world does
// not have. A transformer whose answer cannot be read must not be treated as
// "carry on" — that would be a filter silently not filtering, which is the
// failure `fail: closed` exists to prevent.
var ErrNotATransformer = errors.New("wasm: that component did not answer as a transformer")

// Transform runs one request through this instance.
func (f *function) Transform(ctx context.Context, req *invoke.Request) (*invoke.Outcome, error) {
	if f.world != WorldGatewayTransformer {
		return nil, fmt.Errorf("%w: this is a %s and transforms nothing", ErrUnsupportedWorld, f.world)
	}

	out, err := f.instance.Call(ctx, f.export, requestValue(req))
	if err != nil {
		return nil, fmt.Errorf("wasm: calling %s: %w", f.export, err)
	}
	if len(out) != 1 {
		return nil, fmt.Errorf("%w: %s returned %d values", ErrNotATransformer, f.export, len(out))
	}
	return outcomeFrom(out[0], f.export)
}

// outcomeFrom lifts the world's `outcome` variant back.
//
// EVERY FIELD IS CHECKED for the reason responseFrom gives, and the stakes are
// higher here: this decides whether a request carries on. A shape nobody
// understands is an error, which the gateway then treats as the transformer
// having failed — and what that means is the author's to say, per entry, with
// `fail: closed` or `fail: open`.
func outcomeFrom(value component.Value, export string) (*invoke.Outcome, error) {
	var variant *component.VariantValue
	switch v := value.(type) {
	case *component.VariantValue:
		variant = v
	case component.VariantValue:
		variant = &v
	default:
		return nil, fmt.Errorf("%w: %s answered with no outcome", ErrNotATransformer, export)
	}

	switch variant.Disc {
	case outcomeForward:
		req, err := requestFrom(variant.Payload, export)
		if err != nil {
			return nil, err
		}
		return &invoke.Outcome{Forward: req}, nil

	case outcomeAnswer:
		res, err := responseFrom(variant.Payload, export)
		if err != nil {
			return nil, err
		}
		return &invoke.Outcome{Answer: res}, nil
	}

	return nil, fmt.Errorf("%w: %s answered with arm %d", ErrNotATransformer, export, variant.Disc)
}

// requestFrom lifts the world's `request` record — the reverse of requestValue,
// and the only place this tier reads one back out of a guest.
func requestFrom(value component.Value, export string) (*invoke.Request, error) {
	fields, ok := value.([]component.Value)
	if !ok || len(fields) != 4 {
		return nil, fmt.Errorf("%w: %s forwarded something that is not a request", ErrNotATransformer, export)
	}

	method, methodOK := fields[0].(string)
	path, pathOK := fields[1].(string)
	if !methodOK || !pathOK {
		return nil, fmt.Errorf("%w: %s forwarded a method or path that is not a string", ErrNotATransformer, export)
	}

	headers, err := headersFrom(fields[2], export)
	if err != nil {
		return nil, err
	}

	// CANONICALISED, because a header name is case-insensitive and this map is
	// not. Everything upstream fills it from net/http, which canonicalises —
	// `X-Card` — and a guest writes whatever it writes. A transformer adding
	// `x-shaped` would otherwise produce a request whose next reader misses it
	// looking for `X-Shaped`, and the miss is silent: the header is right there,
	// under a key nobody asks for.
	canonical := make(map[string][]string, len(headers))
	for name, values := range headers {
		key := http.CanonicalHeaderKey(name)
		canonical[key] = append(canonical[key], values...)
	}

	body, ok := fields[3].([]byte)
	if !ok && fields[3] != nil {
		return nil, fmt.Errorf("%w: %s forwarded a body that is not bytes", ErrNotATransformer, export)
	}

	return &invoke.Request{Method: method, Path: path, Headers: canonical, Body: body}, nil
}
