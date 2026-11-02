package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/pkg/queue"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/services/build"
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
	// DefaultReconcileEvery is how often the open index is walked for records
	// whose queue item was lost.
	DefaultReconcileEvery = 5 * time.Minute
	// Admission control on stranger-authored input: every Enqueue argument comes from
	// an unauthenticated POST to the frontend.
	DefaultMaxQueueDepth  = 10_000
	DefaultMaxOpenPerRepo = 32
	// DefaultMaxMessageBytes bounds one request message. p2p/rpc deliberately
	// does not bound one and says "every v2 service is expected to make that
	// call"; this is build's.
	DefaultMaxMessageBytes = 64 << 10
	// DefaultMaxLogBytes bounds one attempt's log: the build script deciding how much
	// to print is a stranger's. Past it the log says it was truncated and the build
	// carries on.
	DefaultMaxLogBytes = 16 << 20
	// DefaultPublishTimeout bounds handing one finished log to the asset store.
	// It is spent inside ClaimMargin, before the ack that is this service's
	// commit point, which is why withDefaults refuses one that does not fit.
	DefaultPublishTimeout = 10 * time.Second
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

	// ReconcileEvery is how often the open index is reconciled against the
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

	// LogDir is where a running build's log lives; empty means defaultLogDir(), a
	// scratch location since a log that matters reached the asset store. The service
	// owns it: livelog.OwnDir refuses another's directory and tightens a
	// world-writable one, since file names derive from public values. A tmp sweep is
	// harmless because each log is an inode its writer holds and readers pread.
	LogDir string `yaml:"logDir"`

	// MaxLogBytes bounds one attempt's log. Zero means DefaultMaxLogBytes.
	MaxLogBytes int64 `yaml:"maxLogBytes"`

	// PublishTimeout bounds the handoff of a finished log to the asset store.
	// Zero means DefaultPublishTimeout.
	PublishTimeout time.Duration `yaml:"publishTimeout"`
}

