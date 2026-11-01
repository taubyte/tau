package proxy

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/pkg/splice"
)

// The chain runs in order, as a gateway's: each transformer sees what the previous
// one produced. Every transformer is called even with an empty chunk, since it may
// hold bytes of its own to release.
//
// What a failure means is the author's, per entry (`fail`). A transformer that
// closes ran and decided, which ends the connection whatever `fail` says.
//
// Each transformer's state returns with its answer and is handed in with the next
// chunk, so two chunks may be shaped on two nodes.

// ErrClosedByTransformer is a transformer that decided the conversation stops.
var ErrClosedByTransformer = errors.New("proxy: a transformer closed the connection")

// chain builds the shaper for one direction, or nil when nothing shapes it.
func (s *Service) chain(ctx context.Context, r specs.Resource, b Bind, scope []string, conn *invoke.Connection, way invoke.Way) splice.Shaper {
	entries := specs.ReadProxy(r).Transformers
	if len(entries) == 0 {
		return nil
	}

	type link struct {
		at   specs.Address
		open bool
	}

	links := make([]link, 0, len(entries))
	for _, t := range entries {
		name := t.Name
		if name == "" {
			continue
		}
		links = append(links, link{
			at: specs.Address{
				Project:     b.Project,
				Environment: b.Environment,
				Scope:       specs.ScopeOf(scope, t.Namespace),
				Kind:        specs.KindFunction,
				Name:        name,
			},
			// `closed` is the vocabulary's default, so anything that is not
			// the word `open` is closed.
			open: t.Fail == specs.TransformersEntryFailOpen,
		})
	}
	if len(links) == 0 {
		return nil
	}

	// One state per transformer per direction, held here for the connection's
	// life and handed back to whichever instance takes the next chunk.
	states := make([][]byte, len(links))

	return func(chunk []byte, last bool) ([]byte, error) {
		for i, l := range links {
			out, err := s.opts.workers.Shape(ctx, l.at, &invoke.Stream{
				Conn:  conn,
				Way:   way,
				State: states[i],
				Chunk: chunk,
				Last:  last,
			})
			if err != nil {
				if l.open {
					// The author said the connection is better carried without
					// it. The chunk goes on as this link received it.
					continue
				}
				return nil, fmt.Errorf("proxy: %s could not be run and is fail-closed: %w", l.at.Name, err)
			}
			if out.Pass == nil {
				return nil, fmt.Errorf("%w: %s: %s", ErrClosedByTransformer, l.at.Name, out.Refused)
			}

			states[i] = out.Pass.State
			chunk = out.Pass.Out
		}
		return chunk, nil
	}
}
