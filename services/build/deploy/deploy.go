// Package deploy is what a push to a project's configuration repository does, and
// the only writer of the register: a deploy is a commit that arrived through
// build's ingress, was authorized, and became a watched job.
//
// The pushed branch picks the deployments through the project's environment
// table, each one a job of its own; the revision is the published one plus one.
// Each deployment is its own compile, since per-environment keys resolve
// differently. A push to a code
// repository is delegated to services/build/artifact, and refused by name on a
// node without a builder.
package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/afero"

	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/services/build/run"
)

// branchRef is the only ref shape a deploy answers to; a tag matches no
// environment.
const branchRef = "refs/heads/"

var (
	// ErrNotConfiguration is a code push on a node with nothing to build code with,
	// refused by name rather than succeeding having done nothing.
	ErrNotConfiguration = errors.New("deploy: this repository is not the project's configuration, and this node was not given anything to build code with")

	// ErrNotABranch is a ref that is not a branch.
	ErrNotABranch = errors.New("deploy: only a branch is a deployment")

	// ErrNoDeployment is a branch no deployment claims.
	ErrNoDeployment = errors.New("deploy: no deployment is fed by that branch")

	ErrNoRegister = errors.New("deploy: nowhere to publish to")
	ErrNoCheckout = errors.New("deploy: no way to read a repository")
	ErrNoProjects = errors.New("deploy: no way to tell a configuration repository from a code one")
	// ErrNotClaimed is a configuration whose project.yaml names an id this repository
	// has not claimed in the register's project table; a copied config would
	// otherwise move another project's pointers.
	ErrNotClaimed = errors.New("deploy: the project id is not claimed by this repository")

	// ErrBroken is a project the audit calls broken, or cannot cut into views here.
	ErrBroken = errors.New("deploy: broken, so nothing is published")

	// ErrUnproven is a zone the project has not proven is its own, by the TXT
	// record `tau domain claim` prints. A deploy that published it would let
	// any project answer for any domain.
	ErrUnproven = errors.New("deploy: a custom domain is not proven to be the project's")
)

// Domains proves a custom domain is a project's. services/cm's Domains is one.
type Domains interface {
	Prove(ctx context.Context, project, fqdn string) error
}

// Projects answers what a repository is to a project.
type Projects interface {
	// Configures reports whether this repository is the project's
	// configuration — the one whose pushes are deploys.
	Configures(ctx context.Context, projectID, repositoryKey string) (bool, error)
}

// Checkout puts one commit (never just the branch head) of a repository in a
// directory on this machine, since a build bind-mounts it.
type Checkout interface {
	// Checkout returns the directory holding the tree at `commit`, plus a
	// function that releases whatever it took.
	Checkout(ctx context.Context, repositoryKey, branch, commit string) (dir string, done func(), err error)
}

// Builder is what a code push becomes, and what a configuration push becomes once
// published (bare-path resources). An interface so this package does not import
// the container runner.
type Builder interface {
	// Plan is which environments a code push feeds.
	Plan(ctx context.Context, w run.Work, out io.Writer) ([]string, error)
	Build(ctx context.Context, w run.Work, out run.Output) error
}

// Register is the slice of the register a deploy writes.
type Register interface {
	Resolve(ctx context.Context, project, environment string) (register.Pointer, error)
	Manifest(ctx context.Context, project, environment string) (register.Manifest, register.ObjectID, error)
	Publish(ctx context.Context, p register.Publication) (register.ObjectID, error)
}

// Deploy is a run.Runner.
type Deploy struct {
	projects Projects
	checkout Checkout

	// register is looked up per run: a register can go away between builds.
	register func(context.Context) (Register, error)

	// builder runs a push to a code repository, when this node was given one.
	builder Builder

	// regions answers where a deployment runs; see WithRegions.
	regions schema.Regions

	// cloudFqdn completes generated names; a name that cannot be completed is
	// reported rather than published half-formed.
	cloudFqdn string

	domains Domains
}

var _ run.Runner = (*Deploy)(nil)

