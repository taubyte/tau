package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/pkg/queue"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/services/plm"
)

// Defaults, each a Config field set through New rather than a mutable package
// var two Services in one process would share.
const (
	// DefaultBuildTimeout is v1's DefaultMaxTime.
	DefaultBuildTimeout = time.Hour
	// DefaultClaimMargin is the part of a claim's lease reserved for this
	// service's own bookkeeping after Run returns: writing the terminal record
	// and acknowledging. The run never gets it.
	DefaultClaimMargin = 30 * time.Second
	// DefaultClaimInterval is how often the claim loop cycles when it found
	// nothing to do.
	DefaultClaimInterval = time.Second
	// DefaultReconcileEvery is how often the open rows are walked for jobs whose
	// queue item was lost.
	DefaultReconcileEvery = 5 * time.Minute
	// Admission control on stranger-authored input: every Enqueue argument comes from
	// an unauthenticated POST to ingress.
	DefaultMaxQueueDepth  = 10_000
	DefaultMaxOpenPerRepo = 32
	// DefaultMaxMessageBytes bounds one request message. p2p/rpc deliberately
	// does not bound one and says "every v2 service is expected to make that
	// call"; this is build's.
	DefaultMaxMessageBytes = 64 << 10
	// DefaultPublishTimeout bounds handing one finished log to the asset store.
	// It is spent inside ClaimMargin, before the ack that is this service's
	// commit point, which is why withDefaults refuses one that does not fit.
	DefaultPublishTimeout = 10 * time.Second
	// DefaultRecordReads and DefaultRecordReadInterval are how long a builder waits for
	// a claimed job's record to reach its replica before it fails the job (claims
	// arrive through raft, records through kvdb, and kvdb can lag).
	DefaultRecordReads        = 5
	DefaultRecordReadInterval = 200 * time.Millisecond
	// DefaultJobRetention is how long a finished job is kept.
	DefaultJobRetention = 30 * 24 * time.Hour
)

// Config is what an operator declared. A plain value: no live objects, no
// setters, and it round-trips through yaml.
type Config struct {
	// Queue configures the work queue. Name is forced to build.QueueDomain by New:
	// two nodes disagreeing would be two queues.
	Queue queue.Config `yaml:"queue"`

	// BuildTimeout is the longest a single run may take. Zero means
	// DefaultBuildTimeout.
	BuildTimeout time.Duration `yaml:"buildTimeout"`

	// ClaimMargin is held back from every claim's lease for this service's own
	// bookkeeping. Zero means DefaultClaimMargin.
	ClaimMargin time.Duration `yaml:"claimMargin"`

	// ClaimInterval is how often the claim loop looks for work. Zero means
	// DefaultClaimInterval.
	ClaimInterval time.Duration `yaml:"claimInterval"`

	// ReconcileEvery is how often the open rows are reconciled against the
	// queue. Zero means DefaultReconcileEvery.
	ReconcileEvery time.Duration `yaml:"reconcileEvery"`

	// MaxQueueDepth refuses an Enqueue that would take the queue past it.
	// Zero means DefaultMaxQueueDepth.
	MaxQueueDepth int `yaml:"maxQueueDepth"`

	// MaxOpenPerRepo refuses an Enqueue for a repository that already has this
	// many jobs outstanding. Zero means DefaultMaxOpenPerRepo.
	MaxOpenPerRepo int `yaml:"maxOpenPerRepo"`

	// MaxMessageBytes bounds one decoded request message. Zero means
	// DefaultMaxMessageBytes.
	MaxMessageBytes int `yaml:"maxMessageBytes"`

	// PublishTimeout bounds the handoff of a finished log to the asset store.
	// Zero means DefaultPublishTimeout.
	PublishTimeout time.Duration `yaml:"publishTimeout"`

	// RecordReads is how many times a builder reads a claimed job's record before
	// failing the job as one whose record never reached it, RecordReadInterval
	// apart. A user retries such a job. Zero means the defaults.
	RecordReads        int           `yaml:"recordReads"`
	RecordReadInterval time.Duration `yaml:"recordReadInterval"`

	// JobRetention is how long a finished job is kept before reconcile erases it: its
	// events, its log's name and its rows. A job whose build a deployment still runs
	// is kept however old, since what runs is resolved through it. Zero means
	// DefaultJobRetention.
	JobRetention time.Duration `yaml:"jobRetention"`
}

