package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	logging "github.com/ipfs/go-log/v2"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	gossh "golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/pkg/containers"
	"github.com/taubyte/tau/pkg/forge/drivers"
	pkgkvdb "github.com/taubyte/tau/pkg/kvdb"
	"github.com/taubyte/tau/pkg/raft"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/services/build"
	"github.com/taubyte/tau/services/build/artifact"
	"github.com/taubyte/tau/services/build/deploy"
	"github.com/taubyte/tau/services/build/folders"
	"github.com/taubyte/tau/services/build/linked"
	"github.com/taubyte/tau/services/build/run"
	"github.com/taubyte/tau/services/build/uploads"
	"github.com/taubyte/tau/services/cm"
	"github.com/taubyte/tau/services/consensus"
	"github.com/taubyte/tau/services/register"
	"github.com/taubyte/tau/services/secrets"
	"github.com/taubyte/tau/services/storage"
)

// Service is the build backend as an instance runs it.
type Service interface {
	service.Service

	// Built is what last built a deployment's resource: the cid of its artifact.
	Built(project, environment, resource string) (string, error)

	// WaitForLeader returns once the queue's group knows a leader, maybe this node:
	// from then a job is read or refused with who reads it. The group forms off the
	// request path, so a composer that needs one waits here.
	WaitForLeader(ctx context.Context) error
}

// New is the build backend, unstarted. Start builds it from its instance.
func New() Service { return &hosted{} }

// ErrNotStarted is a backend asked for what only a started one has.
var ErrNotStarted = errors.New("build: not started")

// ErrNoConsensus is an instance running no consensus service, so the queue has no
// group to live in.
var ErrNoConsensus = errors.New("build: no consensus service runs beside it")

// retentionPoll is how often the job records reread the cloud's retention.
const retentionPoll = time.Minute

// hosted is the backend as an instance runs it: the server Start built, and what
// Start opened for it, closed with it.
type hosted struct {
	*server
	queue raft.Cluster
	also  []io.Closer
}

func (b *hosted) WaitForLeader(ctx context.Context) error {
	if b.queue == nil {
		return ErrNotStarted
	}
	return b.queue.WaitForLeader(ctx)
}

// Start runs the backend on an instance: its queue a raft group of the consensus
// beside it, its job records a replicated database on the peer, its artifacts and
// logs in the storage service, its projects in the register and its link
// credentials in the vault, both wherever discovery finds them.
func (b *hosted) Start(ctx context.Context, h service.Host) error {
	if b.server != nil {
		return errors.New("build: already started")
	}
	var cfg Config
	if err := h.Config(&cfg); err != nil {
		return err
	}
	agree, ok := service.Get[consensus.Service](h)
	if !ok {
		return ErrNoConsensus
	}
	cluster, err := agree.Raft(build.Backend.Name)
	if err != nil {
		return err
	}
	b.also = append(b.also, closer(func() error { return cluster.Close() }))

	fleet := register.ViewsOn(h)
	b.also = append(b.also, fleet)
	records, err := pkgkvdb.Open(h.Peer(), build.Backend.Name+"/jobs",
		pkgkvdb.WithLogger(logging.Logger("tau.build")),
		pkgkvdb.WithRetentionFrom(func(ctx context.Context) (time.Duration, error) {
			return register.Retention(ctx, fleet)
		}, retentionPoll))
	if err != nil {
		return fmt.Errorf("build: opening the job records: %w", err)
	}
	b.also = append(b.also, closer(func() error { records.Close(); return nil }))

	table := register.TableOn(h)
	vault := secrets.VaultOn(h)
	assets := storage.StoreOn(h)
	b.also = append(b.also, table, vault, assets)
	projects := linked.Projects{Table: table.Now}

	domains, err := provers(h, cfg)
	if err != nil {
		return err
	}
	b.also = append(b.also, domains)
	runner, confirm, err := runnerFor(h.Region(), cfg, projects, table.Now, vault, domains)
	if err != nil {
		return err
	}

	s, err := newServer(cfg,
		withHost(h.Peer().Host()),
		withStore(cluster),
		withRecords(records),
		withRepositories(projects),
		withRunner(runner),
		// A PUSH IS BUILT ONLY FOR WHAT THE REPOSITORY HOLDS: the ref is read at
		// the forge over the repository's deploy key before it is queued.
		withConfirmer(confirm),
		withAssets(assets),
		withClock(h.Now),
	)
	if err != nil {
		return err
	}
	b.server, b.queue = s, cluster
	return s.serve(ctx)
}

// Close stops the server, then what Start opened for it.
func (b *hosted) Close() error {
	var errs []error
	if b.server != nil {
		errs = append(errs, b.server.Close())
	}
	for i := len(b.also) - 1; i >= 0; i-- {
		errs = append(errs, b.also[i].Close())
	}
	b.also = nil
	return errors.Join(errs...)
}

type closer func() error

func (c closer) Close() error { return c() }

