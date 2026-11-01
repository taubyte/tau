package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/specs"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// proxytransformer.go is the host half of `taubyte:proxy-transformer`, called per
// chunk and direction. A buffering transformer's state is a blob it returns and
// the host carries, so instances stay pooled instead of pinned per connection.

// WorldProxyTransformer is `implements: proxy-transformer`.
const WorldProxyTransformer = specs.WorkloadBlockImplementsProxyTransformer

// shapeExport is fixed: a proxy names the function, not an export inside it.
const shapeExport = "transform"

// The verdict variant's arms, in the order the WIT declares them.
const (
	verdictPass uint32 = iota
	verdictClose
)

// ErrNotAProxyTransformer is a component answering in a shape this world lacks;
// never treated as "carry on", which would be a filter silently not filtering.
var ErrNotAProxyTransformer = errors.New("wasm: that component did not answer as a proxy transformer")

// Shape runs one chunk through this instance.
func (f *function) Shape(ctx context.Context, s *invoke.Stream) (*invoke.Shaping, error) {
	if f.world != WorldProxyTransformer {
		return nil, fmt.Errorf("%w: this is a %s and shapes nothing", ErrUnsupportedWorld, f.world)
	}

	out, err := f.instance.Call(ctx, f.export, streamValue(s))
	if err != nil {
		return nil, fmt.Errorf("wasm: calling %s: %w", f.export, err)
	}
	if len(out) != 1 {
		return nil, fmt.Errorf("%w: %s returned %d values", ErrNotAProxyTransformer, f.export, len(out))
	}
	return shapingFrom(out[0], f.export)
}

// streamValue lowers the world's `stream` record.
func streamValue(s *invoke.Stream) component.Value {
	if s == nil {
		s = &invoke.Stream{}
	}

	// A nil list is empty: list<u8> has no absent case.
	state, chunk := s.State, s.Chunk
	if state == nil {
		state = []byte{}
	}
	if chunk == nil {
		chunk = []byte{}
	}

	return []component.Value{connectionValue(s.Conn), uint32(s.Way), state, chunk, s.Last}
}

// shapingFrom lifts the world's `verdict` variant back.
func shapingFrom(value component.Value, export string) (*invoke.Shaping, error) {
	var variant *component.VariantValue
	switch v := value.(type) {
	case *component.VariantValue:
		variant = v
	case component.VariantValue:
		variant = &v
	default:
		return nil, fmt.Errorf("%w: %s answered with no verdict", ErrNotAProxyTransformer, export)
	}

	switch variant.Disc {
	case verdictPass:
		fields, ok := variant.Payload.([]component.Value)
		if !ok || len(fields) != 2 {
			return nil, fmt.Errorf("%w: %s passed something that is not a shaped chunk", ErrNotAProxyTransformer, export)
		}
		out, outOK := bytesOf(fields[0])
		state, stateOK := bytesOf(fields[1])
		if !outOK || !stateOK {
			return nil, fmt.Errorf("%w: %s passed a chunk or a state that is not bytes", ErrNotAProxyTransformer, export)
		}
		return &invoke.Shaping{Pass: &invoke.Shaped{Out: out, State: state}}, nil

	case verdictClose:
		// AN EMPTY REASON IS STILL A DECISION, exactly as it is for a router.
		why, _ := variant.Payload.(string)
		if why == "" {
			why = "the transformer closed it"
		}
		return &invoke.Shaping{Refused: why}, nil
	}

	return nil, fmt.Errorf("%w: %s answered with arm %d", ErrNotAProxyTransformer, export, variant.Disc)
}

// bytesOf reads a `list<u8>` back. An empty list arrives as nil rather than an
// empty slice, which is a shape and not an error.
func bytesOf(v component.Value) ([]byte, bool) {
	switch b := v.(type) {
	case []byte:
		return b, true
	case nil:
		return nil, true
	}
	return nil, false
}
