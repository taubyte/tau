// Package run is what plm hands the thing that runs a job, and what it asks of
// it: shared by plm's jobs and every builder, so neither imports the other.
package run

import (
	"context"
	"errors"
	"io"
	"time"

	builderv2 "github.com/taubyte/tau/pkg/proto/gen/tau/builder/v2"
	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
)

// Confirmer asks a repository whether it holds ref at commit. A nil error is
// yes; ErrNotAtCommit is no; anything else is that it could not be asked.
type Confirmer interface {
	Confirm(ctx context.Context, repositoryKey, ref, commit string) error
}

// ErrNotAtCommit is a repository that does not hold the pushed ref at the
// pushed commit.
var ErrNotAtCommit = errors.New("build: the repository does not hold that ref at that commit")

// Work is everything a run is given, derived by plm's jobs rather than carried
// in from the push.
type Work struct {
	// JobID is the record this run belongs to. What the run produces is
	// recorded on it, at this attempt.
	JobID string
	// RepositoryKey is the canonical clone identity (pkg/git/repokey).
	RepositoryKey string
	// Ref is the pushed git ref. It already passed ingress's branch-eligibility
	// policy; jobs does not re-filter it.
	Ref string
	// CommitSHA is the pushed head — the "after" of the diff.
	CommitSHA string
	// Before is the last commit build finished for this (repository, ref), from
	// its own history, never the webhook. Empty means no prior commit: a full build.
	Before string
	// ProjectID is the project the repository belongs to, resolved at enqueue time
	// and stored on the record.
	ProjectID string
	// Attempt is which delivery this is, counting from one.
	Attempt uint32
	// Environment is the one deployment this run builds. Empty is a plan: the
	// run reads which environments the commit feeds, and each becomes a job of
	// its own.
	Environment string
	// Prelude is what the plan said, which opens a build's log.
	Prelude string
	// Within is how long the run may take from when it is handed: its claim's
	// deadline less the claim's margin. Zero is unbounded.
	Within time.Duration
}

// Output is where a run puts what it produces, and is the log's writer. Nothing
// here reaches a store: the log goes to a file the node owns
// (services/plm/livelog) and then the asset store, and the job record hears
// about it once, when the job settles. The log is readable and durable once
// Write returns.
type Output interface {
	io.Writer

	// Asset hands over one built artifact of the run's environment as bytes, read
	// to EOF. The key composed from deployment and resource is build's
	// grammar, so a worker arrives at the same one.
	Asset(ctx context.Context, resourceID string, content io.Reader) error
}

// Builder executes one job, on a builder (services/builder).
type Executor interface {
	// Plan reads which environments the pushed commit feeds, each of which
	// becomes a job of its own. What it writes opens each of those jobs' logs.
	Plan(ctx context.Context, w Work, out io.Writer) ([]string, error)

	// Run executes one job and must return when ctx ends (Cancel, the claim deadline,
	// Close). A run cut short by shutdown is abandoned, so the claim lapses and
	// another node builds it. Leadership loss does not cancel it; the ack catches a
	// stale run before anything is published. An error from Run is terminal: the
	// queue's retry is for a worker that died, not a build that failed.
	Run(ctx context.Context, w Work, out Output) error
}

// Service is a builder as plm hands it runs (pkg/proto/tau/builder/v2): over its
// protocol, or in the caller where a development cloud runs no builder. It runs one
// at a time, and keeps each run's log until plm seals it.
type Builder interface {
	// Plan reads which environments the pushed commit feeds.
	Plan(ctx context.Context, w Work) (*builderv2.Planned, error)
	// Build builds w's environment, and returns once the run has ended.
	Build(ctx context.Context, w Work) (*builderv2.Built, error)
	// Cancel stops the run, whose Plan or Build then returns as it ended.
	Cancel(ctx context.Context, r *builderv2.Run) error
	// Seal ends the run's log with the status build settled its job with, and keeps
	// a build's log in the storage service.
	Seal(ctx context.Context, s *builderv2.Sealing) (*builderv2.Sealed, error)
	// Follow reads the run's log from req's offset.
	Follow(ctx context.Context, req *plmv2.FollowRequest) (Events, error)
}

// Events is a run's log as it is read: Receive blocks until the next event, and is
// io.EOF once the log has ended.
type Events interface {
	Receive() (*plmv2.Job_Event, error)
	Close() error
}

// WorkOf is a run's work as builder.proto carries it.
func WorkOf(w *builderv2.Work) Work {
	return Work{
		JobID:         w.GetJobId(),
		RepositoryKey: w.GetRepositoryKey(),
		Ref:           w.GetRef(),
		CommitSHA:     w.GetCommitSha(),
		Before:        w.GetBefore(),
		ProjectID:     w.GetProjectId(),
		Attempt:       w.GetAttempt(),
		Environment:   w.GetEnvironment(),
		Prelude:       w.GetPrelude(),
		Within:        time.Duration(w.GetWithin()),
	}
}

// Proto is w as builder.proto carries it.
func (w Work) Proto() *builderv2.Work {
	return &builderv2.Work{
		JobId:         w.JobID,
		RepositoryKey: w.RepositoryKey,
		Ref:           w.Ref,
		CommitSha:     w.CommitSHA,
		Before:        w.Before,
		ProjectId:     w.ProjectID,
		Attempt:       w.Attempt,
		Environment:   w.Environment,
		Prelude:       w.Prelude,
		Within:        int64(w.Within),
	}
}
