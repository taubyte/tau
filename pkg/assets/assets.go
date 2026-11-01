// Package assets is where a build's output goes: the log it wrote and the artifacts
// it produced, in the node's own content-addressed store. Bytes go in, a content id
// comes back, and any node that can reach a holder reads them by that id.
//
// It does not promise durability beyond the writing node: Put returns once the bytes
// are addressable here, and another node fetches them from this one, so a cid stops
// resolving if its writer is gone and nobody fetched it. Closing that (push to R
// holders, ack from K before Put returns) is the storage service's. Nothing pins
// against garbage collection either; there is no sweep over this blockstore yet.
//
// It takes bytes and no identity: authorization belongs to whoever accepted the push,
// and a cid is an address, not a credential.
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

// New builds a store over a peer. It takes a peer.Node, unlike other v2 services,
// because the narrow thing it needs is the DAG, which only a peer has.
func New(node peer.Node) (*Store, error) {
	if node == nil {
		return nil, ErrNoNode
	}
	return &Store{node: node}, nil
}

// Put stores content and returns the id it is addressed by. It streams to EOF with no
// length known in advance, as a build log has none. The same bytes always yield the
// same id.
func (s *Store) Put(ctx context.Context, content io.Reader) (string, error) {
	if content == nil {
		return "", errors.New("assets: nothing to store")
	}

	// The context is checked before the write, not during it: AddFile takes none and
	// reads to EOF on this goroutine.
	if err := ctx.Err(); err != nil {
		return "", err
	}

	id, err := s.node.AddFile(content)
	if err != nil {
		return "", fmt.Errorf("assets: storing: %w", err)
	}
	return id, nil
}

// Get reads content back by id, fetching a cid this node does not hold from whoever
// does.
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