// defaultLogDir is the scratch log directory, per uid so a shared /tmp name cannot
// be created first by another account (which livelog.OwnDir would then refuse).
// Two nodes under one account share it safely: names are (job, attempt). On
// windows Getuid is -1, harmless since the temp dir is per user.
func defaultLogDir() string {
	return filepath.Join(os.TempDir(), "tau-build-logs-"+strconv.Itoa(os.Getuid()))
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
		{"maxLogBytes", c.MaxLogBytes},
	} {
		if n.value < 0 {
			return c, fmt.Errorf("build: %s must not be negative", n.name)
		}
	}

	if c.BuildTimeout == 0 {
		c.BuildTimeout = DefaultBuildTimeout
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
	if c.MaxQueueDepth == 0 {
		c.MaxQueueDepth = DefaultMaxQueueDepth
	}
	if c.MaxOpenPerRepo == 0 {
		c.MaxOpenPerRepo = DefaultMaxOpenPerRepo
	}
	if c.MaxMessageBytes == 0 {
		c.MaxMessageBytes = DefaultMaxMessageBytes
	}
	if c.MaxLogBytes == 0 {
		c.MaxLogBytes = DefaultMaxLogBytes
	}
	if c.PublishTimeout == 0 {
		c.PublishTimeout = DefaultPublishTimeout
	}
	if c.LogDir == "" {
		c.LogDir = defaultLogDir()
	}

	// The publish is spent out of ClaimMargin (ack, ship the log, write the terminal
	// record), so it must fit inside it.
	if c.PublishTimeout >= c.ClaimMargin {
		return c, fmt.Errorf(
			"build: publishTimeout (%s) must be shorter than claimMargin (%s), which is what the "+
				"publish and the acknowledgement share",
			c.PublishTimeout, c.ClaimMargin)
	}

	c.Queue.Name = build.QueueDomain

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

// Store is pkg/queue's Store, method for method, so the claim and the job record
// are written by one writer against one log and can disagree only in the
// direction a redelivery repairs.
//
//   - Get: every single-job read is by an id this service derived or was handed.
//   - Keys: the only scan; the key grammar carries every listing (store.go).
//   - Batch: every multi-key transition is one entry. No Set or Delete, so half a
//     transition cannot be written.
//   - Barrier: a new leader may hold committed but unapplied entries; Enqueue
//     without one could create a duplicate job.
//   - IsLeader: a cheap pre-check; Batch fails the same way on a follower.
//
// Deliberately absent: Close (the composition root owns the cluster) and Leader
// and the membership methods, so "a follower answers Unavailable" is the only
// design and forwarding, placement and admission stay with the cluster service.
// No kvdb: build's central fact is a mutual exclusion, which a CRDT cannot
// express.
type Store interface {
	Get(key string) ([]byte, bool)
	Keys(prefix string) []string
	Batch(ops []raft.BatchOp, timeout time.Duration) error
	Barrier(timeout time.Duration) error
	IsLeader() bool
}

// One injected value is both the queue's store and the job records', held by
// the compiler rather than by a comment. raft.Cluster and *raft.MockCluster
// both satisfy it.
var (
	_ queue.Store = (Store)(nil)
	_ Store       = (raft.Cluster)(nil)
	_ Store       = (*raft.MockCluster)(nil)
)

// Repositories asks the register's project table which project a pushed
// repository belongs to; an unregistered repository is refused before anything
// is queued.
type Repositories interface {
	// Project returns the id of the project a repository belongs to, or an
	// error when this cloud has no registered repository under that key.
	Project(ctx context.Context, repositoryKey string) (projectID string, err error)
}

// Confirmer asks a repository whether it holds ref at commit. A nil error is
// yes; ErrNotAtCommit is no; anything else is that it could not be asked.
type Confirmer interface {
	Confirm(ctx context.Context, repositoryKey, ref, commit string) error
}

// ErrNotAtCommit is a repository that does not hold the pushed ref at the
// pushed commit.
var ErrNotAtCommit = errors.New("build: the repository does not hold that ref at that commit")

// Work is everything a run is given, derived here rather than carried across the
// frontend/backend boundary.
type Work struct {
	// JobID is the record this run belongs to. What the run produces is
	// recorded on it, at this attempt.
	JobID string
	// RepositoryKey is the canonical clone identity (pkg/git/repokey).
	RepositoryKey string
	// Ref is the pushed git ref. It already passed the frontend's
	// branch-eligibility policy; this service does not re-filter it.
	Ref string
	// CommitSHA is the pushed head — the "after" of the diff.
	CommitSHA string
	// Before is the last commit this service finished for this (repository, ref), from
	// its own history, never the webhook. Empty means no prior commit: a full build.
	Before string
	// ProjectID is the project the repository belongs to, resolved from auth at
	// enqueue time and stored on the record.
	ProjectID string
	// Attempt is which delivery this is, counting from one.
	Attempt uint32
}

// Output is where a run puts what it produces, and is the log's writer. Nothing
// here reaches a store: the log goes to a file this node owns
// (services/build/livelog) and then the asset store, and the job record hears
// about it once, when the job settles. The log is readable and durable once
// Write returns.
type Output interface {
	io.Writer

	// Asset hands over one built artifact as bytes, read to EOF. The key composed from
	// deployment and resource is this service's grammar, so a worker arrives at the
	// same one.
	Asset(ctx context.Context, environment, resourceID string, content io.Reader) error
}

// Assets is the byte store a build's output goes to (the storage service). A node
// without one still builds, but its logs and artifacts die with the run. An
// implementation owes:
//
//   - content addressing: the same bytes, the same id
//   - durability on another node before it returns
//   - one-pass streaming with no length known in advance
//   - safety under concurrent calls
//   - returning when ctx ends, which is a slice of the claim's margin
//   - no identity: a cid is an address, and authorization is the frontend's
//
// Nothing unreferences an asset yet; that becomes a retention question once
// terminal records are swept.
type Assets interface {
	// Put stores content and returns the content id it is addressed by.
	Put(ctx context.Context, content io.Reader) (cid string, err error)
}

// Runner executes one job. A Service constructed without one serves and
// replicates but claims nothing — which is what a node that holds the domain's
// state but is not meant to run CI wants.
type Runner interface {
	// Run executes one job and must return when ctx ends (Cancel, the claim deadline,
	// Close). A run cut short by shutdown is abandoned, so the claim lapses and
	// another node builds it. Leadership loss does not cancel it; the ack catches a
	// stale run before anything is published. An error from Run is terminal: the
	// queue's retry is for a worker that died, not a build that failed.
	Run(ctx context.Context, w Work, out Output) error
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