// New builds a deploy runner.
func New(opts ...Option) (*Deploy, error) {
	var o options
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return nil, fmt.Errorf("deploy: option: %w", err)
		}
	}

	switch {
	case o.projects == nil:
		return nil, ErrNoProjects
	case o.checkout == nil:
		return nil, ErrNoCheckout
	case o.register == nil:
		return nil, ErrNoRegister
	}

	regions := o.regions
	if regions == nil {
		regions = schema.WithNoRegions()
	}

	return &Deploy{
		projects:  o.projects,
		checkout:  o.checkout,
		register:  o.register,
		builder:   o.builder,
		cloudFqdn: o.cloudFqdn,
		regions:   regions,
		domains:   o.domains,
	}, nil
}

// Plan reads which environments the push feeds: a configuration push from the
// pushed commit itself, so one that adds an environment builds it; a code push from
// what the project publishes, through the builder.
func (d *Deploy) Plan(ctx context.Context, w run.Work, out io.Writer) ([]string, error) {
	branch, ok := strings.CutPrefix(w.Ref, branchRef)
	if !ok || branch == "" {
		return nil, fmt.Errorf("%w: %s", ErrNotABranch, w.Ref)
	}

	configures, err := d.projects.Configures(ctx, w.ProjectID, w.RepositoryKey)
	if err != nil {
		return nil, fmt.Errorf("deploy: %w", err)
	}
	if !configures {
		if d.builder == nil {
			return nil, ErrNotConfiguration
		}
		return d.builder.Plan(ctx, w, out)
	}

	dir, done, err := d.checkout.Checkout(ctx, w.RepositoryKey, branch, w.CommitSHA)
	if err != nil {
		return nil, fmt.Errorf("deploy: reading %s at %s: %w", w.RepositoryKey, short(w.CommitSHA), err)
	}
	defer done()

	names, err := deployments(afero.NewOsFs(), dir, branch)
	if err != nil {
		return nil, err
	}
	say(out, "%s feeds %s", branch, strings.Join(names, ", "))
	return names, nil
}

// Run publishes what the pushed commit compiles to for the job's one environment,
// then builds what it backs. Each environment is its own job, so one that fails to
// compile does not hold back another.
func (d *Deploy) Run(ctx context.Context, w run.Work, out run.Output) error {
	branch, ok := strings.CutPrefix(w.Ref, branchRef)
	if !ok || branch == "" {
		return fmt.Errorf("%w: %s", ErrNotABranch, w.Ref)
	}

	configures, err := d.projects.Configures(ctx, w.ProjectID, w.RepositoryKey)
	if err != nil {
		return fmt.Errorf("deploy: %w", err)
	}
	if !configures {
		// A code push is the builder's job; this is the one place that tells the two
		// apart.
		if d.builder == nil {
			return ErrNotConfiguration
		}
		return d.builder.Build(ctx, w, out)
	}

	dir, done, err := d.checkout.Checkout(ctx, w.RepositoryKey, branch, w.CommitSHA)
	if err != nil {
		return fmt.Errorf("deploy: reading %s at %s: %w", w.RepositoryKey, short(w.CommitSHA), err)
	}
	defer done()

	name := w.Environment
	compiled, p, flat, err := compileFor(afero.NewOsFs(), dir, branch, name)
	if err != nil {
		return err
	}
	views, err := d.cut(flat, name, out)
	if err != nil {
		return err
	}
	say(out, "compiled %s", name)

	// The compiled deployment must name w.ProjectID, the project this repository
	// configures, or nothing is written.
	if id := p.ID(); id != w.ProjectID {
		return fmt.Errorf("%w: project.yaml names %q, and %s is linked as the configuration of %q — run `tau link` for this id, or put %q back",
			ErrNotClaimed, id, w.RepositoryKey, w.ProjectID, w.ProjectID)
	}

	proven := map[string]bool{}
	for _, z := range p.Zones() {
		if err := d.prove(ctx, w.ProjectID, z.Fqdn, proven); err != nil {
			return err
		}
	}

	r, err := d.register(ctx)
	if err != nil {
		return err
	}

	hosts, incomplete := hostsOf(p, d.cloudFqdn)
	object, revision, unchanged, err := d.put(ctx, r, w.ProjectID, name, compiled, views, hosts)
	if err != nil {
		return err
	}
	if unchanged {
		say(out, "%s unchanged at revision %d (%s)", name, revision, object)
	} else {
		say(out, "%s published as revision %d (%s)", name, revision, object)
	}
	for _, host := range hosts {
		say(out, "    %s", host)
	}
	for _, host := range incomplete {
		say(out, "    %s.<cloud fqdn> — not published; this cloud was not told its own zone", host)
	}

	// A BARE PATH IS IN THIS REPOSITORY, so what it backs is built from this
	// commit — after publishing, because the builder reads what to build from
	// the deployment just published.
	if d.builder != nil {
		return d.builder.Build(ctx, w, out)
	}
	return nil
}

