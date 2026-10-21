// Package deploy is what a push to a project's CONFIGURATION repository does.
//
// # The register is written here and nowhere else
//
// tau is git-native: the config repository is the source of truth and a deploy
// is a commit. So the only thing that writes the register is the build service
// acting on a push that arrived through the frontend, was authorized there,
// and became a job with a record and a log somebody can watch. Every other
// composition in this tree reads the register and cannot publish to it —
// node/locate.go says so in as many words.
//
// A person running a publish command against a swarm would go around all of
// that: no commit, no authorization, no record, and a fleet whose state no
// longer matches any git history.
//
// # The branch is the deployment
//
// A project declares which branches feed which of its deployments, so the ref
// that was pushed already answers "deploy where". `main` feeds production and
// the shadow written inside it; `release/*` feeds staging; anything else is a
// preview. Nobody types an environment, and nobody types a revision either:
// the register refuses one that does not move forward, so this reads what is
// published and adds one.
//
// # Each deployment is its own compile
//
// Fifty-one attributes may be written per deployment — every ceiling, every
// limit, the toolchain a build runs — and the compiler resolves `<key>_<env>`
// down to `<key>` for the one it is compiling. One object published under
// several names would give every deployment production's numbers.
//
// # It routes; it does not do both
//
// A push is one of two things and this is where that is decided, because the
// decision is one question to the register's project table and both answers need it. A push to the
// configuration repository is handled here. A push to a code repository is a
// BUILD — services/build/artifact — and is delegated when this was given one.
//
// A node with no builder refuses a code push BY NAME rather than succeeding
// quietly, because a job that reported success having built nothing looks
// exactly like a deploy that worked.
package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/afero"

	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/services/build/backend"
)

// branchRef is the only ref shape a deploy answers to. A tag or a note is not a
// deployment: nothing in the environment table matches one, so it would resolve
// to no deployments and fail as if the project were misconfigured.
const branchRef = "refs/heads/"

var (
	// ErrNotConfiguration is a push to a repository that is not the project's
	// configuration, on a node that was given nothing to build code with. It
	// names the job rather than passing it: one that returned success having
	// produced nothing would look exactly like a deploy that worked.
	ErrNotConfiguration = errors.New("deploy: this repository is not the project's configuration, and this node was not given anything to build code with")

	// ErrNotABranch is a ref that is not a branch.
	ErrNotABranch = errors.New("deploy: only a branch is a deployment")

	// ErrNoDeployment is a branch no deployment claims.
	ErrNoDeployment = errors.New("deploy: no deployment is fed by that branch")

	ErrNoRegister = errors.New("deploy: nowhere to publish to")
	ErrNoCheckout = errors.New("deploy: no way to read a repository")
	ErrNoProjects = errors.New("deploy: no way to tell a configuration repository from a code one")
	// ErrUnplaced is a deployment this cloud cannot place where it says it runs.
	// ErrNotClaimed is a configuration whose project.yaml names an id the
	// pushed repository has not claimed. The register's project table is the
	// one thing that says which repository may publish under an id, and this
	// is the read of it at publish time: a config copied from another project
	// keeps that project's id, and publishing it would move that project's
	// pointers.
	ErrNotClaimed = errors.New("deploy: the project id is not claimed by this repository")

	ErrUnplaced = errors.New("deploy: this cloud cannot place it")

	// ErrUnproven is a zone the project has not proven is its own, by the TXT
	// record `tau domain claim` prints. A deploy that published it would let
	// any project answer for any domain.
	ErrUnproven = errors.New("deploy: a custom domain is not proven to be the project's")
)

// Domains proves a custom domain is a project's. services/cm's Domains is one.
type Domains interface {
	Prove(ctx context.Context, project, fqdn string) error
}

// Projects answers what a repository IS to a project.
//
// It is one question and not the project table's whole surface, because that is all this
// needs and a wider seam would be a wider thing to stand up in a test.
type Projects interface {
	// Configures reports whether this repository is the project's
	// configuration — the one whose pushes are deploys.
	Configures(ctx context.Context, projectID, repositoryKey string) (bool, error)
}

// Checkout puts one commit of a repository somewhere readable.
//
// IT TAKES THE COMMIT AND NOT ONLY THE BRANCH. A branch moves; the job is about
// one commit, and a deploy that read whatever the branch pointed at by the time
// it ran would publish something no push ever asked for.
//
// IT HANDS BACK A DIRECTORY ON THIS MACHINE, not a filesystem abstraction, and
// that is a deliberate narrowing: the other thing a push becomes is a BUILD,
// which runs the repository's own commands in a container against a bind-mounted
// directory. There is no way to bind-mount an afero.Fs, so a seam that returned
// one would be a seam only half of its callers could use.
type Checkout interface {
	// Checkout returns the directory holding the tree at `commit`, plus a
	// function that releases whatever it took.
	Checkout(ctx context.Context, repositoryKey, branch, commit string) (dir string, done func(), err error)
}

