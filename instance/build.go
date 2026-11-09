package instance

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/services/plm/hosted"
	"github.com/taubyte/tau/services/plm/jobs"
	"github.com/taubyte/tau/services/worker"
)

// Composes auth: who is admitted to this cloud, once a forge said who a caller is.
// The plm service runs from the catalog (services/plm/hosted), and the builder from
// services/builder; what a worker runs is read from its job records, this node's replica of them.

// ErrNotBuilt is a resource the platform builds and nothing has built yet.
//
// IT IS NOT THE PLM SERVICE'S NotFound, deliberately: what a gateway tells a
// caller follows the error's code, and a deployment missing its artifact is a
// broken site (502), not a name nobody serves (404).
var ErrNotBuilt = errors.New("node: nothing has built this resource yet")

// PLMAddr is where this node's plm listens, nil when it runs
// none or it has not started.
func (n *Instance) PLMAddr() net.Addr {
	if f, ok := service.Get[hosted.Service](&added{node: n}); ok {
		return f.Addr()
	}
	return nil
}

// artifacts is what a built resource currently runs. It drops the kind, since a
// resource id is unique. The answer is cached for the register pointer's TTL, how
// long a node may run the previous artifact after a build.
// artifacts' seen is a built resource's builtAt, read on every request without a
// lock.
type artifacts struct {
	node *Instance
	seen sync.Map
}

// builtKey is a resource within one deployment.
type builtKey struct {
	project, environment, resource string
}

type builtAt struct {
	cid  string
	when time.Time
}

// PLMArtifacts is what this node's worker reads a built resource's artifact
// from: plm's job records, this node's replica of them, which it shares with any
// plm service running here. No plm node is asked.
func (n *Instance) PLMArtifacts() worker.Artifacts {
	return &artifacts{node: n}
}

func (a *artifacts) Artifact(ctx context.Context, project, environment, kind, resource string) (string, error) {
	key := builtKey{project: project, environment: environment, resource: resource}

	if cid, ok := a.cached(key); ok {
		return cid, nil
	}
	records, err := a.node.dbs.at(jobs.Records)
	if err != nil {
		return "", err
	}
	cid, err := jobs.Live(ctx, records, project, environment, resource)
	if err != nil {
		// A FAILED LOOKUP IS NOT CACHED. "Nothing has built this yet" is a
		// state that ends the moment a build's records reach this node, and
		// remembering it would keep a node refusing a resource for a TTL after it
		// became runnable.
		return "", notBuilt(err, project, environment, resource)
	}
	a.remember(key, cid)
	return cid, nil
}

// notBuilt is the plm service's NotFound as ErrNotBuilt, and anything else
// as it came.
func notBuilt(err error, project, environment, resource string) error {
	if connect.CodeOf(err) == connect.CodeNotFound {
		return fmt.Errorf("%w: %s/%s's %s", ErrNotBuilt, project, environment, resource)
	}
	return err
}

func (a *artifacts) remember(key builtKey, cid string) {
	a.seen.Store(key, builtAt{cid: cid, when: a.node.clock()})
}

func (a *artifacts) cached(key builtKey) (string, bool) {
	held, ok := a.seen.Load(key)
	if !ok {
		return "", false
	}
	at := held.(builtAt)
	if a.node.clock().Sub(at.when) >= a.node.cfg.Worker.Pointer() {
		return "", false
	}
	return at.cid, true
}

// clock is this node's, so a test that moves time moves everything on it.
func (n *Instance) clock() time.Time {
	if n.opts.now != nil {
		return n.opts.now()
	}
	return time.Now()
}
