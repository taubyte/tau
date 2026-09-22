package node

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/build"
	"github.com/taubyte/tau/services/build/artifact"
	"github.com/taubyte/tau/services/build/backend"
	"github.com/taubyte/tau/services/build/clients"
	"github.com/taubyte/tau/services/build/deploy"
	"github.com/taubyte/tau/services/build/frontend"
	"github.com/taubyte/tau/services/build/frontend/githubauth"
	authService "github.com/taubyte/tau/services/v2/auth"
)

// build.go composes the two halves of the build service and the auth they both
// ask.
//
//	auth           the store of projects, repositories, hooks and their keys.
//	build-backend  the queue, the job records, and the runners that clone a
//	               pushed commit and deploy it.
//	build-frontend the webhook a provider posts to, and the job API a
//	               developer reads.
//
// BOTH HALVES REACH AUTH AS A CLIENT OVER THE SWARM, even on a node that runs
// auth itself. It is the one place this composition does not take the local
// shortcut it takes for the register, and the reason is the boundary the split
// exists for: a role that could reach auth's store in-process would be a role
// holding auth's store.

// maxAuthConnections bounds the streams one node keeps to auth. It is small
// because every answer is cached in front of it — the frontend caches hook
// secrets (frontend/secrets.go) — and everything else is once per job.
const maxAuthConnections = 4

// pollInterval is how often a waiting lookup looks again. The stats view is
// gossiped and changes on its own schedule; there is nothing to subscribe to.
const pollInterval = 250 * time.Millisecond

// ErrNoBuildBackend is a resource the platform builds, on a node that can find
// nothing that builds.
var ErrNoBuildBackend = errors.New("node: no build service is reachable")

// fromStats is a discovery.Resolver over the view every other locator on this
// node reads.
//
// THERE IS ONE ANSWER TO "WHO DOES THIS" AND THIS IS IT. A second lookup
// mechanism for the build service would be a second answer that could disagree
// with the one a worker and a gateway already use.
type fromStats struct {
	node *Node
	name string
}

