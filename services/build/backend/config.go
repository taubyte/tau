package backend

import (
	"context"
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
	// DefaultMaxLogBytes bounds ONE ATTEMPT'S LOG, and it is admission control
	// on a stranger's output the same way MaxQueueDepth is on a stranger's
	// pushes: the build script deciding how much to print is in a repository
	// this cloud was asked to build, not in this cloud. Past it the log says it
	// was truncated and the build carries on. v1 has no bound of any kind: its
	// log is a temp file nothing ever truncates, closes or removes.
	DefaultMaxLogBytes = 16 << 20
	// DefaultPublishTimeout bounds handing one finished log to the asset store.
	// It is spent inside ClaimMargin, before the ack that is this service's
	// commit point, which is why withDefaults refuses one that does not fit.
	DefaultPublishTimeout = 10 * time.Second
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

	// LogDir is where a run's log file lives while the build is running. Zero
	// means defaultLogDir() below — a directory under the OS temp dir, which is
	// a SCRATCH location on purpose: a log that matters is one that reached the
	// asset store, and a node that died mid-build leaves a file behind that
	// nothing will ever come back for. Somewhere the OS already cleans is the
	// honest place for that, and an operator who wants a bigger or faster disk
	// for it names one.
	//
	// WHEREVER IT POINTS, THE SERVICE OWNS IT. output() calls livelog.OwnDir,
	// which refuses a directory belonging to somebody else and tightens one of
	// its own that is writable by anybody — because the path of the file
	// inside it is derived from public values and the open truncates. An
	// operator who names a shared directory here gets a refusal rather than a
	// service quietly writing where anyone can plant a name first.
	//
	// AND THE SWEEP THAT COMES WITH THAT PLACE COSTS NOTHING, which is a fact
	// about livelog rather than about this directory: a log is the INODE its
	// writer holds, opened once, and every reader preads that same descriptor.
	// A tmp sweeper takes the NAME; the run keeps appending, the watchers keep
	// reading, and the handoff at the end of the build reads the whole file. It
	// was not always so — a reader used to open the path again, so a name
	// removed mid-build lost the entire log with every byte of it still on
	// disk, and the publish said nothing about it — and that is why the two
	// halves are written down together: this line chooses a directory the OS
	// deletes from, and it is only honest while nothing resolves a log's name
	// twice.
	LogDir string `yaml:"logDir"`

	// MaxLogBytes bounds one attempt's log. Zero means DefaultMaxLogBytes.
	MaxLogBytes int64 `yaml:"maxLogBytes"`

	// PublishTimeout bounds the handoff of a finished log to the asset store.
	// Zero means DefaultPublishTimeout.
	PublishTimeout time.Duration `yaml:"publishTimeout"`
}

