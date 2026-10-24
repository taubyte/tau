package boot

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/taubyte/tau/node"
	"github.com/taubyte/tau/pkg/kvdb"
	"github.com/taubyte/tau/pkg/register"
)

// retentionPoll is how often a node rereads the retention its databases write
// under from the fleet document. A node that reads a change late is safe:
// every write carries the retention it was written under.
const retentionPoll = time.Minute

var errNoFleetSource = errors.New("boot: this node runs no register to read the fleet from")

// fleetDocument reads the fleet document from the register this node runs,
// once the node is up. A node that runs no register cannot learn it and keeps
// the default, as it does for the replication floor.
func fleetDocument(running *atomic.Pointer[node.Node]) kvdb.FleetSource {
	return func(ctx context.Context) ([]byte, bool, error) {
		n := running.Load()
		if n == nil || n.Register() == nil {
			return nil, false, errNoFleetSource
		}
		reg := n.Register().Local()
		p, err := reg.ResolveFleet(ctx)
		if errors.Is(err, register.ErrNotPublished) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		doc, err := reg.Object(ctx, p.Object)
		return doc, err == nil, err
	}
}