func (f *fromStats) Known() []libp2ppeer.ID {
	// A NODE WITH NO VIEW KNOWS NOTHING, and says so as a nil rather than as an
	// empty list it built: the two read the same to a caller that counts them
	// and not to one that asks whether it looked.
	if f.node.stats == nil {
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

func (f *fromStats) Any(ctx context.Context) (libp2ppeer.ID, error) {
	peers, err := f.Peers(ctx, 1)
	if err != nil {
		return "", err
	}
	return peers[0], nil
}

func (f *fromStats) Peers(ctx context.Context, want int) ([]libp2ppeer.ID, error) {
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

// Failed does nothing, and the nothing is the point.
//
// A DIAL THAT FAILED IS NOT EVIDENCE THE NODE STOPPED RUNNING THE SERVICE. The
// view already ages a row out when its node stops reporting, which is the one
// signal that means what "this peer is gone" would mean here. Dropping a peer
// on a failed dial would let one bad connection remove the only node running a
// service from every lookup after it.
func (f *fromStats) Failed(libp2ppeer.ID) {}

// dial opens a pool onto whichever node runs `service`, on `p`.
func (n *Node) dial(p protocol.ID, service string) (*clients.Pool, error) {
	pool, err := clients.NewPool(n.opts.host, &fromStats{node: n, name: service}, p, maxAuthConnections)
	if err != nil {
		return nil, err
	}
	n.closers = append(n.closers, closerFunc(pool.Close))
	return pool, nil
}

func (n *Node) openAuth() error {
	if !n.cfg.runs(Auth) {
		return nil
	}
	if n.opts.databases == nil {
		return errors.New("node: auth needs somewhere to keep projects, repositories and hooks")
	}

	db, err := n.opts.databases.New(logger, Auth, rebroadcastSeconds)
	if err != nil {
		return err
	}
	n.closers = append(n.closers, closerFunc(func() { db.Close() }))

	// A CLOUD THAT HAS NEVER BEEN GIVEN A KEY PAIR STILL STARTS, and is told
	// what that costs. Domain-ownership claims are signed with this key, so a
	// minted one means anything this cloud verified under a previous key stops
	// verifying — which is fine for a development cloud and is a data loss for
	// a real one. The alternative would be a key baked into the binary, and
	// then every cloud could sign for every other cloud's domains.
	cfg := n.cfg.Auth
	if cfg.DomainValidation.Private == "" && cfg.DomainValidation.Public == "" {
		pair, err := authService.GenerateKeyPair()
		if err != nil {
			return fmt.Errorf("node: auth: %w", err)
		}
		cfg.DomainValidation = pair
		logger.Warnf("auth: no domain-validation key pair was configured, so one was minted for this run. " +
			"Every domain this cloud has already verified stops verifying, and will again on the next restart. " +
			"Put a pair in the config to keep them.")
	}

	svc, err := authService.New(n.opts.host, db, cfg)
	if err != nil {
		return err
	}
	n.auth = svc
	n.closers = append(n.closers, svc)
	return nil
}

// openBuildBackend is the private half: the queue, the job records, and the
// runner that turns a push into a deploy.
func (n *Node) openBuildBackend() error {
	if !n.cfg.runs(BuildBackend) {
		return nil
	}
	if n.opts.raft == nil {
		return ErrNoRaft
	}

	cluster, err := n.opts.raft(BuildBackend)
	if err != nil {
		return fmt.Errorf("node: %s: %w", BuildBackend, err)
	}
	n.closers = append(n.closers, closerFunc(func() { cluster.Close() }))

	auth, err := n.dial(clients.AuthProtocol, Auth)
	if err != nil {
		return err
	}
	repositories, err := clients.Repositories(auth)
	if err != nil {
		return err
	}
	projects, err := clients.Projects(auth)
	if err != nil {
		return err
	}

	// SSH, BECAUSE A DEPLOY KEY IS AN SSH KEY. auth holds one per repository,
	// and a cloud reaching a provider over https could only read what is
	// already public.
	clone := deploy.NewClone(repositories, deploy.WithURL(deploy.SSH))

	options := []deploy.Option{
		deploy.WithProjects(projects),
		deploy.WithCheckout(clone),
		deploy.WithRegister(n.publisher),
		deploy.WithCloudFqdn(n.cfg.Gateway.CloudFqdn),
	}

	// A NODE THAT CANNOT RUN A CONTAINER CANNOT BUILD CODE, and it says so on
	// the push rather than starting and refusing every one. The two halves of
	// what a push can be are independent: a node with no container backend
	// still deploys configuration, which is most of what a small cloud does.
	if n.cfg.Container.declared() {
		backend, err := containerBackend(n.cfg.Container)
		if err != nil {
			return err
		}

		engine, err := artifact.NewEngine(backend)
		if err != nil {
			return err
		}

		buildOptions := []artifact.Option{
			artifact.WithRegister(buildRegister{n}),
			artifact.WithProjects(projects),
			artifact.WithCheckout(clone),
			artifact.WithEngine(engine),
		}
		// THE SAME VAULT THE WORKER READS, and by the same rule — an injected
		// resolver wins, then this node's own or one in the fleet. What
		// differs is which variables reach it: `at: build` says the value is
		// baked into the artifact, and the config decides that, not this
		// wiring.
		switch {
		case n.opts.secrets != nil:
			buildOptions = append(buildOptions, artifact.WithSecrets(n.opts.secrets))
		case n.secrets != nil || n.stats != nil:
			v := newVault(n)
			n.closers = append(n.closers, v)
			buildOptions = append(buildOptions, artifact.WithSecrets(v))
		}

		builder, err := artifact.New(buildOptions...)
		if err != nil {
			return err
		}
		options = append(options, deploy.WithBuilder(builder))
	}

	runner, err := deploy.New(options...)
	if err != nil {
		return err
	}

	service := []backend.Option{
		backend.WithHost(n.opts.host),
		backend.WithStore(cluster),
		backend.WithRepositories(repositories),
		backend.WithRunner(runner),
	}
	if n.opts.assets != nil {
		service = append(service, backend.WithAssets(n.opts.assets))
	}

	svc, err := backend.New(n.cfg.BuildBackend, service...)
	if err != nil {
		return err
	}
	n.buildBackend = svc
	n.closers = append(n.closers, svc)
	return nil
}

// openBuildFrontend is the public half: a webhook and a job API.
func (n *Node) openBuildFrontend() error {
	if !n.cfg.runs(BuildFrontend) {
		return nil
	}

	auth, err := n.dial(clients.AuthProtocol, Auth)
	if err != nil {
		return err
	}
	hooks, err := clients.Hooks(auth)
	if err != nil {
		return err
	}
	projects, err := clients.Projects(auth)
	if err != nil {
		return err
	}

	backends, err := n.dial(build.Protocol, BuildBackend)
	if err != nil {
		return err
	}
	builds, err := clients.Builds(backends)
	if err != nil {
		return err
	}

	// THE PROVIDER IS THE IDENTITY. Whether a caller may act on a repository is
	// that repository's host's answer and never this cloud's, which is what
	// keeps a token this cloud has not stored from becoming one it has.
	identity, err := githubauth.New()
	if err != nil {
		return err
	}

	frontendOptions := []frontend.Option{
		frontend.WithHooks(hooks),
		frontend.WithProjects(projects),
		frontend.WithBackend(builds),
		frontend.WithIdentity(identity),
	}
	// THE PUBLIC ROLE NEVER HOLDS A VAULT ITSELF. It is the one process a
	// stranger reaches, and withDefaults already refuses it running the
	// backend for the same reason — so this is always the stream onto a node
	// that does, and it exists at all only because a node running stats can
	// find one.
	if n.stats != nil {
		v := newVault(n)
		n.closers = append(n.closers, v)
		frontendOptions = append(frontendOptions, frontend.WithVault(v))
	}

	svc, err := frontend.New(n.cfg.BuildFrontend, frontendOptions...)
	if err != nil {
		return err
	}
	n.buildFrontend = svc
	n.closers = append(n.closers, svc)
	return nil
}

// buildRegister is how a code build reads the configuration it builds against.
//
// IT RESOLVES PER CALL for the same reason the deploy's publisher does: which
// node answers is a routing question, and one held from construction would be
// whichever node this dialled at boot, forever.
type buildRegister struct{ node *Node }

func (b buildRegister) Environments(ctx context.Context, project string) ([]string, error) {
	r, err := b.reader(ctx)
	if err != nil {
		return nil, err
	}
	return r.Environments(ctx, project)
}

func (b buildRegister) Resolve(ctx context.Context, project, environment string) (pkgregister.Pointer, error) {
	r, err := b.reader(ctx)
	if err != nil {
		return pkgregister.Pointer{}, err
	}
	return r.Resolve(ctx, project, environment)
}

func (b buildRegister) Object(ctx context.Context, id pkgregister.ObjectID) ([]byte, error) {
	r, err := b.reader(ctx)
	if err != nil {
		return nil, err
	}
	return r.Object(ctx, id)
}

// reader is the register this node reads, local when it runs one. It is
// node/locate.go's one reach, narrowed to what a build may ask.
func (b buildRegister) reader(ctx context.Context) (artifact.Register, error) {
	return b.node.registers.Now(ctx)
}

// artifacts is what a BUILT resource currently runs.
//
// IT DROPS THE KIND, and that is this composer's line to write rather than a
// shape either side bends to: a resource id is already unique, so it is not
// part of the key a build wrote, and the worker passes a kind because a caller
// looking up a resource has one in hand.
//
// THE ANSWER IS CACHED, briefly. A worker resolves this per invocation and the
// row only moves when a build finishes, so a lookup per request would be an
// RPC per request for an answer that changes on a deploy's timescale. The TTL
// IS THAT TIMESCALE — how long a node may keep running the previous artifact
// after a new one is built — which is the same thing the register's pointer TTL
// is, and it is set from the same place.
type artifacts struct {
	node *Node

	mu   sync.Mutex
	seen map[string]builtAt
}

type builtAt struct {
	cid  string
	when time.Time
}

func (a *artifacts) Artifact(ctx context.Context, project, environment, kind, resource string) (string, error) {
	key := strings.Join([]string{project, environment, resource}, "\x00")

	if cid, ok := a.cached(key); ok {
		return cid, nil
	}

	// THE VIEW IS ASKED BEFORE THE POOL IS, and the reason is that this runs on
	// the REQUEST PATH. fromStats.Peers waits until a peer appears or the
	// context ends, which is right for a pool opened at startup — services come
	// up concurrently — and wrong here: on a cloud running no build backend at
	// all there is nothing to wait for, and a request whose context has no
	// deadline waits forever instead of being refused.
	//
	// So it is the same rule the register and the vault already keep: ask the
	// view, and if the view knows nobody, say so. A backend that appears a
	// moment later is picked up by the next request, because a failed lookup is
	// not cached.
	if len((&fromStats{node: a.node, name: BuildBackend}).Known()) == 0 {
		return "", fmt.Errorf("%w: %s/%s is built by the platform and no build service is reachable",
			ErrNoBuildBackend, project, environment)
	}

	pool, err := a.node.dial(build.Protocol, BuildBackend)
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
		return "", err
	}

	a.mu.Lock()
	a.seen[key] = builtAt{cid: cid, when: a.node.clock()}
	a.mu.Unlock()
	return cid, nil
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
func (n *Node) clock() time.Time {
	if n.opts.now != nil {
		return n.opts.now()
	}
	return time.Now()
}

// publisher is where a deploy publishes.
//
// IT IS THE ONLY WRITE PATH TO THE REGISTER IN THIS COMPOSITION. registerSource
// is read-only on purpose — "nothing this composition wires is allowed to
// deploy" — and this is the exception that proves it: a deploy is what a push
// became, and a push is the only thing that may.
func (n *Node) publisher(ctx context.Context) (deploy.Register, error) {
	return n.registers.Now(ctx)
}
