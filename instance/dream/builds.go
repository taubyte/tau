package dream

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/instance"
	"github.com/taubyte/tau/p2p/discovery"
	"github.com/taubyte/tau/p2p/peer"
	builderv2 "github.com/taubyte/tau/pkg/proto/gen/tau/builder/v2"
	plmv2 "github.com/taubyte/tau/pkg/proto/gen/tau/plm/v2"
	"github.com/taubyte/tau/services/auth/projects"
	"github.com/taubyte/tau/services/builder"
	"github.com/taubyte/tau/services/plm"
	"github.com/taubyte/tau/services/plm/clients"
	"github.com/taubyte/tau/services/plm/joblog"
	"github.com/taubyte/tau/services/plm/run"
	"github.com/taubyte/tau/services/worker"
	workerHosted "github.com/taubyte/tau/services/worker/hosted"
)

// builds.go is how a cloud builds a push. PLM and builder are two services, and
// a cloud runs either, both or neither: a push goes through plm's queue when a
// node runs build, else straight to a builder over its protocol when a node runs one,
// else to the builder's runs in this process. Without build nothing records what was
// built, so the cloud answers its nodes' lookups of it (artifacts).

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
	var plms, builders []libp2ppeer.ID
	for _, n := range nodes {
		if slices.Contains(n.Services(), instance.PLM) {
			plms = append(plms, n.Host().ID())
		}
		if slices.Contains(n.Services(), instance.Builder) {
			builders = append(builders, n.Host().ID())
		}
	}
	var (
		b   builds
		err error
	)
	switch {
	case len(plms) > 0:
		b, err = u.queued(plms)
	case len(builders) > 0:
		b, err = u.builderAt(builders[0])
	case len(nodes) > 0:
		b, err = u.inPlace(nodes[0])
	default:
		err = errors.New("dream: a cloud with no node builds nothing")
	}
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

// builderAt is pushes run on the builder id, over its protocol.
func (u *Cloud) builderAt(id libp2ppeer.ID) (builds, error) {
	g, err := u.guest()
	if err != nil {
		return nil, err
	}
	r, err := clients.BuilderAt(g.Host(), id)
	if err != nil {
		return nil, err
	}
	return &direct{u: u, runs: r, also: r}, nil
}

// inPlace is pushes run by the builder's runs in this process, beside n, whose
// services they reach as a builder on n would.
func (u *Cloud) inPlace(n *instance.Instance) (builds, error) {
	h := n.HostOf(instance.Builder)
	var cfg builder.Config
	if err := h.Config(&cfg); err != nil {
		return nil, err
	}
	runs, err := builder.Open(h, cfg)
	if err != nil {
		return nil, err
	}
	return &direct{u: u, runs: runs, also: runs}, nil
}

// direct is pushes run on a builder with no queue in front of it: the plan, then each
// environment it feeds, one after another, as the jobs a plm node would make.
type direct struct {
	u    *Cloud
	runs run.Builder
	// also is what reaching the builder opened.
	also io.Closer

	// mu runs one push at a time: a builder runs one run at a time.
	mu     sync.Mutex
	before map[string]string
}

func (d *direct) Close() error { return d.also.Close() }

func (d *direct) build(ctx context.Context, p Push) (Outcome, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	table, err := d.u.projects()
	if err != nil {
		return Outcome{}, err
	}
	linked, err := table.Repository(ctx, p.Repository)
	if errors.Is(err, projects.ErrNotLinked) {
		// REFUSED AS PLM REFUSES IT, before anything runs.
		return Outcome{}, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("repository %q is not registered with this cloud: %w", p.Repository, err))
	}
	if err != nil {
		return Outcome{}, err
	}
	work := run.Work{
		JobID:         jobID(p, ""),
		RepositoryKey: p.Repository,
		Ref:           p.Ref,
		CommitSHA:     p.Commit,
		ProjectID:     linked.Project,
		Attempt:       1,
	}
	planned, err := d.runs.Plan(ctx, work)
	if err != nil {
		return Outcome{}, err
	}
	plan := planOf(p, work, planned)
	d.seal(ctx, work, plan.Status)
	out := Outcome{Plan: plan}
	for _, environment := range planned.GetEnvironments() {
		if plan.Status != plmv2.JobStatus_JobStatusSuccess {
			break
		}
		job, err := d.environment(ctx, work, environment, planned.GetSaid())
		if err != nil {
			return out, err
		}
		out.Environments = append(out.Environments, job)
	}
	return out, nil
}

