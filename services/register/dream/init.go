// Package dream runs the register on a peer of an in-process mesh.
//
// The store is a real replicated kvdb, not a map, because the interesting
// question a universe can answer about a register is the one a map cannot:
// whether a project published against one node resolves on another.
package dream

import (
	"context"
	"io"

	"github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/host"

	"github.com/taubyte/tau/p2p/mesh"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/kvdb"
	"github.com/taubyte/tau/services/register"
)

// name is the register's kvdb.
const name = "register"

// rebroadcastSeconds is how often the CRDT under the store re-announces its
// heads. Five is what every other kvdb in this repo uses; the register has no
// reason to differ, and differing would be a number to explain.
const rebroadcastSeconds = 5

var logger = log.Logger("tau.register.dream")

// Start runs a register on a new peer of m, and answers the peer. The service is
// closed with the mesh, before its peer.
func Start(m *mesh.Mesh) (peer.Node, error) {
	node, err := m.Add()
	if err != nil {
		return nil, err
	}
	svc, err := create(m.Context(), node.Peer(), node)
	if err != nil {
		return nil, err
	}
	m.OnClose(svc)
	return node, nil
}

func create(ctx context.Context, h host.Host, node peer.Node) (io.Closer, error) {
	db, err := kvdb.New(node).New(logger, name, rebroadcastSeconds)
	if err != nil {
		return nil, err
	}

	store, err := register.NewKVStore(db)
	if err != nil {
		db.Close()
		return nil, err
	}

	svc, err := register.New(h, register.WithStore(store))
	if err != nil {
		db.Close()
		return nil, err
	}
	if err := svc.Start(ctx); err != nil {
		svc.Close()
		db.Close()
		return nil, err
	}
	return closer{svc: svc, db: db}, nil
}

// closer stops the service, then the database it was reading.
//
// THE DATABASE IS CLOSED HERE BECAUSE IT WAS OPENED HERE. register.Service
// takes a store and never closes it — the store is handed in by whatever
// composed the node and is shared with the services composed beside it — so
// this is the one place that owns this particular one.
type closer struct {
	svc *register.Service
	db  interface{ Close() }
}

func (c closer) Close() error {
	err := c.svc.Close()
	c.db.Close()
	return err
}
