package backend

import (
	"context"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/pkg/v2/queue"
	"github.com/taubyte/tau/services/build"
)

// Defaults. Every one of them is a field on Config set through New, never a
// mutable package var two Services in one process would share — which is what
// services/patrick/service.go's BootstrapTime, DefaultReAnnounceJobTime and
// MaxReAnnounceJobs are, and which its own types.go already records as a fixed
// race.
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
	// DefaultMaxQueueDepth and DefaultMaxOpenPerRepo are admission control on
	// input a STRANGER authored: every Enqueue argument originates in an
	// unauthenticated POST to the frontend. v1 has no bound of either kind.
	DefaultMaxQueueDepth  = 10_000
	DefaultMaxOpenPerRepo = 32
	// DefaultMaxMessageBytes bounds one request message. p2p/rpc deliberately
	// does not bound one and says "every v2 service is expected to make that
	// call"; this is build's.
	DefaultMaxMessageBytes = 64 << 10
)

// Config is what an operator declared. A plain value: no live objects, no
// setters, and it round-trips through yaml.
type Config struct {
	// Queue configures the work queue. Name is FORCED to build.QueueDomain by
	// New — the queue's name is the consistency domain build.proto fixes by
	// literal, not something a deployment renames, and two nodes of one cloud
	// disagreeing about it is two queues where there should be one.
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
}