// Builder is what a push to a CODE repository becomes, and what a push to the
// configuration repository becomes too once it is published: a resource whose
// source is a bare path is built from the configuration repository.
//
// It is an interface rather than the concrete builder so that this package
// does not import the one that runs containers — routing a push and running a
// build are different jobs, and only one of them needs a daemon.
type Builder interface {
	Build(ctx context.Context, w backend.Work, out backend.Output) error
}

// Register is the slice of the register a deploy writes: read what is there,
// then publish.
type Register interface {
	Resolve(ctx context.Context, project, environment string) (register.Pointer, error)
	ResolveView(ctx context.Context, project, environment, destination string) (register.ViewPointer, error)
	Publish(ctx context.Context, p register.Publication) (register.ObjectID, error)
}

// Deploy is a backend.Runner.
type Deploy struct {
	projects Projects
	checkout Checkout

	// register is looked up per run rather than held, for the same reason
	// node/ does it: which node answers is a routing question, and a
	// register can go away between two builds.
	register func(context.Context) (Register, error)

	// builder runs a push to a code repository, when this node was given one.
	builder Builder

	// regions answers where a deployment runs; see WithRegions.
	regions schema.Regions

	// cloudFqdn is the zone this cloud serves. It completes the generated half
	// of a name — `<label>.<project label>` in the config, the rest here — and
	// a name that cannot be completed is reported rather than published
	// half-formed, because a partial name in a routing table is one no request
	// will ever carry.
	cloudFqdn string

	domains Domains
}

var _ backend.Runner = (*Deploy)(nil)

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
		regions = schema.NoRegion()
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

