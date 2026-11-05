package jobs

import (
	"context"
	"errors"
	"io"

	"github.com/taubyte/tau/core/service"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/services/build/run"
)

// Direct is build's jobs with no queue to share: a push runs to its end in the
// caller awaiting it, its plan and then each environment's build, recorded as a
// queued one is. It is what a development cloud with no build node builds with
// (instance/dream). Its queue is its own, in memory.
type Direct struct {
	Local
	also []io.Closer
}

// Alone is jobs on the instance h, run with r: the records in the database a build
// node keeps them in, so whatever reads a build's records reads these.
func Alone(h service.Host, r run.Runner) (*Direct, error) {
	s, also, err := onInstance(h, Config{}, raft.NewMockCluster(), withRunner(r))
	d := &Direct{Local: Local{s: s}, also: also}
	if err != nil {
		return nil, errors.Join(err, d.Close())
	}
	return d, nil
}

// Await runs the queue in the caller until jobID has ended: each step claims what
// is due and runs it here, so a plan's environment jobs run as it queues them.
func (d *Direct) Await(ctx context.Context, jobID string) (*buildv2.Job, error) {
	for {
		job, err := d.s.job(jobID)
		if err != nil {
			return nil, err
		}
		if finished(job.GetStatus()) {
			return job, nil
		}
		ran, err := d.s.step(ctx)
		if err != nil {
			return nil, err
		}
		if !ran {
			return nil, errFailedPrecondition("build: job %s is %s and nothing is due to run", jobID, job.GetStatus())
		}
	}
}

// Close closes what Alone opened.
func (d *Direct) Close() error {
	var errs []error
	for i := len(d.also) - 1; i >= 0; i-- {
		errs = append(errs, d.also[i].Close())
	}
	d.also = nil
	return errors.Join(errs...)
}
