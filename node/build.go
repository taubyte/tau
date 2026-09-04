package node

import (
	"context"
	"errors"
	"fmt"
	"time"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/services/build"
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
	if f.node.stats == nil {
		return nil
	}

	rows := f.node.stats.Route(f.name, f.node.cfg.Stats.Freshness())
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
	runner, err := deploy.New(
		deploy.WithProjects(projects),
		deploy.WithCheckout(deploy.NewClone(repositories, deploy.WithURL(deploy.SSH))),
		deploy.WithRegister(n.publisher),
		deploy.WithCloudFqdn(n.cfg.Gateway.CloudFqdn),
	)
	if err != nil {
		return err
	}

	svc, err := backend.New(n.cfg.BuildBackend,
		backend.WithHost(n.opts.host),
		backend.WithStore(cluster),
		backend.WithRepositories(repositories),
		backend.WithRunner(runner),
	)
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

	svc, err := frontend.New(n.cfg.BuildFrontend,
		frontend.WithHooks(hooks),
		frontend.WithProjects(projects),
		frontend.WithBackend(builds),
		frontend.WithIdentity(identity),
	)
	if err != nil {
		return err
	}
	n.buildFrontend = svc
	n.closers = append(n.closers, svc)
	return nil
}

// publisher is where a deploy publishes.
//
// IT IS THE ONLY WRITE PATH TO THE REGISTER IN THIS COMPOSITION. registerSource
// is read-only on purpose — "nothing this composition wires is allowed to
// deploy" — and this is the exception that proves it: a deploy is what a push
// became, and a push is the only thing that may.
func (n *Node) publisher(context.Context) (deploy.Register, error) {
	if n.register != nil {
		return n.register.Local(), nil
	}
	return n.registerClientNow()
}
