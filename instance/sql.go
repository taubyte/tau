package instance

import (
	"context"
	"fmt"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/database/engines/sql/distributed"
	"github.com/taubyte/tau/pkg/database/engines/sql/distributed/swarm"
)

// registerSQL makes tau's own `sql` engine one this node can open: a musql
// database per declared database, under the node's root, its copies meeting
// over this node's host and pubsub (pkg/database/engines/sql/distributed/swarm).
//
// IT JOINS THE COMPOSER'S REGISTRY rather than standing beside it, because
// that is where a workload's `sql` access is answered from (instance/database's
// engines). A composer that registered a `distributed` sql engine of its own
// keeps it.
//
// A NODE WITHOUT A ROOT, A HOST OR A PUBSUB REGISTERS NONE, and a `sql:
// distributed` database then answers "no engine" by name: a sql database kept
// in a temporary directory loses every row with the process, and one with no
// router can reach no copy but its own.
func (n *Instance) registerSQL() error {
	if n.cfg.Root == "" || n.Host() == nil || n.Messaging() == nil {
		return nil
	}
	if n.opts.engines == nil {
		n.opts.engines = database.NewRegistry()
	}
	for _, e := range n.opts.engines.EnginesFor(database.SQL) {
		if e == distributed.Name {
			return nil
		}
	}

	mesh, err := swarm.New(context.Background(), n.Host(), n.Messaging())
	if err != nil {
		return fmt.Errorf("node: sql: %w", err)
	}
	n.closers = append(n.closers, mesh)

	engine, err := distributed.New(distributed.UnderRoot(n.cfg.Root), distributed.WithSync(mesh.Sync))
	if err != nil {
		return fmt.Errorf("node: sql: %w", err)
	}
	if err := n.opts.engines.Register(context.Background(), engine); err != nil {
		return fmt.Errorf("node: sql: %w", err)
	}
	return nil
}
