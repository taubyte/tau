package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/project"
)

// route.go decides where one connection goes when a function decides it.
//
// # The router sees the RAW bytes, and the gateway's rule is inverted here
//
// A gateway runs its transformers first, because a router that saw the request
// before the chain would decide from a body somebody was about to redact. A
// proxy does the opposite, and the reason is what the two things are: a gateway
// transformer shapes one whole request, and a proxy transformer is a STREAM
// FILTER WITH STATE. Running the filter first would mean starting it before the
// destination is known, and a filter that buffers a frame would starve the
// router of the very bytes it needs to classify the connection.
//
// A proxy router classifies — a TLS ClientHello, a postgres startup packet —
// and nothing a downstream filter does to the payload changes what protocol
// arrived. The transformers still see every byte, prefix included.
//
// # The peek is bounded twice, in bytes and in time
//
// A router asking for more is a client holding this listener open by sending
// nothing, so both ceilings are the proxy's and neither is the router's to
// raise. Past either, the connection is closed.

// DefaultPeek is how many bytes a router may ask to see.
//
// A TLS ClientHello with a long SNI and a full cipher list fits in a couple of
// kilobytes, and a postgres startup packet in a few hundred. 8 KiB leaves room
// for a protocol nobody here has thought of without letting a router ask a
// client to fill this node's memory.
const DefaultPeek = 8 * 1024

// DefaultDecide is how long the whole decision may take, from the first byte
// asked for to the answer.
const DefaultDecide = 5 * time.Second

// ErrUndecided is a router that never got there.
var ErrUndecided = errors.New("proxy: the router did not decide")

// directed asks this proxy's router where the connection goes, reading as much
// of it as the router asks for. It answers with the destination and everything
// it had to read, which the caller replays to the workload.
func (s *Service) directed(ctx context.Context, r project.Resource, b Bind, scope []string, conn *invoke.Connection, from net.Conn) (project.Address, string, []byte, error) {
	at, err := s.routerAt(r, b, scope)
	if err != nil {
		return project.Address{}, "", nil, err
	}

	decide, cancel := context.WithTimeout(ctx, s.cfg.decideTimeout())
	defer cancel()

	// THE READ DEADLINE IS THE SAME BUDGET, set on the socket rather than only
	// on the context: a client that sends nothing blocks in Read, where a
	// context cancels nothing.
	from.SetReadDeadline(time.Now().Add(s.cfg.decideTimeout()))
	defer from.SetReadDeadline(time.Time{})

	limit := s.cfg.peekLimit()
	peeked := make([]byte, 0, limit)
	buf := make([]byte, limit)

	for {
		d, err := s.opts.workers.Direct(decide, at, conn, peeked)
		if err != nil {
			return project.Address{}, "", peeked, fmt.Errorf("proxy: asking %s: %w", at.Name, err)
		}

		switch {
		case d.Refused != "":
			return project.Address{}, "", peeked, fmt.Errorf("%w: %s", ErrUndecided, d.Refused)

		case d.To != nil:
			return project.Address{
				Project:     b.Project,
				Environment: b.Environment,
				Scope:       project.ScopeOf(scope, d.To.Namespace),
				Kind:        d.To.Kind,
				Name:        d.To.Name,
			}, d.To.Port, peeked, nil
		}

		// It wants more. Both ceilings are checked before a byte is read, so a
		// router asking for more than it may have costs one round trip rather
		// than a full buffer.
		want := int(d.More)
		if want > limit {
			return project.Address{}, "", peeked, fmt.Errorf("%w: it asked to see %d bytes and may see %d", ErrUndecided, want, limit)
		}
		if d.Enough(len(peeked)) {
			return project.Address{}, "", peeked, fmt.Errorf("%w: it asked for %d bytes having already seen %d", ErrUndecided, want, len(peeked))
		}

		n, err := from.Read(buf[:want-len(peeked)])
		peeked = append(peeked, buf[:n]...)
		if err != nil {
			if errors.Is(err, io.EOF) {
				// The client is done and the router never decided. One last
				// call with everything there is, so a protocol whose whole
				// greeting is shorter than it guessed still gets an answer.
				return s.lastWord(decide, at, conn, peeked, b, scope)
			}
			return project.Address{}, "", peeked, fmt.Errorf("%w: reading from %s: %w", ErrUndecided, conn.Source, err)
		}
	}
}

// lastWord asks once more with everything that arrived, and takes no for an
// answer.
func (s *Service) lastWord(ctx context.Context, at project.Address, conn *invoke.Connection, peeked []byte, b Bind, scope []string) (project.Address, string, []byte, error) {
	d, err := s.opts.workers.Direct(ctx, at, conn, peeked)
	if err != nil {
		return project.Address{}, "", peeked, fmt.Errorf("proxy: asking %s: %w", at.Name, err)
	}
	if d.To == nil {
		return project.Address{}, "", peeked, fmt.Errorf("%w: the connection ended after %d bytes", ErrUndecided, len(peeked))
	}
	return project.Address{
		Project:     b.Project,
		Environment: b.Environment,
		Scope:       project.ScopeOf(scope, d.To.Namespace),
		Kind:        d.To.Kind,
		Name:        d.To.Name,
	}, d.To.Port, peeked, nil
}

// routerAt is where this proxy's router function lives.
func (s *Service) routerAt(r project.Resource, b Bind, scope []string) (project.Address, error) {
	name := r.String("router", "name")
	if name == "" {
		return project.Address{}, fmt.Errorf("%w: it is router-routed and names no router", ErrUnservable)
	}
	return project.Address{
		Project:     b.Project,
		Environment: b.Environment,
		Scope:       project.ScopeOf(scope, r.String("router", "namespace")),
		Kind:        "function",
		Name:        name,
	}, nil
}
