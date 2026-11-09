package hosted

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"connectrpc.com/connect"

	"github.com/taubyte/tau/services/plm/jobs"
	"github.com/taubyte/tau/services/worker"
)

// What a worker runs is read from plm's job records, this node's replica of them.

// builtRecords is the database those records are kept in.
var builtRecords = jobs.Records

// ErrNotBuilt is a resource the platform builds and nothing has built yet.
//
// IT IS NOT THE PLM SERVICE'S NotFound, deliberately: what a gateway tells a
// caller follows the error's code, and a deployment missing its artifact is a
// broken site (502), not a name nobody serves (404).
var ErrNotBuilt = errors.New("worker: nothing has built this resource yet")

// Artifacts is where this worker looks a built resource's artifact up: what whoever
// composed its node gave it, else plm's records on this node.
func (n *Service) Artifacts() worker.Artifacts {
	if n.lookup != nil {
		return n.lookup
	}
	return n.PLMArtifacts()
}

// artifacts is what a built resource currently runs. It drops the kind, since a
// resource id is unique. The answer is cached for the register pointer's TTL, how
// long a node may run the previous artifact after a build.
// artifacts' seen is a built resource's builtAt, read on every request without a
// lock.
type artifacts struct {
	node *Service
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
func (n *Service) PLMArtifacts() worker.Artifacts {
	return n.built
}

func (a *artifacts) Artifact(ctx context.Context, project, environment, kind, resource string) (string, error) {
	key := builtKey{project: project, environment: environment, resource: resource}

	if cid, ok := a.cached(key); ok {
		return cid, nil
	}
	records, err := a.node.dbs.Database(builtRecords)
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
	a.seen.Store(key, builtAt{cid: cid, when: a.node.host.Now()})
}

func (a *artifacts) cached(key builtKey) (string, bool) {
	held, ok := a.seen.Load(key)
	if !ok {
		return "", false
	}
	at := held.(builtAt)
	if a.node.host.Now().Sub(at.when) >= a.node.cfg.Pointer() {
		return "", false
	}
	return at.cid, true
}