// withDefaults fills in the zero values and refuses the combinations that have
// no honest reading.
func (c Config) withDefaults() (Config, error) {
	for _, d := range []struct {
		name  string
		value time.Duration
	}{
		{"buildTimeout", c.BuildTimeout},
		{"claimMargin", c.ClaimMargin},
		{"claimInterval", c.ClaimInterval},
		{"reconcileEvery", c.ReconcileEvery},
	} {
		if d.value < 0 {
			return c, fmt.Errorf("build: %s must not be negative", d.name)
		}
	}
	for _, n := range []struct {
		name  string
		value int
	}{
		{"maxQueueDepth", c.MaxQueueDepth},
		{"maxOpenPerRepo", c.MaxOpenPerRepo},
		{"maxMessageBytes", c.MaxMessageBytes},
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

	c.Queue.Name = build.QueueDomain

	// A LEASE THAT DOES NOT OUTLAST A BUILD IS A SECOND CLAIMER. The queue's
	// visibility timeout is what makes a claim exclusive; if a run can reach it,
	// the queue's own repair() puts the item back while the run is still going
	// and this very loop's next cycle claims it again — one node building one
	// job twice. v1's 60s re-announce against a one-hour build timeout is that
	// bug with different constants.
	//
	// pkg/v2/queue has no Extend, so a lease cannot be renewed mid-run. That is
	// worked around rather than added to a shared package at 98% coverage:
	// the lease simply has to be longer than the build, and the default derives
	// it so an operator who set neither cannot get it wrong.
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

// Store is the replicated store this service persists into. It is
// pkg/v2/queue's Store, method for method, deliberately: the fact that decides
// a claim ("this item is claimed") and the fact that records it ("this job's
// status is Claimed") are then written by ONE writer against ONE log, in a
// fixed order, and can disagree only in the direction a redelivery repairs.
// Two stores — a raft queue plus a CRDT kvdb — is exactly the seam v1's /jobs/
// record and its separate /assigned/ row fell through.
//
// Why each method is here:
//
//   - Get: every single-job read is by an id this service derived or was
//     handed. There is no query, so there is no query method.
//   - Keys: the only scan. Every listing — a project's jobs, the open index,
//     the retention index — is a prefix listing, and unlike auth's ListRegEx
//     there is no anchored-pattern option, so the KEY GRAMMAR carries the whole
//     burden. See store.go.
//   - Batch: every multi-key transition is one replicated entry. There is
//     deliberately no Set and no Delete even though raft.Cluster has both:
//     offering a single-key write is offering a way to write half a
//     transition, which is v1's orphaned /jobs/<id> with no queue push. The
//     omission IS the atomicity guarantee.
//   - Barrier: load-bearing, not defensive. A node that has just become leader
//     can hold committed entries it has not applied, so an Enqueue that read
//     the job table without one would create a second job for a push it
//     already has.
//   - IsLeader: the write path must refuse on a follower rather than diverge,
//     and the claim loop must not run there at all. It is a cheap pre-check,
//     not the authority — Batch fails the same way.
//
// DELIBERATELY ABSENT, each with a reason:
//
//   - No Close. The composition root owns the cluster's lifetime, and a service
//     that cannot name Close cannot close a consensus group the services beside
//     it are replicating into (service-conventions.md §2e).
//   - No Leader, Members, AddVoter, RemoveServer, TransferLeadership, State,
//     WaitForLeader, Namespace or Apply. All nine are on raft.Cluster; all nine
//     are refused. This is the load-bearing omission: a backend that could NAME
//     Leader() would grow forwarding, then peer selection, then admission, then
//     placement — the four things service-conventions.md §4 reserves for the
//     cluster service, arrived at one reasonable-looking method at a time. Not
//     naming it is what makes "a follower answers Unavailable" the only
//     available design, and that in turn deletes forwarding, the leader cache
//     and the staleness question together.
//
// And no kvdb anywhere. auth's data is per-key owned, so a CRDT with
// last-writer-wins is honest for it. Build's central fact is a MUTUAL
// EXCLUSION — which node may run this job — and a CRDT cannot express one;
// root AGENTS.md rule 4 also says a read-then-write guard over kvdb guarantees
// nothing, where under raft, with one writer and a barrier, it is correctness.
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

// Repositories is the one question this service asks auth.
//
// ONE FACT, not authv1connect's whole client: naming only this keeps
// deploy_key out of this package's vocabulary until the Runner needs it, which
// is also the security question auth.proto and services/v2/auth/repositories.go
// left open on purpose — "because nothing named the consumer". Build is that
// consumer, and widening this interface is the change that answers it.
//
// It doubles as admission control on stranger-authored input: a push for a
// repository nobody registered with this cloud is refused before anything is
// queued.
type Repositories interface {
	// Project returns the id of the project a repository belongs to, or an
	// error when this cloud has no registered repository under that key.
	Project(ctx context.Context, repositoryKey string) (projectID string, err error)
}

// Work is everything a run is given. It is derived here, never carried across
// the frontend/backend boundary: build.proto's PushEvent is three fields, and
// ProjectID and Before are read from this service's own state.
type Work struct {
	// JobID is the record this run belongs to. Every Sink row is written under
	// it, at this attempt.
	JobID string
	// RepositoryKey is the canonical clone identity (pkg/git/repokey).
	RepositoryKey string
	// Ref is the pushed git ref. It already passed the frontend's
	// branch-eligibility policy; this service does not re-filter it.
	Ref string
	// CommitSHA is the pushed head — the "after" of the diff.
	CommitSHA string
	// Before is the last commit THIS service finished for this exact
	// (repository, ref), read from its own history and never from the webhook.
	// Empty means there is no prior commit to diff against, which a Runner must
	// treat as a full build — exactly what a GitHub payload's all-zero `before`
	// signals for a newly created branch.
	Before string
	// ProjectID is the project the repository belongs to, resolved from auth at
	// enqueue time and stored on the record.
	ProjectID string
	// Attempt is which delivery this is, counting from one.
	Attempt uint32
}

// Sink is where a run puts what it produced.
//
// ONE ENTRY, ONE KEY — never a map re-serialized on each write (root AGENTS.md
// rule 1). Durable when called, so a crashed build still has the logs it had
// got to; v1's log file only reached IPFS after the run returned, so a build
// that killed its worker left nothing to read.
type Sink interface {
	// Log records one log entry's cid, at the time the entry was made.
	Log(ctx context.Context, at time.Time, cid string) error
	// Asset records one built resource's artifact cid.
	Asset(ctx context.Context, resourceID, cid string) error
}

// Runner executes one job. A Service constructed without one serves and
// replicates but claims nothing — which is what a node that holds the domain's
// state but is not meant to run CI wants.
type Runner interface {
	// Run executes one job. It MUST return when ctx ends: this service cancels
	// ctx on Cancel, on the claim's deadline, and on Close — where returning
	// promptly is what keeps a restart from waiting out an hour-long build. A
	// run the shutdown cut short is ABANDONED rather than failed: the claim
	// lapses and another node builds it (see Service.Close).
	//
	// Losing leadership does not cancel it: nothing watches for that, and there
	// is nothing to gain by stopping early. The ack is where a run that no
	// longer speaks for the group is caught, and it is caught before anything
	// this run produced is published.
	//
	// AN ERROR FROM Run IS TERMINAL — the ack, then status Failed, and both in
	// one section (runner.go's settle). The queue's retry exists for a worker
	// that DIED, not for a build that failed;
	// retrying a compile error five times is waste, and v1's own
	// MaxJobAttempts=2 counted only REPORTED failures, so a crash was free and
	// a job that killed its worker retried forever.
	Run(ctx context.Context, w Work, sink Sink) error
}

// Action names what a caller is trying to do, for the Authorizer.
type Action int

const (
	ActEnqueue Action = iota
	ActRead
	ActCancel
	ActRetry
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
	default:
		return fmt.Sprintf("action(%d)", int(a))
	}
}

// Authorizer decides whether an authenticated peer may take an action on a
// subject — a repository key for ActEnqueue, a job or project id otherwise.
type Authorizer interface {
	Allow(ctx context.Context, caller peer.ID, act Action, subject string) error
}

// AllowAnyPeer is the documented OSS default: a peer the transport
// authenticated may call. See authz.go for the membership gap that sits
// under it, and why this package does not close it.
func AllowAnyPeer() Authorizer { return allowAnyPeer{} }

type allowAnyPeer struct{}

func (allowAnyPeer) Allow(context.Context, peer.ID, Action, string) error { return nil }

// AllowPeers narrows a deployment that knows its callers' peer ids to exactly
// them. Nothing in-tree wires it yet; it exists so that narrowing is a
// constructor argument rather than a new mechanism invented later, and so that
// the default above is a CHOICE with an alternative rather than the only shape.
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