// Run publishes what the pushed commit compiles to.
//
// EVERY DEPLOYMENT COMPILES BEFORE ANY ONE PUBLISHES. A project that compiles
// for production and not for preview must not go half live, and a compile
// error is the runner's error either way — the queue's retry exists for a
// worker that died, not for config that does not compile.
func (d *Deploy) Run(ctx context.Context, w backend.Work, out backend.Output) error {
	branch, ok := strings.CutPrefix(w.Ref, branchRef)
	if !ok || branch == "" {
		return fmt.Errorf("%w: %s", ErrNotABranch, w.Ref)
	}

	configures, err := d.projects.Configures(ctx, w.ProjectID, w.RepositoryKey)
	if err != nil {
		return fmt.Errorf("deploy: %w", err)
	}
	if !configures {
		// A CODE PUSH IS SOMEBODY ELSE'S JOB, and this is the only place that
		// knows which of the two it is — the question costs one read of the project table
		// and both answers need it, so asking it twice would be asking it
		// twice.
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

	fs := afero.NewOsFs()
	names, err := deployments(fs, dir, branch)
	if err != nil {
		return err
	}
	say(out, "%s feeds %s", branch, strings.Join(names, ", "))

	type compiled struct {
		name    string
		bytes   []byte
		project *specs.Project
		views   []register.View
	}
	plan := make([]compiled, 0, len(names))
	for _, name := range names {
		bytes, p, flat, err := compileFor(fs, dir, branch, name)
		if err != nil {
			return err
		}
		views, err := d.cut(flat, name, out)
		if err != nil {
			return err
		}
		plan = append(plan, compiled{name: name, bytes: bytes, project: p, views: views})
		say(out, "compiled %s", name)
	}

	// THE ID IS THE AUTHOR'S, AND THE CLAIM IS WHAT LETS IT PUBLISH. w.ProjectID
	// is the project this repository is linked as configuration of; every
	// compiled deployment must name exactly that id, or it is refused before
	// anything is written.
	for _, c := range plan {
		if id := c.project.ID(); id != w.ProjectID {
			return fmt.Errorf("%w: project.yaml names %q, and %s is linked as the configuration of %q — run `tau link` for this id, or put %q back",
				ErrNotClaimed, id, w.RepositoryKey, w.ProjectID, w.ProjectID)
		}
	}

	proven := map[string]bool{}
	for _, c := range plan {
		for _, z := range c.project.Zones() {
			if err := d.prove(ctx, w.ProjectID, z.Fqdn, proven); err != nil {
				return err
			}
		}
	}

	r, err := d.register(ctx)
	if err != nil {
		return err
	}

	for _, c := range plan {
		hosts, incomplete := hostsOf(c.project, d.cloudFqdn)

		object, revision, unchanged, err := d.put(ctx, r, w.ProjectID, c.name, c.bytes, c.views, hosts)
		if err != nil {
			return err
		}
		if unchanged {
			say(out, "%s unchanged at revision %d (%s)", c.name, revision, object)
		} else {
			say(out, "%s published as revision %d (%s)", c.name, revision, object)
		}

		for _, host := range hosts {
			say(out, "    %s", host)
		}
		for _, host := range incomplete {
			say(out, "    %s.<cloud fqdn> — not published; this cloud was not told its own zone", host)
		}
	}

	// A BARE PATH IS IN THIS REPOSITORY, so what it backs is built from this
	// commit — after publishing, because the builder reads what to build from
	// the deployments just published.
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

// put publishes one deployment and says what it did.
//
// WHAT IS ALREADY THERE DECIDES THE REVISION, and whether there is anything to
// do at all: a compiled project is content-addressed, so a push that changed
// nothing this deployment reads produces the same object id, and moving the
// pointer for it would churn every reader to say the same thing.
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

// deployments is which of a project's environments this push feeds.
func deployments(fs afero.Fs, dir, branch string) ([]string, error) {
	declared, err := schema.EnvironmentsOf(fs, dir)
	if err != nil {
		return nil, fmt.Errorf("deploy: reading the environment table: %w", err)
	}

	// A project with no table has exactly one deployment and no name for it in
	// the document, so this names it — and it is the only place that may,
	// because the name is a key in the register that must be the same on every
	// push.
	if len(declared) == 0 {
		return []string{DefaultEnvironment}, nil
	}

	fed := schema.DeploymentNames(declared, branch)
	if len(fed) == 0 {
		return nil, fmt.Errorf("%w: %q feeds none of %s",
			ErrNoDeployment, branch, strings.Join(schema.EnvironmentNames(declared), ", "))
	}
	return fed, nil
}

// DefaultEnvironment names the one deployment of a project that declares no
// environment table.
const DefaultEnvironment = "prod"

// compileFor reads a config directory as ONE deployment sees it.
//
// THE DECLARED SET TRAVELS WITH THE ENVIRONMENT, because one document carries
// every deployment's spelling at once: without it, strict mode reads
// `timeout_preview` in production's compile as a typo and refuses the project
// outright — which is not a wrong value but no deploy at all.
//
// THE BYTES ARE JSON OF THE COMPILED OBJECT, which is what pkg/project reads
// back. Saying so in one place is what keeps the writer and the reader from
// disagreeing about a format nobody else names.
func compileFor(fs afero.Fs, dir, branch, environment string) ([]byte, *specs.Project, map[string]any, error) {
	declared, err := schema.EnvironmentsOf(fs, dir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("deploy: reading the environment table: %w", err)
	}

	options := []schema.Option{
		schema.WithVirtual(fs, dir),
		schema.WithEnvironments(schema.EnvironmentNames(declared)...),
		schema.WithBranch(branch),
	}
	// A PROJECT WITH NO TABLE HAS NOTHING TO BIND. DefaultEnvironment names that
	// project's one deployment in the register; it is not a value any document may
	// suffix a key with, and reading for a value the project never declared is
	// refused — rightly, because it would be a compile no override could reach.
	if len(declared) > 0 {
		options = append(options, schema.WithEnvironment(environment))
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

// cut asks this cloud's Regions where a compiled deployment runs, and cuts it into one
// view per region it serves — what each region reads, and nothing it would filter out.
//
// WHAT CANNOT BE CUT IS REFUSED BEFORE ANYTHING PUBLISHES, exactly as a compile error
// is: a resource with nowhere to run, or one reaching across regions to something held
// only in another, is a deployment that half works. Everything short of that is said in
// the build log, so a region a project names and this cloud does not serve is never
// silently dropped.
func (d *Deploy) cut(flat map[string]any, environment string, out backend.Output) ([]register.View, error) {
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
		return nil, fmt.Errorf("%w: %s: %s", ErrUnplaced, environment, strings.Join(refused, "; "))
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

// sameViews is whether every view of a deployment is what is already published, so a
// push that changed nothing any region reads moves no pointer — and a change in what
// this cloud serves, with the object the same, still does.
func sameViews(ctx context.Context, r Register, projectID, environment string, views []register.View) (bool, error) {
	for _, v := range views {
		current, err := r.ResolveView(ctx, projectID, environment, v.Destination)
		switch {
		case errors.Is(err, register.ErrNoView), errors.Is(err, register.ErrNotPublished):
			return false, nil
		case err != nil:
			return false, fmt.Errorf("deploy: reading what %s/%s holds in %s: %w", projectID, environment, v.Destination, err)
		}
		if current.Object != register.IDOf(v.Compiled) {
			return false, nil
		}
	}
	return true, nil
}

// hostsOf is the fully-qualified names a deployment answers on, and the
// generated ones that could not be completed.
//
// A ZONE'S APEX IS ONE OF THEM, and it is not a resource answering at a name —
// it is the name this project is AUTHORITATIVE for. It is published here
// because the index this feeds is what turns a query into a project, and
// without it nothing could find the records an author wrote: a zone that
// declares an MX would resolve to nobody, which is the whole of what
// "a zone served authoritatively by this cloud" promises.
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

	// NEVER GENERATED: a zone is one the author owns and delegated here. The
	// cloud's own zone is not declared as a resource at all — a name under it
	// is reached with host.generated, which the loop above already completed.
	for _, z := range p.Zones() {
		hosts = append(hosts, z.Fqdn)
	}
	return hosts, incomplete
}

// say writes one line of the build log. A write that fails is not a build that
// failed: the log is somebody watching, and the deploy is the point.
func say(out backend.Output, format string, args ...any) {
	fmt.Fprintf(out, format+"\n", args...)
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
