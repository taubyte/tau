package instance

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/core/service"
	authService "github.com/taubyte/tau/services/auth"
	"github.com/taubyte/tau/services/build"
	"github.com/taubyte/tau/services/build/clients"
	"github.com/taubyte/tau/services/build/hosted"
	"github.com/taubyte/tau/services/build/jobs"
	"github.com/taubyte/tau/services/worker"
)

// Composes auth: who is admitted to this cloud, once a forge said who a caller is.
// The build service, both its roles, runs from the catalog (services/build/hosted),
// and is asked here only what a worker's artifact lookup needs.

// maxAuthConnections bounds the streams one node keeps to auth. It is small
// because every answer is cached in front of it — ingress caches hook
// secrets (ingress/secrets.go) — and everything else is once per job.
const maxAuthConnections = 4

// pollInterval is how often a waiting lookup looks again. The discovery view is
// gossiped and changes on its own schedule; there is nothing to subscribe to.
const pollInterval = 250 * time.Millisecond

// ErrNoBuild is a resource the platform builds, on a node that can find
// nothing that builds.
var ErrNoBuild = errors.New("node: no build service is reachable")

// ErrNotBuilt is a resource the platform builds and nothing has built yet.
//
// IT IS NOT THE BUILD SERVICE'S NotFound, deliberately: what a gateway tells a
// caller follows the error's code, and a deployment missing its artifact is a
// broken site (502), not a name nobody serves (404).
var ErrNotBuilt = errors.New("node: nothing has built this resource yet")

// fromDiscovery is a discovery.Resolver over the view every other locator on this
// node reads: one answer to "who does this".
type fromDiscovery struct {
	node *Instance
	name string
}

func (f *fromDiscovery) Known() []libp2ppeer.ID {
	// A NODE WITH NO VIEW KNOWS NOTHING, and says so as a nil rather than as an
	// empty list it built: the two read the same to a caller that counts them
	// and not to one that asks whether it looked.
	if f.node.discovery == nil {
		return nil
	}

	// THE SAME ROWS JUDGED BY THE SAME TOLERANCE every reach on this node uses
	// — see [Node.rowsFor]. What differs below is only what a row that does not
	// decode means, which is this resolver's to say and not the view's.
	rows := f.node.rowsFor(f.name)
	out := make([]libp2ppeer.ID, 0, len(rows))
	for _, row := range rows {
		id, err := libp2ppeer.Decode(row.Peer)
		if err != nil {
			// A row whose peer does not decode is one nothing can dial. It is
			// skipped rather than failing the lookup, so one bad row cannot
			// hide the good ones beside it.
			continue
		}
		out = append(out, id)
	}
	return out
}

func (f *fromDiscovery) Any(ctx context.Context) (libp2ppeer.ID, error) {
	peers, err := f.Peers(ctx, 1)
	if err != nil {
		return "", err
	}
	return peers[0], nil
}

func (f *fromDiscovery) Peers(ctx context.Context, want int) ([]libp2ppeer.ID, error) {
	for {
		if known := f.Known(); len(known) > 0 {
			if want > 0 && len(known) > want {
				known = known[:want]
			}
			return known, nil
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("node: no %s is reachable: %w", f.name, ctx.Err())
		case <-time.After(pollInterval):
		}
	}
}

// Failed does nothing: a failed dial is not evidence the node stopped running the
// service, and the view already ages out rows whose node stopped reporting.
func (f *fromDiscovery) Failed(libp2ppeer.ID) {}

// dial opens a pool onto whichever node runs `service`, on `p`.
func (n *Instance) dial(p protocol.ID, service string) (*clients.Pool, error) {
	pool, err := clients.NewPool(n.Host(), &fromDiscovery{node: n, name: service}, p, maxAuthConnections)
	if err != nil {
		return nil, err
	}
	n.closers = append(n.closers, closerFunc(pool.Close))
	return pool, nil
}

