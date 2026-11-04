package instance

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/taubyte/tau/pkg/containers"

	"connectrpc.com/connect"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	gossh "golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/pkg/forge/drivers"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/pkg/tenancy"
	authService "github.com/taubyte/tau/services/auth"
	"github.com/taubyte/tau/services/build"
	"github.com/taubyte/tau/services/build/artifact"
	"github.com/taubyte/tau/services/build/backend"
	"github.com/taubyte/tau/services/build/clients"
	"github.com/taubyte/tau/services/build/deploy"
	"github.com/taubyte/tau/services/build/folders"
	"github.com/taubyte/tau/services/build/frontend"
	"github.com/taubyte/tau/services/build/frontend/forgeauth"
	"github.com/taubyte/tau/services/build/run"
	"github.com/taubyte/tau/services/build/uploads"
	"github.com/taubyte/tau/services/worker"
)

// Composes the two halves of the build service and the auth they both ask:
//
//	auth           who is admitted to this cloud, once a forge said who a caller is.
//	build-backend  the queue, the job records, and the runners that clone a
//	               pushed commit and deploy it.
//	build-frontend the webhook a provider posts to, and the job API a
//	               developer reads.
//
// Both halves reach auth as a client over the swarm even when it runs locally, so no
// role holds auth's store.

// maxAuthConnections bounds the streams one node keeps to auth. It is small
// because every answer is cached in front of it — the frontend caches hook
// secrets (frontend/secrets.go) — and everything else is once per job.
const maxAuthConnections = 4

// pollInterval is how often a waiting lookup looks again. The discovery view is
// gossiped and changes on its own schedule; there is nothing to subscribe to.
const pollInterval = 250 * time.Millisecond

// ErrNoBuildBackend is a resource the platform builds, on a node that can find
// nothing that builds.
var ErrNoBuildBackend = errors.New("node: no build service is reachable")

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

// buildClone is the checkout the build backend clones with.
//
// SSH WHERE A DEPLOY KEY IS, HTTPS WHERE NONE IS: a deploy key is an ssh key,
// and a repository without one is read anonymously or not at all. Both
// addresses, and the ssh account, are the forge's; an ssh host is pinned.
func (n *Instance) buildClone(keys deploy.Credentials) (*deploy.Clone, error) {
	registry, err := n.forgeRegistry()
	if err != nil {
		return nil, err
	}
	pinned, err := drivers.HostKeys(n.cfg.Forges)
	if err != nil {
		return nil, err
	}
	return deploy.NewClone(keys,
		deploy.WithAddress(deploy.Forged(registry)),
		deploy.WithHostKeys(func(host string) []gossh.PublicKey { return pinned[host] })), nil
}

