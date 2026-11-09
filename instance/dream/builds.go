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
	pkgregister "github.com/taubyte/tau/pkg/register"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
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

// Build is p built as this cloud builds it, returned once the push and every job it
// made have ended, and what a deploy among them published is what every node's
// register serves: plm hands a run to any builder, and the next push's run may read
// the register on a node the publication has not reached yet.
func (u *Cloud) Build(ctx context.Context, p Push) (Outcome, error) {
	b, err := u.builder()
	if err != nil {
		return Outcome{}, err
	}
	out, err := b.build(ctx, p)
	if err != nil {
		return out, err
	}
	return out, u.deployed(ctx, p, out)
}

// deployed waits until every node running the register serves one pointer for each
// environment a push of a configuration repository built.
func (u *Cloud) deployed(ctx context.Context, p Push, out Outcome) error {
	var built []string
	for _, job := range out.Environments {
		if job.GetStatus() == plmv2.JobStatus_JobStatusSuccess {
			built = append(built, job.GetEnvironment())
		}
	}
	if len(built) == 0 {
		return nil
	}
	table, err := u.projects()
	if err != nil {
		return err
	}
	linked, err := table.Repository(ctx, p.Repository)
	if err != nil || !linked.Configures {
		return err
	}
	for _, environment := range built {
		for !u.agree(ctx, linked.Project, environment) {
			if err := pause(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// agree is whether every node running the register serves one pointer for a
// project's environment, and, when the revision publishes one for its region, the
// view its region reads: an object of its own, which can arrive after the pointer.
func (u *Cloud) agree(ctx context.Context, project, environment string) bool {
	var first *pkgregister.Pointer
	for _, n := range u.nodes() {
		if n.Register() == nil || n.Register().Local() == nil {
			continue
		}
		at, err := n.Register().Local().Resolve(ctx, project, environment)
		switch {
		case err != nil:
			return false
		case first == nil:
			first = &at
		case at != *first:
			return false
		}
		manifest, _, err := n.Register().Local().Manifest(ctx, project, environment)
		if err != nil || manifest.Revision != at.Revision {
			return false
		}
		region := n.HostOf("").Region()
		if region == "" {
			region = schema.DefaultRegion
		}
		if _, listed := manifest.Views[region]; !listed && !manifest.Whole {
			continue
		}
		viewed, err := n.Register().Local().ResolveView(ctx, project, environment, region)
		if err != nil || viewed.Revision != manifest.Revision {
			return false
		}
	}
	return true
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