// withDefaults fills in the zero values and refuses the combinations that have
// no honest reading.
func (c Config) withDefaults() (Config, error) {
	type namedDuration struct {
		name  string
		value time.Duration
	}
	for _, d := range []namedDuration{
		{"buildTimeout", c.BuildTimeout},
		{"claimMargin", c.ClaimMargin},
		{"claimInterval", c.ClaimInterval},
		{"reconcileEvery", c.ReconcileEvery},
		{"publishTimeout", c.PublishTimeout},
		{"jobRetention", c.JobRetention},
		{"recordReadInterval", c.RecordReadInterval},
	} {
		if d.value < 0 {
			return c, fmt.Errorf("build: %s must not be negative", d.name)
		}
	}
	type namedCount struct {
		name  string
		value int64
	}
	for _, n := range []namedCount{
		{"maxQueueDepth", int64(c.MaxQueueDepth)},
		{"maxOpenPerRepo", int64(c.MaxOpenPerRepo)},
		{"maxMessageBytes", int64(c.MaxMessageBytes)},
		{"recordReads", int64(c.RecordReads)},
	} {
		if n.value < 0 {
			return c, fmt.Errorf("build: %s must not be negative", n.name)
		}
	}

	if c.BuildTimeout == 0 {
		c.BuildTimeout = DefaultBuildTimeout
	}
	if c.RecordReads == 0 {
		c.RecordReads = DefaultRecordReads
	}
	if c.RecordReadInterval == 0 {
		c.RecordReadInterval = DefaultRecordReadInterval
	}
	if c.ClaimMargin == 0 {
		c.ClaimMargin = DefaultClaimMargin
	}
	if c.ClaimInterval == 0 {
		c.ClaimInterval = DefaultClaimInterval
	}
	if c.ReconcileEvery == 0 {
		c.ReconcileEvery = DefaultReconcileEvery
	}
	if c.JobRetention == 0 {
		c.JobRetention = DefaultJobRetention
	}
	if c.MaxQueueDepth == 0 {
		c.MaxQueueDepth = DefaultMaxQueueDepth
	}
	if c.MaxOpenPerRepo == 0 {
		c.MaxOpenPerRepo = DefaultMaxOpenPerRepo
	}
	if c.MaxMessageBytes == 0 {
		c.MaxMessageBytes = DefaultMaxMessageBytes
	}
	if c.PublishTimeout == 0 {
		c.PublishTimeout = DefaultPublishTimeout
	}
	// The publish is spent out of ClaimMargin (ack, ship the log, write the terminal
	// record), so it must fit inside it.
	if c.PublishTimeout >= c.ClaimMargin {
		return c, fmt.Errorf(
			"build: publishTimeout (%s) must be shorter than claimMargin (%s), which is what the "+
				"publish and the acknowledgement share",
			c.PublishTimeout, c.ClaimMargin)
	}

	c.Queue.Name = plm.QueueDomain

	// A lease that does not outlast a build is a second claimer: the queue's repair()
	// would hand the item back mid-run. pkg/queue has no Extend, so the lease must be
	// longer than the build, and the default derives it.
	if c.Queue.VisibilityTimeout == 0 {
		c.Queue.VisibilityTimeout = c.BuildTimeout + c.ClaimMargin
	}
	if c.Queue.VisibilityTimeout <= c.BuildTimeout {
		return c, fmt.Errorf(
			"build: queue.visibilityTimeout (%s) must outlast buildTimeout (%s), or a running build loses its own claim",
			c.Queue.VisibilityTimeout, c.BuildTimeout)
	}

	return c, nil
}

// Store is pkg/queue's Store, method for method: the raft group the work queue
// lives in. Nothing else of this service is in it; a job's events are in kvdb
// (withRecords, events.go), since every write to raft is a replicated log entry.
//
//   - Get, Keys, Batch: the queue's own.
//   - Barrier: a new leader may hold committed but unapplied queue entries; a
//     transition reads behind one.
//   - IsLeader: a cheap pre-check; Batch fails the same way on a follower.
//
// Deliberately absent: Close (the composition root owns the cluster) and Leader
// and the membership methods, so "a follower answers Unavailable" is the only
// design and forwarding, placement and admission stay with the cluster service.
type Store interface {
	Get(key string) ([]byte, bool)
	Keys(prefix string) []string
	Batch(ops []raft.BatchOp, timeout time.Duration) error
	Barrier(timeout time.Duration) error
	IsLeader() bool
}

var (
	_ queue.Store = (Store)(nil)
	_ Store       = (raft.Cluster)(nil)
	_ Store       = (*raft.MockCluster)(nil)
)

// Repositories asks auth's project table which project a pushed
// repository belongs to; an unregistered repository is refused before anything
// is queued.
type Repositories interface {
	// Project returns the id of the project a repository belongs to, or an
	// error when this cloud has no registered repository under that key.
	Project(ctx context.Context, repositoryKey string) (projectID string, err error)
}

// Action names what a caller is trying to do, for the Authorizer.
type Action int

const (
	ActEnqueue Action = iota
	ActRead
	ActCancel
	ActRetry

	// ActArtifact is a read about a project rather than a job: what one resource of
	// one deployment runs. Its own action because its subject is a project id.
	ActArtifact
)

func (a Action) String() string {
	switch a {
	case ActEnqueue:
		return "enqueue"
	case ActRead:
		return "read"
	case ActCancel:
		return "cancel"
	case ActRetry:
		return "retry"
	case ActArtifact:
		return "artifact"
	default:
		return fmt.Sprintf("action(%d)", int(a))
	}
}

// Authorizer decides whether an authenticated peer may take an action on a
// subject — a repository key for ActEnqueue, a job or project id otherwise.
type Authorizer interface {
	Allow(ctx context.Context, caller peer.ID, act Action, subject string) error
}

// AllowAnyPeer is the OSS default: any peer the transport authenticated may call.
func AllowAnyPeer() Authorizer { return allowAnyPeer{} }

type allowAnyPeer struct{}

func (allowAnyPeer) Allow(context.Context, peer.ID, Action, string) error { return nil }

// AllowPeers narrows callers to exactly these peer ids.
func AllowPeers(ids ...peer.ID) Authorizer {
	set := make(map[peer.ID]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return allowPeers(set)
}

type allowPeers map[peer.ID]struct{}

func (a allowPeers) Allow(_ context.Context, caller peer.ID, act Action, _ string) error {
	if _, ok := a[caller]; !ok {
		return errPermissionDenied("peer %s may not %s", caller, act)
	}
	return nil
}
