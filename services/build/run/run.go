// Package run is what the build backend hands the thing that runs a job, and what
// it asks of it: shared by the backend and every runner, so neither imports the
// other.
package run

import (
	"context"
	"errors"
	"io"
)

// Confirmer asks a repository whether it holds ref at commit. A nil error is
// yes; ErrNotAtCommit is no; anything else is that it could not be asked.
type Confirmer interface {
	Confirm(ctx context.Context, repositoryKey, ref, commit string) error
}

// ErrNotAtCommit is a repository that does not hold the pushed ref at the
// pushed commit.
var ErrNotAtCommit = errors.New("build: the repository does not hold that ref at that commit")

// Work is everything a run is given, derived by the backend rather than carried
// across the frontend/backend boundary.
type Work struct {
	// JobID is the record this run belongs to. What the run produces is
	// recorded on it, at this attempt.
	JobID string
	// RepositoryKey is the canonical clone identity (pkg/git/repokey).
	RepositoryKey string
	// Ref is the pushed git ref. It already passed the frontend's
	// branch-eligibility policy; the backend does not re-filter it.
	Ref string
	// CommitSHA is the pushed head — the "after" of the diff.
	CommitSHA string
	// Before is the last commit the backend finished for this (repository, ref), from
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
}

// Output is where a run puts what it produces, and is the log's writer. Nothing
// here reaches a store: the log goes to a file the node owns
// (services/build/livelog) and then the asset store, and the job record hears
// about it once, when the job settles. The log is readable and durable once
// Write returns.
type Output interface {
	io.Writer

	// Asset hands over one built artifact of the run's environment as bytes, read
	// to EOF. The key composed from deployment and resource is the backend's
	// grammar, so a worker arrives at the same one.
	Asset(ctx context.Context, resourceID string, content io.Reader) error
}

// Runner executes one job. A backend without one serves and replicates but claims
// nothing — which is what a node that holds the domain's state but is not meant
// to run CI wants.
type Runner interface {
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