// prove refuses a zone that is not the project's, before anything publishes.
// The cloud's generated names are its own and are never asked about.
func (d *Deploy) prove(ctx context.Context, projectID, fqdn string, proven map[string]bool) error {
	fqdn = strings.ToLower(strings.TrimSuffix(fqdn, "."))
	if proven[fqdn] {
		return nil
	}
	if d.domains == nil {
		return fmt.Errorf("%w: %s: this cloud proves no domain", ErrUnproven, fqdn)
	}
	if err := d.domains.Prove(ctx, projectID, fqdn); err != nil {
		return fmt.Errorf("%w: %s: %w — run `tau domain claim %s` and set the TXT record it prints", ErrUnproven, fqdn, err, fqdn)
	}
	proven[fqdn] = true
	return nil
}

// put publishes one deployment. A compiled project is content-addressed, so an
// unchanged one moves no pointer.
func (d *Deploy) put(ctx context.Context, r Register, projectID, environment string,
	compiled []byte, views []register.View, hosts []string) (register.ObjectID, uint64, bool, error) {

	var revision uint64
	current, err := r.Resolve(ctx, projectID, environment)
	switch {
	case err == nil:
		same, err := sameViews(ctx, r, projectID, environment, views)
		if err != nil {
			return "", 0, false, err
		}
		if current.Object == register.IDOf(compiled) && same {
			return current.Object, current.Revision, true, nil
		}
		revision = current.Revision + 1
	case errors.Is(err, register.ErrNotPublished):
		revision = 1
	default:
		return "", 0, false, fmt.Errorf("deploy: reading what %s/%s runs: %w", projectID, environment, err)
	}

	id, err := r.Publish(ctx, register.Publication{
		Project:     projectID,
		Environment: environment,
		Revision:    revision,
		Compiled:    compiled,
		Hosts:       hosts,
		Views:       views,
	})
	if err != nil {
		return "", 0, false, err
	}
	return id, revision, false, nil
}

// Compiled is what a push of the tree at dir to branch deploys: the project each
// environment it feeds compiles to, compiled as a deploy compiles it.
func Compiled(dir, branch string) (map[string]*specs.Project, error) {
	fs := afero.NewOsFs()
	names, err := deployments(fs, dir, branch)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*specs.Project, len(names))
	for _, name := range names {
		_, p, _, err := compileFor(fs, dir, branch, name)
		if err != nil {
			return nil, err
		}
		out[name] = p
	}
	return out, nil
}

// deployments is which of a project's environments this push feeds.
func deployments(fs afero.Fs, dir, branch string) ([]string, error) {
	declared, err := schema.EnvironmentsOf(fs, dir)
	if err != nil {
		return nil, fmt.Errorf("deploy: reading the environment table: %w", err)
	}

	if len(declared) == 0 {
		return nil, fmt.Errorf("%w: the project declares no environments", ErrNoDeployment)
	}

	fed := schema.DeploymentNames(declared, branch)
	if len(fed) == 0 {
		return nil, fmt.Errorf("%w: %q feeds none of %s",
			ErrNoDeployment, branch, strings.Join(schema.EnvironmentNames(declared), ", "))
	}
	return fed, nil
}