// planOf is how a plan ended, as plm would record its job: failed with what it said,
// failed for feeding nothing, or the parent of a job per environment it feeds.
func planOf(p Push, work run.Work, planned *builderv2.Planned) *plmv2.Job {
	plan := jobOf(work)
	switch {
	case planned.GetError() != "":
		plan.Status, plan.Reason = plmv2.JobStatus_JobStatusFailed, strings.TrimSpace(strings.TrimSpace(planned.GetSaid())+"\nerror: "+planned.GetError())
	case len(planned.GetEnvironments()) == 0:
		plan.Status, plan.Reason = plmv2.JobStatus_JobStatusFailed, "the commit feeds no environment"
	default:
		plan.Status = plmv2.JobStatus_JobStatusSuccess
		for _, environment := range planned.GetEnvironments() {
			plan.Children = append(plan.Children, jobID(p, environment))
		}
	}
	return plan
}

// environment builds one environment a plan fed, and keeps what it built for the
// cloud's lookups when it succeeded.
func (d *direct) environment(ctx context.Context, plan run.Work, environment, said string) (*plmv2.Job, error) {
	w := plan
	w.JobID, w.Environment, w.Prelude = jobID(Push{Repository: plan.RepositoryKey, Ref: plan.Ref, Commit: plan.CommitSHA}, environment), environment, said
	anchor := strings.Join([]string{plan.RepositoryKey, plan.Ref, environment}, "\x00")
	w.Before = d.before[anchor]
	built, err := d.runs.Build(ctx, w)
	if err != nil {
		return nil, err
	}
	job := jobOf(w)
	job.Status = plmv2.JobStatus_JobStatusSuccess
	if built.GetError() != "" {
		job.Status, job.Reason = plmv2.JobStatus_JobStatusFailed, built.GetError()
	}
	job.AssetCid = map[string]string{}
	for _, kept := range built.GetStored() {
		job.AssetCid[kept.GetResourceId()] = kept.GetCid()
	}
	if cid := d.seal(ctx, w, job.Status).GetLog(); cid != "" {
		job.Logs = map[string]string{fmt.Sprintf("%020d", w.Attempt): cid}
	}
	if job.Status == plmv2.JobStatus_JobStatusSuccess {
		if d.before == nil {
			d.before = map[string]string{}
		}
		d.before[anchor] = w.CommitSHA
		d.u.kept.keep(w.ProjectID, environment, built.GetStored())
	}
	return job, nil
}

// seal ends a run's log with how it ended. A seal that fails costs the log.
func (d *direct) seal(ctx context.Context, w run.Work, status plmv2.JobStatus) *builderv2.Sealed {
	sealed, err := d.runs.Seal(ctx, &builderv2.Sealing{Run: &builderv2.Run{JobId: w.JobID, Attempt: w.Attempt}, Status: status})
	if err != nil {
		return &builderv2.Sealed{}
	}
	return sealed
}

// jobOf is a run's job as a plm node would record it.
func jobOf(w run.Work) *plmv2.Job {
	return &plmv2.Job{
		Id:            w.JobID,
		RepositoryKey: w.RepositoryKey,
		Ref:           w.Ref,
		CommitSha:     w.CommitSHA,
		Environment:   w.Environment,
		Attempt:       int32(w.Attempt),
		Timestamp:     time.Now().Unix(),
	}
}

// jobID names one push's plan, or one environment of it.
func jobID(p Push, environment string) string {
	sum := sha256.New()
	for _, field := range []string{p.Repository, p.Ref, p.Commit, environment} {
		sum.Write([]byte(strconv.Itoa(len(field)) + ":" + field))
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// builtHere is what this cloud built itself, with no plm service to record it: a
// deployment's resource to the cid of its artifact, the newest success's.
type builtHere struct {
	mu   sync.Mutex
	cids map[string]string
}

func builtKey(project, environment, resource string) string {
	return strings.Join([]string{project, environment, resource}, "\x00")
}

func (b *builtHere) keep(project, environment string, stored []*builderv2.Stored) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cids == nil {
		b.cids = map[string]string{}
	}
	for _, kept := range stored {
		b.cids[builtKey(project, environment, kept.GetResourceId())] = kept.GetCid()
	}
}

func (b *builtHere) artifact(project, environment, resource string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cid, ok := b.cids[builtKey(project, environment, resource)]
	return cid, ok
}

// artifacts is what a node's worker looks a built resource up in: what the cloud
// built itself, else plm's records on the node.
type artifacts struct {
	kept *builtHere

	mu      sync.Mutex
	records worker.Artifacts
}

var _ worker.Artifacts = (*artifacts)(nil)

func (a *artifacts) Artifact(ctx context.Context, project, environment, kind, resource string) (string, error) {
	if cid, ok := a.kept.artifact(project, environment, resource); ok {
		return cid, nil
	}
	a.mu.Lock()
	records := a.records
	a.mu.Unlock()
	if records == nil {
		return "", fmt.Errorf("%w: %s/%s's %s", workerHosted.ErrNotBuilt, project, environment, resource)
	}
	return records.Artifact(ctx, project, environment, kind, resource)
}

// on is the lookup falling back to n's own records, once n has started.
func (a *artifacts) on(n *instance.Instance) {
	a.mu.Lock()
	a.records = n.PLMArtifacts()
	a.mu.Unlock()
}