func (n *Instance) openAuth() error {
	if !n.cfg.runs(Auth) {
		return nil
	}

	// Auth keeps users in its own database and keys and OAuth client secrets in the vault.
	db, err := n.kvdb(Auth)
	if err != nil {
		return err
	}
	n.closers = append(n.closers, closerFunc(db.Close))
	registry, err := n.forgeRegistry()
	if err != nil {
		return err
	}
	v := newVault(n)
	n.closers = append(n.closers, v)
	authOptions := []authService.Option{
		authService.WithUsers(db, v.Cloud(authService.VaultService), n.authConfig()),
		authService.WithForges(registry),
	}
	identity, err := n.identityOption()
	if err != nil {
		return err
	}
	if identity != nil {
		authOptions = append(authOptions, identity)
	}

	svc, err := authService.New(n.Host(), authOptions...)
	if err != nil {
		return err
	}
	n.auth = svc
	n.closers = append(n.closers, svc)
	return nil
}

// BuildAddr is where this node's build service listens, nil when it runs
// none or it has not started.
func (n *Instance) BuildAddr() net.Addr {
	if f, ok := service.Get[hosted.Service](&added{node: n}); ok {
		return f.Addr()
	}
	return nil
}

// artifacts is what a built resource currently runs. It drops the kind, since a
// resource id is unique. The answer is cached for the register pointer's TTL, how
// long a node may run the previous artifact after a build.
type artifacts struct {
	node *Instance

	mu   sync.Mutex
	seen map[string]builtAt
}

type builtAt struct {
	cid  string
	when time.Time
}

// BuildArtifacts is what this node's worker reads a built resource's artifact
// from when nothing was injected: the build service, asked over p2p. A
// composer that injects its own resolver hands this to it to fall back on.
func (n *Instance) BuildArtifacts() worker.Artifacts {
	return &artifacts{node: n, seen: map[string]builtAt{}}
}

func (a *artifacts) Artifact(ctx context.Context, project, environment, kind, resource string) (string, error) {
	key := strings.Join([]string{project, environment, resource}, "\x00")

	if cid, ok := a.cached(key); ok {
		return cid, nil
	}

	// THIS NODE'S OWN JOBS ARE READ IN-PROCESS, as its own register is: a
	// host does not dial itself, so a single-node cloud asking the swarm would
	// find only itself and never resolve anything. A follower says
	// Unavailable, and then the fleet is asked as before.
	if built, ok := service.Get[jobs.Service](&added{node: a.node}); ok {
		cid, err := built.Built(project, environment, resource)
		if err == nil {
			a.remember(key, cid)
			return cid, nil
		}
		if connect.CodeOf(err) != connect.CodeUnavailable {
			return "", notBuilt(err, project, environment, resource)
		}
	}

	// The view is asked before the pool, since this is the request path:
	// fromDiscovery.Peers would wait forever on a cloud with no build node. A failed
	// lookup is not cached.
	if len((&fromDiscovery{node: a.node, name: Build}).Known()) == 0 {
		return "", fmt.Errorf("%w: %s/%s is built by the platform and no build service is reachable",
			ErrNoBuild, project, environment)
	}

	pool, err := a.node.dial(build.Protocol, Build)
	if err != nil {
		return "", err
	}
	client, err := clients.Builds(pool)
	if err != nil {
		return "", err
	}

	cid, err := client.Artifact(ctx, project, environment, resource)
	if err != nil {
		// A FAILED LOOKUP IS NOT CACHED. "Nothing has built this yet" is a
		// state that ends the moment a build finishes, and remembering it would
		// keep a node refusing a resource for a TTL after it became runnable.
		return "", notBuilt(err, project, environment, resource)
	}

	a.remember(key, cid)
	return cid, nil
}

// notBuilt is the build service's NotFound as ErrNotBuilt, and anything else
// as it came.
func notBuilt(err error, project, environment, resource string) error {
	if connect.CodeOf(err) == connect.CodeNotFound {
		return fmt.Errorf("%w: %s/%s's %s", ErrNotBuilt, project, environment, resource)
	}
	return err
}

func (a *artifacts) remember(key, cid string) {
	a.mu.Lock()
	a.seen[key] = builtAt{cid: cid, when: a.node.clock()}
	a.mu.Unlock()
}

func (a *artifacts) cached(key string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	at, ok := a.seen[key]
	if !ok || a.node.clock().Sub(at.when) >= a.node.cfg.Worker.Pointer() {
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
