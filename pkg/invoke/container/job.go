package container

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"

	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/invoke"
)

// job.go runs a workload that runs to completion (invoke.Spec.Completes): each
// request is one run of the image.
//
//	stdin                   the request's body, then EOF
//	TAU_INVOCATION_ID       one id per run
//	exit 0                  200, body is stdout
//	exit n                  ErrJobFailed, with the code and the tail of stderr
//	past the timeout        killed: ErrJobTimedOut
//
// Cloud Run Jobs' and a Kubernetes Job's shape, so batch images run unmodified.
// One container per request; Start only pulls.

var (
	// ErrJobFailed is a run that exited non-zero. It carries the code and the
	// end of what the run wrote to stderr, which is where a batch job explains
	// itself.
	ErrJobFailed = errors.New("container: the job failed")

	// ErrJobTimedOut is a run killed for outliving its request.
	ErrJobTimedOut = errors.New("container: the job ran past its timeout")
)

// stderrTail is how much of a failed run's stderr travels with its error.
const stderrTail = 2 << 10

// startJob prepares a run-to-completion workload: the image is pulled once,
// here, so a request pays only for its own run.
func (e *Runtime) startJob(ctx context.Context, spec invoke.Spec, img Image, mounts []core.VolumeMount) (invoke.Instance, error) {
	if im := e.opts.backend.Image(img.Ref); im != nil && !im.Exists(ctx) {
		if err := im.Pull(ctx); err != nil {
			return nil, fmt.Errorf("container: pulling %s: %w", img.Ref, err)
		}
	}
	return &job{e: e, spec: spec, img: img, mounts: mounts}, nil
}

// job is a run-to-completion workload's instance: a recipe for one run.
type job struct {
	e      *Runtime
	spec   invoke.Spec
	img    Image
	mounts []core.VolumeMount
}

var _ invoke.Instance = (*job)(nil)

// Serve is one run.
func (j *job) Serve(ctx context.Context, req *invoke.Request) (*invoke.Response, error) {
	if j.spec.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, j.spec.Timeout)
		defer cancel()
	}

	cfg := j.e.config(j.img, nil, 0, "", j.mounts)
	cfg.Env = append(cfg.Env, "TAU_INVOCATION_ID="+invocationID())
	cfg.Stdin = bytes.NewReader(req.Body)

	backend := j.e.opts.backend
	id, err := backend.Create(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("container: creating a run of %s: %w", j.spec.Name, err)
	}
	// REMOVED WHATEVER HAPPENS, and on a context of its own: the request's may
	// be the reason this is running.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		backend.Stop(cleanup, id)
		backend.Remove(cleanup, id)
	}()

	if err := backend.Start(ctx, id); err != nil {
		return nil, fmt.Errorf("container: starting a run of %s: %w", j.spec.Name, err)
	}
	if err := backend.Wait(ctx, id); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: %s, after %s", ErrJobTimedOut, j.spec.Name, j.spec.Timeout)
		}
		return nil, fmt.Errorf("container: waiting for a run of %s: %w", j.spec.Name, err)
	}

	info, err := backend.Inspect(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("container: inspecting a run of %s: %w", j.spec.Name, err)
	}
	stdout, stderr, err := output(ctx, backend, id)
	if err != nil {
		return nil, fmt.Errorf("container: reading a run of %s: %w", j.spec.Name, err)
	}

	if info.ExitCode != 0 {
		tail := stderr
		if len(tail) > stderrTail {
			tail = tail[len(tail)-stderrTail:]
		}
		return nil, fmt.Errorf("%w: %s exited %d: %s", ErrJobFailed, j.spec.Name, info.ExitCode, bytes.TrimSpace(tail))
	}
	return &invoke.Response{Status: 200, Body: stdout}, nil
}

// Close has nothing to release: every run removed its own container.
func (j *job) Close() error { return nil }

// output is a finished run's stdout and stderr, demultiplexed: both backends
// frame logs the way docker does.
func output(ctx context.Context, backend Backend, id core.ContainerID) (stdout, stderr []byte, err error) {
	logs, err := backend.Logs(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	defer logs.Close()
	var out, errs bytes.Buffer
	if _, err := stdcopy.StdCopy(&out, &errs, logs); err != nil {
		return nil, nil, err
	}
	return out.Bytes(), errs.Bytes(), nil
}

func invocationID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