// defaultLogDir is the scratch directory a node writes its running builds' logs
// into when the operator names none.
//
// IT CARRIES THE UID, AND THAT IS THE CHANGE THIS ROUND MADE. It was
// "tau-build-logs" flat under the OS temp dir, which on linux is one /tmp
// shared by every account on the box — so the name a node needs to own is a
// name every other account can create first, and the service's own MkdirAll
// accepted whatever it found. livelog.OwnDir refuses that now, and a name
// somebody else got to first would refuse a NODE rather than an attack: two tau
// nodes under two accounts on one box, and the second one builds nothing.
// One directory per uid is the smallest thing that makes ownership something
// this process can always have.
//
// It is not per-NODE. Two nodes under one account share the directory and are
// equally entitled to it, and their file names cannot collide: a name is
// (job, attempt), and the queue's exclusion is what says one job has one run.
//
// On windows Getuid answers -1 for everybody, which is harmless there because
// the temp dir is already inside the user's own profile.
func defaultLogDir() string {
	return filepath.Join(os.TempDir(), "tau-build-logs-"+strconv.Itoa(os.Getuid()))
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
		{"publishTimeout", c.PublishTimeout},
	} {
		if d.value < 0 {
			return c, fmt.Errorf("build: %s must not be negative", d.name)
		}
	}
	for _, n := range []struct {
		name  string
		value int64
	}{
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

	// THE PUBLISH IS SPENT OUT OF THE MARGIN, so it has to fit inside it. The
	// run ends at the claim's deadline less ClaimMargin, and what happens in
	// that margin is: ack, ship the log, write the terminal record. A publish
	// allowed to use the whole margin is a terminal record written well after
	// the claim expired — and the deliveries this node no longer holds are
	// exactly the ones the ack in front of it refuses, so the whole of the
	// margin left after the ack is what the last two have to fit in. Refusing
	// the combination here is cheaper than discovering it once per long build.
	if c.PublishTimeout >= c.ClaimMargin {
		return c, fmt.Errorf(
			"build: publishTimeout (%s) must be shorter than claimMargin (%s), which is what the "+
				"publish and the acknowledgement share",
			c.PublishTimeout, c.ClaimMargin)
	}

	c.Queue.Name = build.QueueDomain

	// A LEASE THAT DOES NOT OUTLAST A BUILD IS A SECOND CLAIMER. The queue's
	// visibility timeout is what makes a claim exclusive; if a run can reach it,
	// the queue's own repair() puts the item back while the run is still going
	// and this very loop's next cycle claims it again — one node building one
	// job twice. v1's 60s re-announce against a one-hour build timeout is that
	// bug with different constants.
	//
	// pkg/queue has no Extend, so a lease cannot be renewed mid-run. That is
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
// pkg/queue's Store, method for method, deliberately: the fact that decides
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
// ONE FACT, not authv2connect's whole client: naming only this keeps
// deploy_key out of this package's vocabulary until the Runner needs it, which
// is also the security question auth.proto and services/auth/repositories.go
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

// Output is where a run puts what it produces, and IT IS THE LOG'S WRITER: a
// runner pipes a build's output straight into it, so wiring one up is an
// io.Copy rather than a call per line.
//
// NOTHING HERE REACHES A STORE. What a run writes goes to a file this node
// owns (services/build/livelog) and to the asset store; the JOB RECORD hears
// about it once, from the section that settles the job. Its predecessor wrote
// a replicated raft row per log line from outside every section — see
// output.go for what that cost.
//
// The log is DURABLE ENOUGH WHEN Write RETURNS: the bytes are readable by
// anybody following the build, and they survive this process dying. v1 writes
// to a file too (os.CreateTemp, services/monkey/monkey.go) — what it does not
// do is let anything READ that file: it is published to IPFS from a `defer` at
// the end of the run and served by cid afterwards, so a build that killed its
// worker never reached the defer and left a temp file in /tmp that nothing
// names and nothing removes.
type Output interface {
	io.Writer

	// Asset hands over one built artifact — the bytes, not a cid, because
	// where they go is this service's decision and not the runner's. It reads
	// content to EOF.
	//
	// IT TAKES THE DEPLOYMENT AS WELL AS THE RESOURCE, and the key those two
	// compose is this service's grammar rather than the runner's: a worker
	// asking what a resource runs in production has to arrive at the same key
	// the build wrote, and a runner that composed it would be a runner deciding
	// a shape two other packages depend on. A build stanza is per-deployment,
	// so one resource can have a different artifact in preview than in
	// production.
	Asset(ctx context.Context, environment, resourceID string, content io.Reader) error
}

// Assets is the byte store a build's output goes to: v1's hoarder, which in v2
// is the STORAGE service — and that service is not written yet, so this is the
// seam it will be plugged into and there is no implementation of it in this
// tree. A node constructed without one still builds; its logs and artifacts
// live and die with the run, and its jobs carry no cids.
//
// WHAT A REAL IMPLEMENTATION OWES IT, stated here because the interface cannot:
//
//   - CONTENT ADDRESSING. The returned id is derived from the bytes, so the
//     same bytes always yield the same id. That is what makes storing an
//     artifact during a run that later fails harmless, and what makes a retry
//     of an identical build cost nothing.
//   - DURABLE BEFORE IT RETURNS, on a node OTHER than this one. The cid goes
//     into a replicated record that any node may answer a read for, so a Put
//     that has only reached this node's disk is a promise the record cannot
//     keep. This is v1's pushStash — the bytes travel from the node that made
//     them to storage — with the acknowledgement that v1 does not wait for.
//   - STREAMING, in one pass, with no length known in advance. A build log has
//     no size until it ends, and this must never require the caller to hold it
//     in memory to find out.
//   - CONCURRENCY. Many runs on one node call it at once.
//   - THE CONTEXT. A Put that outlasts its deadline must return, because the
//     one this service passes is a slice of the margin it has left before its
//     claim expires — and what comes after the publish is the entry that
//     records the outcome, on a node that has to still be the leader to write
//     it. A Put that hangs is a job left Claimed with no queue item until
//     reconcile puts it back.
//   - NO IDENTITY. It is handed bytes and nothing else: an asset store that
//     took a job id would be a second place authorization could be decided,
//     and authorization is the frontend's, on the caller's write access to the
//     job's repository (frontend/authz.go). A cid is an address, not a
//     credential — which is exactly what v1's GET /logs/{cid} treats it as.
//
// What it does NOT owe, said out loud because the absence is a debt rather than
// a decision: any way to UNREFERENCE an asset. Nothing in this service deletes
// an archived job today, so nothing deletes what one points at either — v1's
// defect, carried, and it becomes a retention question the moment terminal
// records start being swept (store.go's terminalIndexKey).
type Assets interface {
	// Put stores content and returns the content id it is addressed by.
	Put(ctx context.Context, content io.Reader) (cid string, err error)
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
	Run(ctx context.Context, w Work, out Output) error
}

// Action names what a caller is trying to do, for the Authorizer.
type Action int

const (
	ActEnqueue Action = iota
	ActRead
	ActCancel
	ActRetry

	// ActArtifact is a read about a PROJECT rather than about a job: what one
	// resource of one deployment currently runs.
	//
	// IT IS ITS OWN ACTION BECAUSE THE SUBJECT MEANS SOMETHING ELSE. Every
	// other action's subject is a job id, and an authorizer told "read" with a
	// project in the subject would either check it against jobs and refuse
	// everything, or stop checking and allow everything. A new question gets a
	// new name.
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
