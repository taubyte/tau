package hosted

import (
	"context"
	"fmt"

	"github.com/taubyte/tau/pkg/database/engines/sql/distributed"
	"github.com/taubyte/tau/pkg/database/engines/sql/distributed/swarm"
)

// registerSQL makes tau's own `sql` engine one this node can open: a musql
// database per declared database, under the node's root, its copies meeting
// over this node's host and pubsub (pkg/database/engines/sql/distributed/swarm).
//
// IT JOINS THE REGISTRY THE SECTION'S EXTERNAL ENGINES ARE IN, because that is
// where a workload's `sql` access is answered from (services/worker/database's
// engines).
//
// A NODE WITHOUT A ROOT, A HOST OR A PUBSUB REGISTERS NONE, and a `sql:
// distributed` database then answers "no engine" by name: a sql database kept
// in a temporary directory loses every row with the process, and one with no
// router can reach no copy but its own.
func (n *Service) registerSQL() error {
	if n.root == "" || n.host.Peer().Host() == nil || n.host.Peer().Messaging() == nil {
		return nil
	}
	mesh, err := swarm.New(context.Background(), n.host.Peer().Host(), n.host.Peer().Messaging())
	if err != nil {
		return fmt.Errorf("worker: sql: %w", err)
	}
	n.closers = append(n.closers, mesh)

	engine, err := distributed.New(distributed.UnderRoot(n.root), distributed.WithSync(mesh.Sync))
	if err != nil {
		return fmt.Errorf("worker: sql: %w", err)
	}
	if err := n.engines.Register(context.Background(), engine); err != nil {
		return fmt.Errorf("worker: sql: %w", err)
	}
	return nil
}