// openBuildBackend is the private half: the queue, the job records, and the
// runner that turns a push into a deploy.
func (n *Instance) openBuildBackend() error {
	if !n.cfg.runs(BuildBackend) {
		return nil
	}
	cluster, err := n.raftGroup(BuildBackend)
	if err != nil {
		return fmt.Errorf("node: %s: %w", BuildBackend, err)
	}
	n.closers = append(n.closers, closerFunc(func() { cluster.Close() }))

	// THE QUEUE IS RAFT'S AND NOTHING ELSE IS: every job's events and rows live in
	// a kvdb database of the backend's own.
	records, err := n.kvdb(BuildBackend + "/jobs")
	if err != nil {
		return fmt.Errorf("node: %s: %w", BuildBackend, err)
	}

	// WHO A PUSH BELONGS TO IS THE REGISTER'S PROJECT TABLE, and the deploy
	// key that clones it is the vault's, under the name the developer's link
	// stored it as (pkg/linkcreds).
	projects := projectTable{n}

	// ONE VAULT FOR THE BACKEND, made on first need: the deploy keys below and
	// a code build's secrets read the same one, by the worker's rule — an
	// injected resolver wins, then this node's own or one in the fleet.
	var backendVault secretReader
	vaults := func() secretReader {
		if backendVault != nil {
			return backendVault
		}
		switch {
		case n.opts.secrets != nil:
			backendVault = n.opts.secrets
		case n.secrets != nil || n.discovery != nil:
			v := newVault(n)
			n.closers = append(n.closers, v)
			backendVault = v
		}
		return backendVault
	}
	clone, err := n.buildClone(linkSecrets{projects: projects, vault: vaults()})
	if err != nil {
		return err
	}

	// WHAT A PUSH IS READ AND RUN WITH: the forge's clone, and the container engine. A
	// development cloud's configuration puts its local folders and uploaded builds in
	// front of them.
	var (
		checkout deploy.Checkout = clone
		confirm  run.Confirmer   = clone
		engine   artifact.Engine
	)

	// A NODE THAT CANNOT RUN A CONTAINER CANNOT BUILD CODE, and it says so on
	// the push rather than starting and refusing every one. The two halves of
	// what a push can be are independent: a node with no container backend
	// still deploys configuration, which is most of what a small cloud does.
	if n.cfg.Container.Any() {
		backend, err := containers.Open(n.cfg.Container)
		if err != nil {
			return err
		}

		if engine, err = artifact.NewEngine(backend); err != nil {
			return err
		}
	}
	if local := n.cfg.BuildBackend.Local; len(local) > 0 {
		f, err := folders.New(local)
		if err != nil {
			return err
		}
		checkout, confirm = f.Checkout(checkout), f.Confirm(confirm)
	}
	if dir := n.cfg.BuildBackend.Uploads; dir != "" {
		held, err := uploads.Open(dir)
		if err != nil {
			return fmt.Errorf("node: %s: uploads: %w", BuildBackend, err)
		}
		checkout, confirm, engine = held.Checkout(checkout), held.Confirm(confirm), held.Engine(engine)
	}

	options := []deploy.Option{
		deploy.WithProjects(projects),
		deploy.WithCheckout(checkout),
		deploy.WithRegister(n.publisher),
		deploy.WithCloudFqdn(n.cfg.CloudFqdn),
	}
	if n.opts.regions != nil {
		options = append(options, deploy.WithRegions(n.opts.regions))
	}
	domains, err := n.domainProver()
	if err != nil {
		return err
	}
	options = append(options, deploy.WithDomains(domains))

	if engine != nil {
		// A source's clone URL is keyed by the forge that serves it, the same
		// function every push's repository key came out of.
		registry, err := n.forgeRegistry()
		if err != nil {
			return err
		}

		buildOptions := []artifact.Option{
			artifact.WithRegister(buildRegister{n}),
			artifact.WithProjects(projects),
			artifact.WithCheckout(checkout),
			artifact.WithEngine(engine),
			artifact.WithKeys(registry),
		}
		if v := vaults(); v != nil {
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
		backend.WithHost(n.Host()),
		backend.WithStore(cluster),
		backend.WithRecords(records),
		backend.WithRepositories(projects),
		backend.WithRunner(runner),
		// A PUSH IS BUILT ONLY FOR WHAT THE REPOSITORY HOLDS: the ref is read
		// at the forge over the repository's deploy key before it is queued.
		backend.WithConfirmer(confirm),
	}
	service = append(service, backend.WithAssets(n.store))

	svc, err := backend.New(n.cfg.BuildBackend, service...)
	if err != nil {
		return err
	}
	n.buildBackend = svc
	n.closers = append(n.closers, svc)
	return nil
}

// openBuildFrontend is the public half: a webhook and a job API.
func (n *Instance) openBuildFrontend() error {
	if !n.cfg.runs(BuildFrontend) {
		return nil
	}

	auth, err := n.dial(clients.AuthProtocol, Auth)
	if err != nil {
		return err
	}
	projects := projectTable{n}

	backends, err := n.dial(build.Protocol, BuildBackend)
	if err != nil {
		return err
	}
	builds, err := clients.Builds(backends)
	if err != nil {
		return err
	}

	served, err := tenancy.Parse(n.cfg.Tenancy)
	if err != nil {
		return err
	}
	registry, err := n.forgeRegistry()
	if err != nil {
		return err
	}
	forges, err := forgeauth.New(registry)
	if err != nil {
		return err
	}

	identities, err := clients.Identities(auth)
	if err != nil {
		return err
	}
	authCalls, err := clients.AuthCalls(auth)
	if err != nil {
		return err
	}

	frontendOptions := []frontend.Option{
		// THE PROJECT TABLE IS THE REGISTER'S: who a project's owner is and
		// what roles it granted. This role writes it only through the link
		// and grant routes, after authorizing the caller.
		frontend.WithLinker(projects),
		frontend.WithProjects(projects),
		// WHO A CALLER IS IS AUTH'S TO SAY, from their tau token.
		frontend.WithCallers(identities),
		// SIGNING IN IS AUTH'S, carried here because auth has no ingress.
		frontend.WithAuthAPI(authCalls),
		frontend.WithBackend(builds),
		frontend.WithDeliveries(forges),
		frontend.WithTenancy(served),
		frontend.WithCloudFqdn(n.cfg.CloudFqdn),
	}
	// A forge with no signed webhook reports pushes on its own feed (Gerrit's
	// stream-events), subscribed to as a second source (frontend/streams.go). A finished
	// job's logs and artifacts are read by cid from the storage service.
	frontendOptions = append(frontendOptions, frontend.WithAssets(n.store))
	if len(registry.EventHosts()) > 0 {
		frontendOptions = append(frontendOptions, frontend.WithStreams(forges, projects))
	}
	// What a deployment runs, read back for a developer's build against it.
	frontendOptions = append(frontendOptions, frontend.WithPublished(buildRegister{n}))
	// The public role never holds a vault: it is the process strangers reach. This is
	// always a stream to a node that does, used only for hook secrets.
	var linked secretReader
	switch {
	case n.opts.secrets != nil:
		linked = n.opts.secrets
	case n.discovery != nil:
		v := newVault(n)
		n.closers = append(n.closers, v)
		linked = v
		frontendOptions = append(frontendOptions, frontend.WithVault(v))
	}
	frontendOptions = append(frontendOptions, frontend.WithHooks(linkSecrets{projects: projects, vault: linked}))
	domains, err := n.domains()
	if err != nil {
		return err
	}
	frontendOptions = append(frontendOptions, frontend.WithDomains(domainClaims{domains}))

	// A console auth may redirect to is a console that may call this ingress: its
	// origins are the CORS allow-list. TLS when the cloud issues certificates.
	secure, err := n.ServiceTLS()
	if err != nil {
		return err
	}
	if secure != nil {
		frontendOptions = append(frontendOptions, frontend.WithTLS(secure))
	}
	feCfg := n.cfg.BuildFrontend
	feCfg.Origins = append(slices.Clone(feCfg.Origins), consoleOrigins(n.cfg.Auth.Redirects)...)
	svc, err := frontend.New(feCfg, frontendOptions...)
	if err != nil {
		return err
	}
	n.buildFrontend = svc
	n.closers = append(n.closers, svc)
	return nil
}

// BuildFrontendAddr is where this node's build frontend listens, nil when it
// runs none or has not started.
func (n *Instance) BuildFrontendAddr() net.Addr {
	if n.buildFrontend == nil {
		return nil
	}
	return n.buildFrontend.Addr()
}

// buildRegister is how a code build reads the configuration it builds against.
//
// IT RESOLVES PER CALL for the same reason the deploy's publisher does: which
// node answers is a routing question, and one held from construction would be
// whichever node this dialled at boot, forever.
type buildRegister struct{ node *Instance }

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

// Compiled is frontend.Published: the object a deployment's pointer names.
func (b buildRegister) Compiled(ctx context.Context, project, environment string) ([]byte, error) {
	pointer, err := b.Resolve(ctx, project, environment)
	if err != nil {
		return nil, err
	}
	return b.Object(ctx, pointer.Object)
}

func (b buildRegister) reader(ctx context.Context) (artifact.Register, error) {
	return b.node.registers.Now(ctx)
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

	// THIS NODE'S OWN BACKEND IS READ IN-PROCESS, as its own register is: a
	// host does not dial itself, so a single-node cloud asking the swarm would
	// find only itself and never resolve anything. A follower says
	// Unavailable, and then the fleet is asked as before.
	if a.node.buildBackend != nil {
		cid, err := a.node.buildBackend.Built(project, environment, resource)
		if err == nil {
			a.remember(key, cid)
			return cid, nil
		}
		if connect.CodeOf(err) != connect.CodeUnavailable {
			return "", notBuilt(err, project, environment, resource)
		}
	}

	// The view is asked before the pool, since this is the request path:
	// fromDiscovery.Peers would wait forever on a cloud with no build backend. A failed
	// lookup is not cached.
	if len((&fromDiscovery{node: a.node, name: BuildBackend}).Known()) == 0 {
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

// publisher is where a deploy publishes.
//
// IT IS THE ONLY WRITE PATH TO THE REGISTER IN THIS COMPOSITION. registerSource
// is read-only on purpose — "nothing this composition wires is allowed to
// deploy" — and this is the exception that proves it: a deploy is what a push
// became, and a push is the only thing that may.
func (n *Instance) publisher(ctx context.Context) (deploy.Register, error) {
	return n.registers.Now(ctx)
}

// WithRegions is what this node's deploys answer about where a project runs. Without
// it a deploy reads no regions (schema.WithNoRegions): everything is placed in
// schema.DefaultRegion, and any region a project names is refused.
func WithRegions(r schema.Regions) Option {
	return func(o *options) error {
		if r == nil {
			return errors.New("node: nil regions")
		}
		o.regions = r
		return nil
	}
}
