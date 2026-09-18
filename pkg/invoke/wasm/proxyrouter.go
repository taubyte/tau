package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// proxyrouter.go is the host half of `taubyte:proxy-router` — the function a
// proxy names instead of a fixed forward.
//
// # It is handed bytes, where a gateway router is handed a request
//
// That is the whole difference between the two worlds, and it is why they are
// two. A raw connection carries a peer address and then bytes; everything a
// gateway router reads off a parsed request is this one's to recognise.
//
// # Pooled, because the whole prefix comes back every time
//
// A router that had to remember a connection between calls would have to be
// pinned to it. Handing the accumulated prefix over on each call costs a copy
// and buys an ordinary pooled instance that any warm worker can run — the same
// trade transformer.go and router.go make.

// WorldProxyRouter is `implements: proxy-router`.
const WorldProxyRouter = "proxy-router"

// directExport is fixed: a proxy names the function, not an export inside it.
const directExport = "route"

// The decision variant's arms, in the order the WIT declares them.
const (
	directTo uint32 = iota
	directMore
	directRefuse
)

// ErrNotAProxyRouter is a component that answered with a shape this world does
// not have. It is an error rather than a default because every default is
// wrong: forwarding invents a destination, and closing tells a client the
// service refused it when what happened is that the router is broken.
var ErrNotAProxyRouter = errors.New("wasm: that component did not answer as a proxy router")

// Direct asks this instance where one connection should go.
func (f *function) Direct(ctx context.Context, conn *invoke.Connection, peeked []byte) (*invoke.Direction, error) {
	if f.world != WorldProxyRouter {
		return nil, fmt.Errorf("%w: this is a %s and directs nothing", ErrUnsupportedWorld, f.world)
	}
	if peeked == nil {
		// A nil list is an empty one and not a null: the world says list<u8>,
		// which has no absent case.
		peeked = []byte{}
	}

	out, err := f.instance.Call(ctx, f.export, connectionValue(conn), peeked)
	if err != nil {
		return nil, fmt.Errorf("wasm: calling %s: %w", f.export, err)
	}
	if len(out) != 1 {
		return nil, fmt.Errorf("%w: %s returned %d values", ErrNotAProxyRouter, f.export, len(out))
	}
	return directionFrom(out[0], f.export)
}

// connectionValue lowers the world's `connection` record. It is shared with
// proxytransformer.go, whose world declares the same record for the same
// reason: one function can serve several proxies and has to tell them apart.
func connectionValue(c *invoke.Connection) component.Value {
	if c == nil {
		c = &invoke.Connection{}
	}
	return []component.Value{c.Source, c.Host, c.Protocol, c.Transport, c.ServerName}
}

// directionFrom lifts the world's `decision` variant back.
func directionFrom(value component.Value, export string) (*invoke.Direction, error) {
	var variant *component.VariantValue
	switch v := value.(type) {
	case *component.VariantValue:
		variant = v
	case component.VariantValue:
		variant = &v
	default:
		return nil, fmt.Errorf("%w: %s answered with no decision", ErrNotAProxyRouter, export)
	}

	switch variant.Disc {
	case directTo:
		fields, ok := variant.Payload.([]component.Value)
		if !ok || len(fields) != 4 {
			return nil, fmt.Errorf("%w: %s directed to something that is not a destination", ErrNotAProxyRouter, export)
		}
		kind, kindOK := fields[0].(string)
		namespace, nsOK := fields[1].(string)
		name, nameOK := fields[2].(string)
		port, portOK := fields[3].(string)
		if !kindOK || !nsOK || !nameOK || !portOK {
			return nil, fmt.Errorf("%w: %s directed to a destination that is not four strings", ErrNotAProxyRouter, export)
		}
		if kind == "" || name == "" {
			// A DESTINATION WITH NO KIND OR NO NAME ADDRESSES NOTHING, and
			// resolving it would search for the empty name in every kind.
			return nil, fmt.Errorf("%w: %s directed to %q/%q", ErrNotAProxyRouter, export, kind, name)
		}
		return &invoke.Direction{To: &invoke.Endpoint{Kind: kind, Namespace: namespace, Name: name, Port: port}}, nil

	case directMore:
		want, ok := variant.Payload.(uint32)
		if !ok {
			return nil, fmt.Errorf("%w: %s asked for more bytes without saying how many", ErrNotAProxyRouter, export)
		}
		if want == 0 {
			// A ROUTER THAT WANTS NO MORE THAN IT HAS WILL NEVER DECIDE, and
			// the loop that honoured it would spin on the same prefix forever.
			return nil, fmt.Errorf("%w: %s asked for more bytes and named none", ErrNotAProxyRouter, export)
		}
		return &invoke.Direction{More: want}, nil

	case directRefuse:
		// AN EMPTY REASON IS STILL A DECISION. The arm is what says the
		// connection is refused; the string is for whoever reads the log.
		why, _ := variant.Payload.(string)
		if why == "" {
			why = "the router refused it"
		}
		return &invoke.Direction{Refused: why}, nil
	}

	return nil, fmt.Errorf("%w: %s answered with arm %d", ErrNotAProxyRouter, export, variant.Disc)
}
