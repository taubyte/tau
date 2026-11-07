package primitives

import (
	"context"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/service"
	pkgkvdb "github.com/taubyte/tau/pkg/kvdb"
	"github.com/taubyte/tau/services/cm"
	"github.com/taubyte/tau/services/register"
)

// Service is primitives as an instance runs it.
type Service interface {
	service.Service

	// Local is this service as the compute tier's seam, with no stream.
	Local() *Tier

	// WaitForLeader returns once the group knows a leader, maybe this node: from
	// then a write is taken, or refused with who takes it. The group forms off the
	// request path, so a composer that needs one waits here.
	WaitForLeader(ctx context.Context) error
}

// Config is the operator's: how often the lifecycle pass runs, and what a dead
// letter keeps. Zero is each one's default.
type Config struct {
	Sweep      time.Duration    `yaml:"sweep"`
	DeadLetter DeadLetterConfig `yaml:"dead_letter"`
}

// DeadLetterConfig bounds a dead letter by age and size; see DefaultDeadLetterRetention.
type DeadLetterConfig struct {
	Age  time.Duration `yaml:"age"`
	Size int64         `yaml:"size"`
}

// retentionPoll is how often the record store rereads the cloud's retention.
const retentionPoll = time.Minute

// Start runs primitives on an instance: its group from the cm beside it, its
// records in a replicated database on the peer, its declarations read through the
// register in the instance's region, its clock the instance's, its fires through the
// workloads beside it, and each minute's window handed to the node discovery names.
func (s *server) Start(ctx context.Context, h service.Host) error {
	var cfg Config
	if err := h.Config(&cfg); err != nil {
		return err
	}
	agree, ok := service.Get[cm.Consensus](h)
	if !ok {
		return ErrNoConsensus
	}
	views := register.ViewsOn(h)
	dead := DefaultDeadLetterRetention
	if cfg.DeadLetter.Age != 0 {
		dead.age = cfg.DeadLetter.Age
	}
	if cfg.DeadLetter.Size != 0 {
		dead.size = cfg.DeadLetter.Size
	}
	err := s.assemble(h.Peer().Host(),
		withRegister(views),
		withConsensus(agree.Raft),
		withRecords(func(name string) (kvdb.Instance, error) {
			return pkgkvdb.Open(h.Peer(), Descriptor.Name+"/"+name,
				pkgkvdb.WithLogger(logger),
				pkgkvdb.WithRetentionFrom(func(ctx context.Context) (time.Duration, error) {
					return register.Retention(ctx, views)
				}, retentionPoll))
		}),
		withClock(h.Now),
		withSweep(cfg.Sweep),
		withDeadLetterRetention(dead.age, dead.size),
		withWorkloads(func() (Workloads, bool) { return service.Get[Workloads](h) }))
	if err != nil {
		views.Close()
		return err
	}
	tasking := service.Routed(h, Protocol, Clocks(s.Local()), dialClocks)
	s.task = func(ctx context.Context, since, upto time.Time) error {
		to, err := tasking.Now(ctx)
		if err != nil {
			return err
		}
		return to.CronTask(ctx, since, upto)
	}
	s.owned = append(s.owned, views, tasking)
	return s.serve(ctx)
}

func dialClocks(h host.Host, id libp2ppeer.ID) (Clocks, error) { return NewClient(h, id) }
