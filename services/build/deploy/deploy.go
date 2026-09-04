// Package deploy is what a push to a project's CONFIGURATION repository does.
//
// # The register is written here and nowhere else
//
// tau is git-native: the config repository is the source of truth and a deploy
// is a commit. So the only thing that writes the register is the build service
// acting on a push that arrived through the frontend, was authorized there,
// and became a job with a record and a log somebody can watch. Every other
// composition in this tree reads the register and cannot publish to it —
// services/v2/node/locate.go says so in as many words.
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
// # What it is NOT
//
// This runs the CONFIGURATION repository's pushes. A push to a code repository
// is a build that produces artifacts, and that needs an asset store this tree
// does not have yet (backend.Assets). Such a job is refused BY NAME rather than
// silently succeeding, because a deploy that quietly built nothing is worse
// than one that did not run.
package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/afero"

	"github.com/taubyte/tau/pkg/project"
	"github.com/taubyte/tau/pkg/register"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/services/build/backend"
)

// branchRef is the only ref shape a deploy answers to. A tag or a note is not a
// deployment: nothing in the environment table matches one, so it would resolve
// to no deployments and fail as if the project were misconfigured.
const branchRef = "refs/heads/"

var (
	// ErrNotConfiguration is a push to a repository that is not the project's
	// configuration. It names the job rather than passing it: a code build
	// needs an artifact store, and one that returned success having produced
	// nothing would look exactly like a deploy that worked.
	ErrNotConfiguration = errors.New("deploy: this repository is not the project's configuration, and building code needs an artifact store this cloud has not been given")

	// ErrNotABranch is a ref that is not a branch.
	ErrNotABranch = errors.New("deploy: only a branch is a deployment")

	// ErrNoDeployment is a branch no deployment claims.
	ErrNoDeployment = errors.New("deploy: no deployment is fed by that branch")

	ErrNoRegister = errors.New("deploy: nowhere to publish to")
	ErrNoCheckout = errors.New("deploy: no way to read a repository")
	ErrNoProjects = errors.New("deploy: no way to tell a configuration repository from a code one")
)

// Projects answers what a repository IS to a project.
//
// It is one question and not auth's whole surface, because that is all this
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

// Register is the slice of the register a deploy writes: read what is there,
// then publish.
type Register interface {
	Resolve(ctx context.Context, project, environment string) (register.Pointer, error)
	Publish(ctx context.Context, p register.Publication) (register.ObjectID, error)
}

// Deploy is a backend.Runner.
type Deploy struct {
	projects Projects
	checkout Checkout

	// register is looked up per run rather than held, for the same reason
	// services/v2/node does it: which node answers is a routing question, and a
	// register can go away between two builds.
	register func(context.Context) (Register, error)

	// cloudFqdn is the zone this cloud serves. It completes the generated half
	// of a name — `<label>.<project label>` in the config, the rest here — and
	// a name that cannot be completed is reported rather than published
	// half-formed, because a partial name in a routing table is one no request
	// will ever carry.
	cloudFqdn string
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

	return &Deploy{
		projects:  o.projects,
		checkout:  o.checkout,
		register:  o.register,
		cloudFqdn: o.cloudFqdn,
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
		return ErrNotConfiguration
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
		project *project.Project
	}
	plan := make([]compiled, 0, len(names))
	for _, name := range names {
		bytes, p, err := compileFor(fs, dir, branch, name)
		if err != nil {
			return err
		}
		plan = append(plan, compiled{name: name, bytes: bytes, project: p})
		say(out, "compiled %s", name)
	}

	r, err := d.register(ctx)
	if err != nil {
		return err
	}

	for _, c := range plan {
		hosts, incomplete := hostsOf(c.project, d.cloudFqdn)

		object, revision, unchanged, err := d.put(ctx, r, w.ProjectID, c.name, c.bytes, hosts)
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
	return nil
}

// put publishes one deployment and says what it did.
//
// WHAT IS ALREADY THERE DECIDES THE REVISION, and whether there is anything to
// do at all: a compiled project is content-addressed, so a push that changed
// nothing this deployment reads produces the same object id, and moving the
// pointer for it would churn every reader to say the same thing.
func (d *Deploy) put(ctx context.Context, r Register, projectID, environment string,
	compiled []byte, hosts []string) (register.ObjectID, uint64, bool, error) {

	var revision uint64
	current, err := r.Resolve(ctx, projectID, environment)
	switch {
	case err == nil:
		if current.Object == register.IDOf(compiled) {
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
func compileFor(fs afero.Fs, dir, branch, environment string) ([]byte, *project.Project, error) {
	c, err := schema.New(
		schema.WithVirtual(fs, dir),
		schema.WithProjectEnvironments(fs, dir),
		schema.WithBranch(branch),
		schema.WithEnvironment(environment),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("deploy: reading the configuration: %w", err)
	}

	obj, _, err := c.Compile(context.Background())
	if err != nil {
		return nil, nil, fmt.Errorf("deploy: compiling %s: %w", environment, err)
	}

	compiled, err := json.Marshal(obj.Flat())
	if err != nil {
		return nil, nil, fmt.Errorf("deploy: %w", err)
	}

	p, err := project.Load(compiled)
	if err != nil {
		return nil, nil, fmt.Errorf("deploy: reading back what was compiled: %w", err)
	}
	return compiled, p, nil
}

// hostsOf is the fully-qualified names a deployment answers on, and the
// generated ones that could not be completed.
func hostsOf(p *project.Project, cloudFqdn string) (hosts, incomplete []string) {
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
