package node

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	gossh "golang.org/x/crypto/ssh"

	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/pkg/tenancy"
	authService "github.com/taubyte/tau/services/auth"
	"github.com/taubyte/tau/services/build"
	"github.com/taubyte/tau/services/build/artifact"
	"github.com/taubyte/tau/services/build/backend"
	"github.com/taubyte/tau/services/build/clients"
	"github.com/taubyte/tau/services/build/deploy"
	"github.com/taubyte/tau/services/build/frontend"
	"github.com/taubyte/tau/services/build/frontend/forgeauth"
)

// build.go composes the two halves of the build service and the auth they both
// ask.
//
//	auth           who is admitted to this cloud, once a forge said who a caller is.
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

// pollInterval is how often a waiting lookup looks again. The discovery view is
// gossiped and changes on its own schedule; there is nothing to subscribe to.
const pollInterval = 250 * time.Millisecond

// ErrNoBuildBackend is a resource the platform builds, on a node that can find
// nothing that builds.
var ErrNoBuildBackend = errors.New("node: no build service is reachable")

// fromDiscovery is a discovery.Resolver over the view every other locator on this
// node reads.
//
// THERE IS ONE ANSWER TO "WHO DOES THIS" AND THIS IS IT. A second lookup
// mechanism for the build service would be a second answer that could disagree
// with the one a worker and a gateway already use.
type fromDiscovery struct {
	node *Node
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

// Failed does nothing, and the nothing is the point.
//
// A DIAL THAT FAILED IS NOT EVIDENCE THE NODE STOPPED RUNNING THE SERVICE. The
// view already ages a row out when its node stops reporting, which is the one
// signal that means what "this peer is gone" would mean here. Dropping a peer
// on a failed dial would let one bad connection remove the only node running a
// service from every lookup after it.
func (f *fromDiscovery) Failed(libp2ppeer.ID) {}

// dial opens a pool onto whichever node runs `service`, on `p`.
func (n *Node) dial(p protocol.ID, service string) (*clients.Pool, error) {
	pool, err := clients.NewPool(n.opts.host, &fromDiscovery{node: n, name: service}, p, maxAuthConnections)
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

	// AUTH KEEPS THIS CLOUD'S USERS in a database of its own, and its keys —
	// each node's token-signing key and pepper, and the operator's OAuth
	// client secrets — in the vault. Who a project is lives in the register;
	// whether a caller is admitted is answered from the users, or from
	// whatever identity this node was composed with.
	if n.opts.databases == nil {
		return ErrNoDatabases
	}
	db, err := n.opts.databases.New(logger, Auth, rebroadcastSeconds)
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
		authService.WithUsers(db, v, n.authConfig()),
		authService.WithForges(registry),
	}
	identity, err := n.identityOption()
	if err != nil {
		return err
	}
	if identity != nil {
		authOptions = append(authOptions, identity)
	}

	svc, err := authService.New(n.opts.host, authOptions...)
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
func (n *Node) buildClone(keys deploy.Credentials) (*deploy.Clone, error) {
	registry, err := n.forgeRegistry()
	if err != nil {
		return nil, err
	}
	pinned, err := hostKeys(n.cfg.Forges)
	if err != nil {
		return nil, err
	}
	return deploy.NewClone(keys,
		deploy.WithAddress(deploy.Forged(registry)),
		deploy.WithHostKeys(func(host string) []gossh.PublicKey { return pinned[host] })), nil
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

	options := []deploy.Option{
		deploy.WithProjects(projects),
		deploy.WithCheckout(clone),
		deploy.WithRegister(n.publisher),
		deploy.WithCloudFqdn(n.cfg.Gateway.CloudFqdn),
	}
	if n.opts.regions != nil {
		options = append(options, deploy.WithRegions(n.opts.regions))
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

		// A source's clone URL is keyed by the forge that serves it, the same
		// function every push's repository key came out of.
		registry, err := n.forgeRegistry()
		if err != nil {
			return err
		}

		buildOptions := []artifact.Option{
			artifact.WithRegister(buildRegister{n}),
			artifact.WithProjects(projects),
			artifact.WithCheckout(clone),
			artifact.WithEngine(engine),
			artifact.WithKeys(registry),
		}
		// THE SAME VAULT THE WORKER READS, and by the same rule — an injected
		// resolver wins, then this node's own or one in the fleet. What
		// differs is which variables reach it: `at: build` says the value is
		// baked into the artifact, and the config decides that, not this
		// wiring.
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
		backend.WithHost(n.opts.host),
		backend.WithStore(cluster),
		backend.WithRepositories(projects),
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
	projects := projectTable{n}

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
		// THE PROJECT TABLE IS THE REGISTER'S, and this role writes it only
		// through the link route, after the caller is shown to write to every
		// repository the link names.
		frontend.WithLinker(projects),
		// WHO MAY USE THIS CLOUD IS AUTH'S TO SAY, from whatever identity
		// its node was composed with — see identity.go.
		frontend.WithAdmissions(&admissions{forges: registry, auth: identities}),
		// THE TAU TOKEN IS THE ONE CREDENTIAL; the forge credential each
		// route asks with is a connection sealed inside it, which auth opens.
		frontend.WithConnections(connections{auth: identities}),
		// SIGNING IN IS AUTH'S, carried here because auth has no ingress.
		frontend.WithAuthAPI(authCalls),
		frontend.WithProjects(projects),
		frontend.WithBackend(builds),
		frontend.WithIdentity(forges),
		// WHAT A LINK INSTALLS AT THE FORGE is installed here, under the
		// caller's connection, so no client is handed a forge credential.
		frontend.WithInstaller(forges),
		frontend.WithRepositories(forges),
		frontend.WithDeliveries(forges),
		frontend.WithTenancy(served),
		frontend.WithCloudFqdn(n.cfg.Gateway.CloudFqdn),
	}
	// A FORGE WITH NO SIGNED WEBHOOK REPORTS PUSHES ON ITS OWN FEED (Gerrit's
	// stream-events), which this frontend subscribes to as a second source of
	// the same push — see frontend/streams.go.
	// A FINISHED JOB'S LOGS AND ARTIFACTS are read by the cid the job records,
	// from the store the backend put them in.
	if n.opts.assets != nil {
		frontendOptions = append(frontendOptions, frontend.WithAssets(n.opts.assets))
	}
	if len(registry.EventHosts()) > 0 {
		frontendOptions = append(frontendOptions, frontend.WithStreams(forges, projects))
	}
	// THE PUBLIC ROLE NEVER HOLDS A VAULT ITSELF. It is the one process a
	// stranger reaches, and withDefaults already refuses it running the
	// backend for the same reason — so this is always the stream onto a node
	// that does, and it exists at all only because a node running discovery can
	// find one.
	//
	// THE HOOK SECRET IS READ THROUGH IT, and only that: verifying a push needs
	// the project's secret, which the developer's link stored there. Nothing a
	// caller sends reads a value back.
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

	// A CONSOLE AUTH MAY RETURN A SIGN-IN TO is a console that may call this
	// ingress from a browser: its callbacks' origins are the CORS allow-list.
	// OVER TLS WHEN THE CLOUD ISSUES CERTIFICATES: a forge's hook is
	// installed at https://build-frontend.<cloud fqdn>, and the certificate
	// for that name comes from the same client every listener here uses.
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
func (n *Node) BuildFrontendAddr() net.Addr {
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
	// the REQUEST PATH. fromDiscovery.Peers waits until a peer appears or the
	// context ends, which is right for a pool opened at startup — services come
	// up concurrently — and wrong here: on a cloud running no build backend at
	// all there is nothing to wait for, and a request whose context has no
	// deadline waits forever instead of being refused.
	//
	// So it is the same rule the register and the vault already keep: ask the
	// view, and if the view knows nobody, say so. A backend that appears a
	// moment later is picked up by the next request, because a failed lookup is
	// not cached.
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

// AsDestination is the region whose views this node reads: every project it serves is
// read as that region's view of it, and nothing placed elsewhere reaches it.
//
// WITHOUT IT THE NODE READS schema.DefaultRegion, the one destination of a build with no
// regions — so an OSS node reads the view a deploy published for it with no region in
// its configuration at all. A build serving several regions says which this node is.
func AsDestination(region string) Option {
	return func(o *options) error {
		if region == "" {
			return errors.New("node: a destination with no name")
		}
		o.destination = region
		return nil
	}
}

// WithRegions is what this node's deploys answer about where a project runs. Without
// it a deploy reads no regions (schema.NoRegion): everything is placed in
// schema.DefaultRegion, and any region a project names is said.
func WithRegions(r schema.Regions) Option {
	return func(o *options) error {
		if r == nil {
			return errors.New("node: nil regions")
		}
		o.regions = r
		return nil
	}
}