// runnerFor is what a push is read and run with: the forge's clone, and the container
// engine. The section's local folders and uploaded builds sit in front of them.
func runnerFor(region string, cfg Config, projects linked.Projects, table func(context.Context) (register.Table, error), vault linked.Vault, domains deploy.Domains) (run.Runner, run.Confirmer, error) {
	forges, err := drivers.Registry(cfg.Forges)
	if err != nil {
		return nil, nil, err
	}
	pinned, err := drivers.HostKeys(cfg.Forges)
	if err != nil {
		return nil, nil, err
	}
	// SSH WHERE A DEPLOY KEY IS, HTTPS WHERE NONE IS: a deploy key is an ssh key, and a
	// repository without one is read anonymously or not at all. Both addresses, and the
	// ssh account, are the forge's; an ssh host is pinned.
	clone := deploy.NewClone(linked.Credentials{Projects: projects, Vault: vault},
		deploy.WithAddress(deploy.Forged(forges)),
		deploy.WithHostKeys(func(host string) []gossh.PublicKey { return pinned[host] }))

	var (
		checkout deploy.Checkout = clone
		confirm  run.Confirmer   = clone
		engine   artifact.Engine
	)
	// A NODE THAT CANNOT RUN A CONTAINER CANNOT BUILD CODE, and it says so on the push
	// rather than starting and refusing every one. It still deploys configuration,
	// which is most of what a small cloud does.
	if cfg.Container.Any() {
		daemon, err := containers.Open(cfg.Container)
		if err != nil {
			return nil, nil, err
		}
		if engine, err = artifact.NewEngine(daemon); err != nil {
			return nil, nil, err
		}
	}
	if len(cfg.Local) > 0 {
		f, err := folders.New(cfg.Local)
		if err != nil {
			return nil, nil, err
		}
		checkout, confirm = f.Checkout(checkout), f.Confirm(confirm)
	}
	if cfg.Uploads != "" {
		held, err := uploads.Open(cfg.Uploads)
		if err != nil {
			return nil, nil, fmt.Errorf("build: uploads: %w", err)
		}
		checkout, confirm, engine = held.Checkout(checkout), held.Confirm(confirm), held.Engine(engine)
	}

	// THE CLOUD HAS THE ONE REGION ITS NODES SERVE, so a deploy places everything there.
	regions := schema.WithNoRegions()
	if region != "" {
		regions = schema.WithRegion(region)
	}
	options := []deploy.Option{
		deploy.WithProjects(projects),
		deploy.WithCheckout(checkout),
		// THE ONLY WRITE PATH TO THE REGISTER: a deploy is what a push became.
		deploy.WithRegister(func(ctx context.Context) (deploy.Register, error) { return table(ctx) }),
		deploy.WithCloudFqdn(cfg.CloudFqdn),
		deploy.WithRegions(regions),
		deploy.WithDomains(domains),
	}
	if engine != nil {
		builder, err := artifact.New(
			artifact.WithRegister(tableReader{table}),
			artifact.WithProjects(projects),
			artifact.WithCheckout(checkout),
			artifact.WithEngine(engine),
			// A source's clone URL is keyed by the forge that serves it, the same
			// function every push's repository key came out of.
			artifact.WithKeys(forges),
			artifact.WithSecrets(vault),
		)
		if err != nil {
			return nil, nil, err
		}
		options = append(options, deploy.WithBuilder(builder))
	}
	runner, err := deploy.New(options...)
	if err != nil {
		return nil, nil, err
	}
	return runner, confirm, nil
}

// trusted proves every zone: a cloud whose operator serves declared zones unproven.
type trusted struct{}

func (trusted) Prove(context.Context, string, string) error { return nil }
func (trusted) Close() error                                { return nil }

// prover is a deploy's domain proof, closed with the backend.
type prover interface {
	deploy.Domains
	io.Closer
}

// provers is what a deploy proves a zone with: nothing, in a cloud that trusts its
// zones, else the cm service, beside it or wherever discovery finds one.
func provers(h service.Host, cfg Config) (prover, error) {
	if cfg.Domains.Trusted {
		return trusted{}, nil
	}
	return cm.NewDomains(h.Peer().Host(),
		func() []libp2ppeer.ID {
			if dir, ok := service.Get[service.Directory](h); ok {
				return dir.Peers(cm.Name)
			}
			return nil
		},
		func() *cm.Service {
			local, _ := service.Get[*cm.Service](h)
			return local
		})
}

// tableReader is the register's table as a code build reads the configuration it
// builds against, reached per call.
type tableReader struct {
	table func(context.Context) (register.Table, error)
}

func (r tableReader) Environments(ctx context.Context, project string) ([]string, error) {
	t, err := r.table(ctx)
	if err != nil {
		return nil, err
	}
	return t.Environments(ctx, project)
}

func (r tableReader) Resolve(ctx context.Context, project, environment string) (pkgregister.Pointer, error) {
	t, err := r.table(ctx)
	if err != nil {
		return pkgregister.Pointer{}, err
	}
	return t.Resolve(ctx, project, environment)
}

func (r tableReader) Object(ctx context.Context, id pkgregister.ObjectID) ([]byte, error) {
	t, err := r.table(ctx)
	if err != nil {
		return nil, err
	}
	return t.Object(ctx, id)
}
