package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	logging "github.com/ipfs/go-log/v2"

	"github.com/taubyte/tau/core/service"
	pkgkvdb "github.com/taubyte/tau/pkg/kvdb"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/services/build"
	"github.com/taubyte/tau/services/build/linked"
	"github.com/taubyte/tau/services/consensus"
	"github.com/taubyte/tau/services/register"
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

	// Local is this backend as build's own routes ask it, in-process.
	Local() Local
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
	cluster, err := agree.Raft(build.Build.Name)
	if err != nil {
		return err
	}
	b.also = append(b.also, closer(func() error { return cluster.Close() }))

	fleet := register.ViewsOn(h)
	b.also = append(b.also, fleet)
	records, err := pkgkvdb.Open(h.Peer(), build.Build.Name+"/jobs",
		pkgkvdb.WithLogger(logging.Logger("tau.build")),
		pkgkvdb.WithRetentionFrom(func(ctx context.Context) (time.Duration, error) {
			return register.Retention(ctx, fleet)
		}, retentionPoll))
	if err != nil {
		return fmt.Errorf("build: opening the job records: %w", err)
	}
	b.also = append(b.also, closer(func() error { records.Close(); return nil }))

	table := register.TableOn(h)
	assets := storage.StoreOn(h)
	b.also = append(b.also, table, assets)

	s, err := newServer(cfg,
		withHost(h.Peer().Host()),
		withStore(cluster),
		withRecords(records),
		// WHO A PUSH BELONGS TO is the register's project table; a repository no
		// project links is refused before anything is queued.
		withRepositories(linked.Projects{Table: table.Now}),
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