// compileFor compiles a config directory for one deployment, with the declared
// environment set so other environments' suffixed keys are not typos. The bytes
// are the compiled object's JSON, which pkg/project reads.
func compileFor(fs afero.Fs, dir, branch, environment string) ([]byte, *specs.Project, map[string]any, error) {
	declared, err := schema.EnvironmentsOf(fs, dir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("deploy: reading the environment table: %w", err)
	}

	options := []schema.Option{
		schema.WithVirtual(fs, dir),
		schema.WithEnvironments(schema.EnvironmentNames(declared)...),
		schema.WithBranch(branch),
		schema.WithEnvironment(environment),
	}

	c, err := schema.New(options...)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("deploy: reading the configuration: %w", err)
	}

	obj, _, err := c.Compile(context.Background())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("deploy: compiling %s: %w", environment, err)
	}

	flat := obj.Flat()
	compiled, err := json.Marshal(flat)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("deploy: %w", err)
	}

	p, err := specs.Load(compiled)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("deploy: reading back what was compiled: %w", err)
	}
	return compiled, p, flat, nil
}

// cut asks this cloud's Regions where a deployment runs and cuts it into one view
// per served region. What cannot be cut is refused before anything publishes;
// everything short of that goes to the build log.
func (d *Deploy) cut(flat map[string]any, environment string, out run.Output) ([]register.View, error) {
	views, findings, err := schema.Views(flat, schema.WithRegions(d.regions), schema.AuditIn(environment))
	var refused []string
	for _, f := range findings {
		if f.Severity == schema.Broken {
			refused = append(refused, f.String())
			continue
		}
		say(out, "%s: %s", environment, f)
	}
	if len(refused) > 0 || err != nil {
		if len(refused) == 0 {
			refused = append(refused, err.Error())
		}
		return nil, fmt.Errorf("%w: %s: %s", ErrBroken, environment, strings.Join(refused, "; "))
	}

	regions := make([]string, 0, len(views))
	for region := range views {
		regions = append(regions, region)
	}
	sort.Strings(regions)
	cut := make([]register.View, 0, len(regions))
	for _, region := range regions {
		v := views[region]
		b, err := json.Marshal(v.Object)
		if err != nil {
			return nil, fmt.Errorf("deploy: %w", err)
		}
		cut = append(cut, register.View{Destination: region, Compiled: b})
		if v.Empty() {
			say(out, "%s in %s: nothing placed", environment, region)
		} else {
			say(out, "%s in %s: %d resources", environment, region, len(v.Roles))
		}
	}
	return cut, nil
}

// sameViews is whether the current manifest lists exactly these views, so an
// unchanged push moves no pointer while a changed set of destinations does.
func sameViews(ctx context.Context, r Register, projectID, environment string, views []register.View) (bool, error) {
	m, _, err := r.Manifest(ctx, projectID, environment)
	switch {
	case errors.Is(err, register.ErrNotPublished):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("deploy: reading what %s/%s lists: %w", projectID, environment, err)
	}
	if m.Whole != (views == nil) || len(m.Views) != len(views) {
		return false, nil
	}
	for _, v := range views {
		if id, ok := m.Views[v.Destination]; !ok || id != register.IDOf(v.Compiled) {
			return false, nil
		}
	}
	return true, nil
}

// hostsOf is the fqdns a deployment answers on, and the generated ones that could
// not be completed. A zone's apex is included: it is how a DNS query finds the
// project's records.
func hostsOf(p *specs.Project, cloudFqdn string) (hosts, incomplete []string) {
	for _, h := range p.Hosts() {
		if !h.Generated {
			hosts = append(hosts, h.Fqdn)
			continue
		}
		if cloudFqdn == "" {
			incomplete = append(incomplete, h.Fqdn)
			continue
		}
		hosts = append(hosts, h.Fqdn+"."+cloudFqdn)
	}

	// Never generated: a zone is the author's, delegated here.
	for _, z := range p.Zones() {
		hosts = append(hosts, z.Fqdn)
	}
	return hosts, incomplete
}

// say writes one line of the build log. A write that fails is not a build that
// failed: the log is somebody watching, and the deploy is the point.
func say(out io.Writer, format string, args ...any) {
	fmt.Fprintf(out, format+"\n", args...)
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
