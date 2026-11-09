package dream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"connectrpc.com/connect"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/instance"
	"github.com/taubyte/tau/p2p/discovery"
	"github.com/taubyte/tau/p2p/peer"
	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
	"github.com/taubyte/tau/services/plm"
	"github.com/taubyte/tau/services/plm/clients"
	"github.com/taubyte/tau/services/plm/joblog"
)

// builds.go is how a cloud builds a push: through plm, which records what was built
// where every worker reads it. A cloud running no plm builds nothing.

// awaitEvery is how often a job queued at plm is read again until it has ended.
const awaitEvery = 50 * time.Millisecond

// builds is what a cloud builds a push with, and closes with it.
type builds interface {
	build(ctx context.Context, p Push) (Outcome, error)
	io.Closer
}

// PLM is p built as this cloud builds it, returned once the push and every job
// it made have ended.
func (u *Cloud) Build(ctx context.Context, p Push) (Outcome, error) {
	b, err := u.builder()
	if err != nil {
		return Outcome{}, err
	}
	return b.build(ctx, p)
}

// builder is the cloud's way of building, settled the first time it is asked for
// and kept until the cloud closes.
func (u *Cloud) builder() (builds, error) {
	u.buildsMu.Lock()
	defer u.buildsMu.Unlock()
	if u.builds != nil {
		return u.builds, nil
	}
	nodes := u.nodes()
	if len(nodes) == 0 {
		return nil, errors.New("dream: a cloud with no node builds nothing")
	}
	var plms []libp2ppeer.ID
	for _, n := range nodes {
		if slices.Contains(n.Services(), instance.PLM) {
			plms = append(plms, n.Host().ID())
		}
	}
	if len(plms) == 0 {
		return nil, fmt.Errorf("dream: no node runs %s, so nothing builds a push", instance.PLM)
	}
	b, err := u.queued(plms)
	if err != nil {
		return nil, err
	}
	u.builds = b
	return b, nil
}

// guest is the cloud's own peer, which calls its services the way a tool beside
// them would.
func (u *Cloud) guest() (peer.Node, error) {
	if u.visitor == nil {
		g, err := u.mesh.Guest()
		if err != nil {
			return nil, err
		}
		u.visitor = g
	}
	return u.visitor, nil
}

// queued is pushes enqueued at build, over its protocol, and awaited there.
type queued struct {
	jobs *clients.BuildClient
	pool *clients.Pool
}

func (u *Cloud) queued(plms []libp2ppeer.ID) (builds, error) {
	g, err := u.guest()
	if err != nil {
		return nil, err
	}
	pool, err := clients.NewPool(g.Host(), discovery.Static(plms...), plm.Protocol, len(plms))
	if err != nil {
		return nil, err
	}
	jobs, err := clients.Builds(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &queued{jobs: jobs, pool: pool}, nil
}

func (q *queued) build(ctx context.Context, p Push) (Outcome, error) {
	id, err := q.enqueue(ctx, p)
	if err != nil {
		return Outcome{}, err
	}
	plan, err := q.await(ctx, id)
	if err != nil {
		return Outcome{}, err
	}
	out := Outcome{Plan: plan}
	for _, child := range plan.GetChildren() {
		job, err := q.await(ctx, child)
		if err != nil {
			return out, err
		}
		out.Environments = append(out.Environments, job)
	}
	return out, nil
}

// enqueue is p queued at build, asked again while plm's nodes say Unavailable:
// what they say while none leads the queue, as when a cloud has just started.
func (q *queued) enqueue(ctx context.Context, p Push) (string, error) {
	for {
		id, err := q.jobs.Enqueue(ctx, p.Repository, p.Ref, p.Commit)
		if connect.CodeOf(err) != connect.CodeUnavailable {
			return id, err
		}
		if err := pause(ctx); err != nil {
			return "", err
		}
	}
}

// await is a job once it has ended, read from build until it has.
func (q *queued) await(ctx context.Context, id string) (*plmv2.Job, error) {
	for {
		job, err := q.jobs.Get(ctx, id)
		switch {
		case connect.CodeOf(err) == connect.CodeUnavailable:
		case err != nil:
			return nil, err
		case joblog.Terminal(job.GetStatus()):
			return job, nil
		}
		if err := pause(ctx); err != nil {
			return nil, err
		}
	}
}

// pause is awaitEvery, or ctx's end.
func pause(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(awaitEvery):
		return nil
	}
}

func (q *queued) Close() error {
	q.pool.Close()
	return nil
}
