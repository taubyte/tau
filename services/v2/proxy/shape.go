package proxy

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/project"
	"github.com/taubyte/tau/pkg/splice"
)

// shape.go runs a proxy's transformers over the bytes crossing it.
//
// # A chain, in order, exactly as a gateway's
//
// What the second transformer sees is what the first produced, and what the
// workload sees is what the last one did. A chain that handed each transformer
// the ORIGINAL chunk would be a list of independent opinions, none of which
// could build on another.
//
// # Every transformer is called, even with nothing
//
// A transformer that buffers passes an empty chunk on, and the next one in the
// chain is still asked about it: it may be holding bytes of its own with a
// reason to release them, and a chain that skipped it would make "did anything
// arrive" decide whether the rest of the chain runs.
//
// # What happens when one fails is the author's, per entry
//
// It is the one decision here that is dangerous in both directions — a filter
// that fails open stops filtering exactly when something is wrong, and an
// enrichment that fails closed cuts the connection to add a header — so `fail`
// is one of two words and neither is guessed.
//
// A transformer that CLOSES is a different thing from one that failed: it ran,
// read the bytes and decided. That ends the connection whatever `fail` says.
//
// # The state is the host's, which is what makes this survivable
//
// Each transformer's state comes back with its answer and is handed in with the
// next chunk, so nothing is pinned to an instance — see the proxy-transformer
// world. Two chunks of one conversation may legitimately be shaped on two
// nodes, which is what lets a transformer outlive the node it started on.

// ErrClosedByTransformer is a transformer that decided the conversation stops.
var ErrClosedByTransformer = errors.New("proxy: a transformer closed the connection")

// chain builds the shaper for one direction, or nil when nothing shapes it.
func (s *Service) chain(ctx context.Context, r project.Resource, b Bind, scope []string, conn *invoke.Connection, way invoke.Way) splice.Shaper {
	entries := r.List("transformers")
	if len(entries) == 0 {
		return nil
	}

	type link struct {
		at   project.Address
		open bool
	}

	links := make([]link, 0, len(entries))
	for _, t := range entries {
		name := t.String("name")
		if name == "" {
			continue
		}
		links = append(links, link{
			at: project.Address{
				Project:     b.Project,
				Environment: b.Environment,
				Scope:       project.ScopeOf(scope, t.String("namespace")),
				Kind:        "function",
				Name:        name,
			},
			// `closed` is the vocabulary's default, so anything that is not
			// the word `open` is closed.
			open: t.String("fail") == "open",
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
