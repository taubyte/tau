// Package dream runs the register on a peer of an in-process mesh, over a real
// replicated kvdb, so a universe can show a project published on one node resolving
// on another.
package dream

import (
	"context"
	"io"
	"time"

	"github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/host"

	"github.com/taubyte/tau/p2p/mesh"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/kvdb"
	"github.com/taubyte/tau/services/register"
)

// name is the register's kvdb.
const name = "register"

// rebroadcastSeconds matches every other kvdb in this repo.
const rebroadcastSeconds = 5

var logger = log.Logger("tau.register.dream")

// Start runs a register on a new peer of m, and answers the peer. The service is
// closed with the mesh, before its peer.
func Start(m *mesh.Mesh) (peer.Node, error) {
	return m.Add(func(node peer.Node) error {
		svc, err := create(m.Context(), node.Host(), node)
		if err != nil {
			return err
		}
		m.OnClose(svc)
		return nil
	})
}

func create(ctx context.Context, h host.Host, node peer.Node) (io.Closer, error) {
	db, err := kvdb.Open(node, name, kvdb.WithLogger(logger), kvdb.WithRebroadcast(rebroadcastSeconds*time.Second))
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

// closer stops the service, then the database it was reading. The database was
// opened here; register.Service never closes the store it is handed.
type closer struct {
	svc *register.Service
	db  interface{ Close() }
}

func (c closer) Close() error {
	err := c.svc.Close()
	c.db.Close()
	return err
}
