// Package assets is where a build's output goes: the log it wrote and the
// artifacts it produced.
//
// # What it is
//
// The node's own content-addressed store. Bytes go in, a content id comes back,
// and any node that can reach a holder can read them by that id. It is the
// mechanism v1 used and it is correct as far as it goes.
//
// # What it does NOT promise, said here rather than discovered
//
// DURABILITY ON A NODE OTHER THAN THE ONE THAT WROTE IT. A Put returns when the
// bytes are addressable from THIS node. Another node reads them by fetching
// them from this one, so a cid handed out in a replicated record — which any
// node may answer a read for — stops resolving if the node that wrote it is
// gone and nothing else has fetched it since.
//
// That is a real gap and it is the storage service's to close (see
// services/storage: the key grammar, placement and reclamation are written, the
// byte plane is not). What closes it is a push to R holders and an
// acknowledgement from K of them before Put returns — v1's pushStash with the
// wait v1 never did. When that lands, this package is the seam it replaces.
//
// PINNING AGAINST GARBAGE COLLECTION. Nothing here holds a reference the
// node's own sweep would honour, because there is no sweep over this
// blockstore yet.
//
// # It is handed bytes and nothing else
//
// No job id, no project, no caller. An asset store that took an identity would
// be a second place authorization could be decided, and authorization belongs
// to whoever accepted the push. A cid is an address, not a credential.
package assets

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/taubyte/tau/p2p/peer"
)

// ErrNoNode is a store with nothing under it.
var ErrNoNode = errors.New("assets: no node to store on")

// Store is the node's content-addressed store.
type Store struct {
	node peer.Node
}

// New builds a store over a peer.
//
// IT TAKES A peer.Node RATHER THAN A host.Host, which is the one place in v2
// that does — every service takes the narrow thing it needs, and the narrow
// thing here IS the DAG, which only a peer has. So this is constructed by
// whoever has the machine's resources and injected as an interface, exactly
// like the kvdb factory beside it.
func New(node peer.Node) (*Store, error) {
	if node == nil {
		return nil, ErrNoNode
	}
	return &Store{node: node}, nil
}

// Put stores content and returns the id it is addressed by.
//
// IT READS TO EOF AND STREAMS, with no length known in advance, because a build
// log has no size until it ends and requiring one would mean holding the whole
// of it in memory to find out.
//
// THE SAME BYTES ALWAYS YIELD THE SAME ID, which is what makes storing an
// artifact during a run that later fails harmless, and a retry of an identical
// build free.
func (s *Store) Put(ctx context.Context, content io.Reader) (string, error) {
	if content == nil {
		return "", errors.New("assets: nothing to store")
	}

	// THE CONTEXT IS CHECKED BEFORE THE WRITE AND NOT DURING IT. AddFile does
	// not take one — it reads to EOF on the calling goroutine — so what this
	// can honour is a deadline that had already passed when it was called. A
	// Put that must abandon a half-written DAG mid-stream is the storage
	// service's problem to solve properly; pretending to solve it here with a
	// goroutine and an abandoned reader would leak the reader and the blocks.
	if err := ctx.Err(); err != nil {
		return "", err
	}

	id, err := s.node.AddFile(content)
	if err != nil {
		return "", fmt.Errorf("assets: storing: %w", err)
	}
	return id, nil
}

// Get reads content back by id.
//
// A CID THIS NODE DOES NOT HOLD IS FETCHED FROM WHOEVER DOES, which is the
// whole reason a cid is worth putting in a replicated record — and the whole
// reason the durability gap above matters, since "whoever does" can be nobody.
func (s *Store) Get(ctx context.Context, id string) (io.ReadSeekCloser, error) {
	if id == "" {
		return nil, errors.New("assets: no content id")
	}

	r, err := s.node.GetFile(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("assets: reading %s: %w", id, err)
	}
	return r, nil
}
