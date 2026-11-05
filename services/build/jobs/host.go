package jobs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	logging "github.com/ipfs/go-log/v2"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/service"
	pkgkvdb "github.com/taubyte/tau/pkg/kvdb"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/services/build"
	"github.com/taubyte/tau/services/build/linked"
	"github.com/taubyte/tau/services/cm"
	"github.com/taubyte/tau/services/register"
	"github.com/taubyte/tau/services/storage"
)

// Service is build's jobs as an instance runs them.
type Service interface {
	service.Service

	// Built is what last built a deployment's resource: the cid of its artifact.
	Built(project, environment, resource string) (string, error)

	// WaitForLeader returns once the queue's group knows a leader, maybe this node:
	// from then a job is read or refused with who reads it. The group forms off the
	// request path, so a composer that needs one waits here.
	WaitForLeader(ctx context.Context) error

	// Local is these jobs as build's own routes ask them, in-process.
	Local() Local
}

// New is build's jobs, unstarted. Start builds it from its instance.
func New() Service { return &hosted{} }

// ErrNotStarted is jobs asked for what only started jobs have.
var ErrNotStarted = errors.New("build: not started")

// ErrNoConsensus is an instance running no cm, whose consensus the queue is a group
// of.
var ErrNoConsensus = errors.New("build: no cm runs beside it, so the queue has no group to live in")

// retentionPoll is how often the job records reread the cloud's retention.
const retentionPoll = time.Minute

// hosted is jobs as an instance runs them: the server Start built, and what
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

// Start runs jobs on an instance: its queue a raft group of the cm beside it, its job records a replicated database on the peer, its logs in the storage
// service and its projects in the register, both wherever discovery finds them.
func (b *hosted) Start(ctx context.Context, h service.Host) error {
	if b.server != nil {
		return errors.New("build: already started")
	}
	var cfg Config
	if err := h.Config(&cfg); err != nil {
		return err
	}
	agree, ok := service.Get[cm.Consensus](h)
	if !ok {
		return ErrNoConsensus
	}
	cluster, err := agree.Raft(build.Build.Name)
	if err != nil {
		return err
	}
	b.also = append(b.also, closer(func() error { return cluster.Close() }))

	s, also, err := onInstance(h, cfg, cluster)
	b.also = append(b.also, also...)
	if err != nil {
		return err
	}
	b.server, b.queue = s, cluster
	return s.serve(ctx)
}

// onInstance is a server over h's records, register and storage, its queue in store,
// and what it opened for them, closed after it.
func onInstance(h service.Host, cfg Config, store Store, more ...option) (*server, []io.Closer, error) {
	records, also, err := recordsOn(h)
	if err != nil {
		return nil, also, fmt.Errorf("build: opening the job records: %w", err)
	}

	table := register.TableOn(h)
	assets := storage.StoreOn(h)
	also = append(also, table, assets)

	s, err := newServer(cfg, append([]option{
		withHost(h.Peer().Host()),
		withStore(store),
		withRecords(records),
		// WHO A PUSH BELONGS TO is the register's project table; a repository no
		// project links is refused before anything is queued.
		withRepositories(linked.Projects{Table: table.Now}),
		withAssets(assets),
		withClock(h.Now),
	}, more...)...)
	return s, also, err
}

// Records is the replicated database build keeps its jobs in, on every node that
// builds or reads what was built.
var Records = build.Build.Name + "/jobs"

// recordsOn is h's replica of the job records: the instance's own when it shares
// its databases, so a node holds one whoever reads them, else one opened here and
// closed with what opened it.
func recordsOn(h service.Host) (kvdb.Instance, []io.Closer, error) {
	if dbs, ok := service.Get[service.Databases](h); ok {
		db, err := dbs.Database(Records)
		return db, nil, err
	}
	fleet := register.ViewsOn(h)
	also := []io.Closer{fleet}
	db, err := pkgkvdb.Open(h.Peer(), Records,
		pkgkvdb.WithLogger(logging.Logger("tau.build")),
		pkgkvdb.WithRetentionFrom(func(ctx context.Context) (time.Duration, error) {
			return register.Retention(ctx, fleet)
		}, retentionPoll))
	if err != nil {
		return nil, also, err
	}
	return db, append(also, closer(func() error { db.Close(); return nil })), nil
}

// Live is the artifact the newest success built for a deployment's resource, read
// from db, a node's replica of the job records, with no build node asked. Nothing
// built is NotFound.
func Live(ctx context.Context, db kvdb.Instance, project, environment, resource string) (string, error) {
	cid, _, ok, err := (&journal{db: db}).live(ctx, project, environment, resource)
	if err != nil {
		return "", err
	}
	if !ok {
		// Nothing built is NotFound, which differs from needing no build; the caller
		// knows which it asked about.
		return "", errNotFound("build: nothing has been built for %s/%s's %s", project, environment, resource)
	}
	if cid == "" {
		return "", errInternal("build: %s/%s's %s names an empty artifact", project, environment, resource)
	}
	return cid, nil
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
