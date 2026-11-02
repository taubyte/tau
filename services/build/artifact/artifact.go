// Package artifact is what a push to a code repository does: it builds what the
// already-published configuration says this repository makes, and hands each
// artifact to the job's output.
//
// It reads the config as deployed, from the register, not git's HEAD: the branch
// resolves to deployments through the published environment table, and each
// deployment's compiled object gives the recipes. A never-deployed project is
// refused. Only a package builds, and what it builds is per environment, so each
// artifact is handed over with the deployment it was built for.
package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/services/build/backend"
)

// branchRef is the only ref shape a build answers to, for the same reason a
// deploy does: nothing in an environment table matches a tag.
const branchRef = "refs/heads/"

var (
	// ErrNeverDeployed is a code push for a project whose configuration has never
	// been published: there is no recipe to build against.
	ErrNeverDeployed = errors.New("artifact: this project has never been deployed, so there is no configuration to build against; push its configuration first")

	// ErrNoDeployment is a branch no deployment claims.
	ErrNoDeployment = errors.New("artifact: no deployment is fed by that branch")

	// ErrNothingToBuild is a push to a repository these deployments do not build
	// from. It is reported and the job succeeds.
	ErrNothingToBuild = errors.New("artifact: nothing in these deployments is built from that repository")

	ErrNotABranch = errors.New("artifact: only a branch is a deployment")
	ErrNoRegister = errors.New("artifact: nothing to read the configuration from")
	ErrNoCheckout = errors.New("artifact: no way to read a repository")
	ErrNoProjects = errors.New("artifact: no way to tell which repository backs a project's code")
	// ErrNoEngine is nothing to run a build in; the build is reported unbuilt rather
	// than failed.
	ErrNoEngine = errors.New("artifact: nothing to run a build in")

	// ErrUnfilled is a resource delegating its serving block to a file in its own
	// repository, compiled without reading it; building it would publish the config
	// repository's block instead.
	ErrUnfilled = errors.New("artifact: this resource's serving block is delegated to a file the compile did not read")

	// ErrNotBuiltHere is a package asking for something this cloud's builds do not do
	// yet, refused by name rather than built without it.
	ErrNotBuiltHere = errors.New("artifact: this cloud does not build that yet")
)

// Register is the read a build makes of what is deployed.
type Register interface {
	Environments(ctx context.Context, project string) ([]string, error)
	Resolve(ctx context.Context, project, environment string) (register.Pointer, error)
	Object(ctx context.Context, id register.ObjectID) ([]byte, error)
}

// Projects says which repository a bare path is in: the project's main
// (configuration) repository, which only the register's project table knows.
type Projects interface {
	MainRepository(ctx context.Context, projectID string) (string, error)
}

// Secrets resolves a secret for a build's vault, the same shape services/worker
// resolves a run-time one with.
type Secrets interface {
	Secret(ctx context.Context, project, environment, secretID string) (string, error)
}

// Checkout puts one commit of a repository on this machine. It is the same seam
// a deploy uses, and for the same reason it hands back a directory: a build
// bind-mounts one.
type Checkout interface {
	Checkout(ctx context.Context, repositoryKey, branch, commit string) (dir string, done func(), err error)
}

// Spec is one build to run.
type Spec struct {
	// Dir holds the whole checkout, not just the package's path, because a monorepo
	// package reaches its siblings (path dependencies, replaces, workspaces).
	Dir string

	// Path is the package's own directory within Dir, where every step runs and
	// Output is read from. Empty is the checkout root.
	Path string

	// Steps run in order over the one checkout, each in its own toolchain image,
	// and the first that fails ends the build.
	Steps []Step

	// Variables are the package's values, every step's environment.
	Variables map[string]string

	// Output is what to collect afterwards, relative to Path. It is a file for a
	// wasm component and a directory for a static site.
	Output string

	// Type is the package's. A warp bundle is not handed over as the build left it:
	// the platform packs it into the archive that tier reads (see pack.go).
	Type string
}

// Step is one toolchain image and the commands run in it, in one shell.
type Step struct {
	Name     string
	Image    string
	Commands []string
}

// Digest names what the recipe is, so a developer's build and a cloud's are known
// to be one recipe.
func (s Spec) Digest() string {
	type digestInput struct {
		Path      string            `json:"path"`
		Steps     []Step            `json:"steps"`
		Variables map[string]string `json:"variables"`
		Output    string            `json:"output"`
		Type      string            `json:"type,omitempty"`
	}
	named, _ := json.Marshal(digestInput{filepath.ToSlash(s.Path), s.Steps, s.Variables, s.Output, s.Type})
	sum := sha256.Sum256(named)
	return hex.EncodeToString(sum[:])
}

// Engine runs one build, a seam so the planning logic is testable without a
// container daemon.
type Engine interface {
	// Make runs the spec, streaming the build's own output to log, and returns
	// what it produced. The caller closes it.
	Make(ctx context.Context, spec Spec, log io.Writer) (io.ReadCloser, error)
}

// Keys turns a clone URL into the repository key it is filed under, applying a
// forge's own spellings. Without one, a URL keys by repokey's parse.
type Keys interface {
	Key(cloneURL string) (repokey.Key, error)
}

// Builder is a code repository's half of a push.
type Builder struct {
	register Register
	projects Projects
	checkout Checkout
	engine   Engine
	secrets  Secrets
	keys     Keys
}

// New builds one.
func New(opts ...Option) (*Builder, error) {
	var o options
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return nil, fmt.Errorf("artifact: option: %w", err)
		}
	}

	switch {
	case o.register == nil:
		return nil, ErrNoRegister
	case o.projects == nil:
		return nil, ErrNoProjects
	case o.checkout == nil:
		return nil, ErrNoCheckout
	case o.engine == nil:
		return nil, ErrNoEngine
	}

	return &Builder{
		register: o.register, projects: o.projects,
		checkout: o.checkout, engine: o.engine, secrets: o.secrets,
		keys: o.keys,
	}, nil
}

// Build runs every build this push touches.
func (b *Builder) Build(ctx context.Context, w backend.Work, out backend.Output) error {
	branch, ok := strings.CutPrefix(w.Ref, branchRef)
	if !ok || branch == "" {
		return fmt.Errorf("%w: %s", ErrNotABranch, w.Ref)
	}

	deployments, err := b.deployments(ctx, w.ProjectID, branch)
	if err != nil {
		return err
	}

	main, err := b.projects.MainRepository(ctx, w.ProjectID)
	if err != nil {
		return fmt.Errorf("artifact: %w", err)
	}

	compiled := make([]Deployment, 0, len(deployments))
	for _, environment := range deployments {
		p, err := b.projectAt(ctx, w.ProjectID, environment)
		if err != nil {
			return err
		}
		compiled = append(compiled, Deployment{Environment: environment, Project: p})
	}

	// WHAT TO BUILD IS DECIDED BEFORE ANYTHING IS CLONED, so a push that
	// touches nothing costs no network and says so.
	work, err := Plan(compiled, func(res specs.Buildable) (bool, error) {
		return b.backedBy(res, w.RepositoryKey, main)
	})
	if err != nil {
		return err
	}
	if len(work) == 0 {
		say(out, "nothing in %s is built from %s", strings.Join(deployments, ", "), w.RepositoryKey)
		return nil
	}

	dir, release, err := b.checkout.Checkout(ctx, w.RepositoryKey, branch, w.CommitSHA)
	if err != nil {
		return fmt.Errorf("artifact: reading %s at %s: %w", w.RepositoryKey, short(w.CommitSHA), err)
	}
	defer release()

	// Every spec is read before any build runs, so one unbuildable package cannot
	// leave the others rebuilt and it stale.
	recipes := make([]Spec, len(work))
	for i, item := range work {
		spec, err := Recipe(item, dir)
		if err != nil {
			return err
		}
		recipes[i] = spec
	}

	for i, item := range work {
		say(out, "building package/%s for %s (recipe %s)", item.Name, item.Environment, recipes[i].Digest())

		product, err := b.engine.Make(ctx, recipes[i], out)
		if errors.Is(err, ErrNoEngine) {
			// An engine with nothing to run this in declines rather than fails.
			say(out, "not built: package/%s for %s: %v", item.Name, item.Environment, err)
			continue
		}
		if err != nil {
			return fmt.Errorf("artifact: building package/%s for %s: %w", item.Name, item.Environment, err)
		}

		err = out.Asset(ctx, item.Environment, item.ID, product)
		product.Close()
		if err != nil {
			return fmt.Errorf("artifact: storing package/%s for %s: %w", item.Name, item.Environment, err)
		}
	}
	return nil
}

// deployments is which of a project's environments this branch feeds, read from
// any one published object since the environment table is the same in all.
func (b *Builder) deployments(ctx context.Context, projectID, branch string) ([]string, error) {
	published, err := b.register.Environments(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("artifact: %w", err)
	}
	if len(published) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNeverDeployed, projectID)
	}

	p, err := b.projectAt(ctx, projectID, published[0])
	if err != nil {
		return nil, err
	}

	declared := p.Deployments(branch)
	if len(declared) == 0 {
		return nil, fmt.Errorf("%w: %q feeds none of %s",
			ErrNoDeployment, branch, strings.Join(published, ", "))
	}

	// Only published deployments: an artifact for an undeployed one would sit under a
	// pointer no worker reads.
	live := make(map[string]bool, len(published))
	for _, name := range published {
		live[name] = true
	}

	out := make([]string, 0, len(declared))
	for _, name := range declared {
		if live[name] {
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: %q feeds %s, and none of those is deployed",
			ErrNoDeployment, branch, strings.Join(declared, ", "))
	}
	return out, nil
}

// Deployment is one environment's compiled project, published for a push or
// compiled locally for `tau build`.
type Deployment struct {
	Environment string
	Project     *specs.Project
}

// Item is one build: a package, in one deployment.
type Item struct {
	Environment string
	Scope       []string
	Name        string
	ID          string
	Package     specs.Package
	Path        string
	Variables   []specs.Variable
}

// Plan is every build the deployments owe that backs says is at hand, exported so
// a cloud's build and a developer's are one decision.
func Plan(deployments []Deployment, backs func(specs.Buildable) (bool, error)) ([]Item, error) {
	var out []Item

	for _, d := range deployments {
		for _, buildable := range d.Project.Buildables() {
			backed, err := backs(buildable)
			if err != nil {
				return nil, err
			}
			if !backed {
				continue
			}

			// Refused: a build delegated to the package's own repository that the
			// compile did not read would build what the config repository says instead.
			if config := buildable.Source.Config; config != "" && !d.Project.Filled(buildable.ID) {
				return nil, fmt.Errorf("%w: package/%s names source.config %q and the compile of %s did not read it",
					ErrUnfilled, buildable.Name, config, d.Environment)
			}

			out = append(out, Item{
				Environment: d.Environment,
				Scope:       buildable.Scope,
				Name:        buildable.Name,
				ID:          buildable.ID,
				Package:     buildable.Package,
				Path:        buildable.Source.Path,
				Variables:   buildable.Variables,
			})
		}
	}
	return out, nil
}

// backedBy reports whether a pushed repository is the one this package is built
// from: a bare path is the project's main repository, otherwise the source names
// it.
func (b *Builder) backedBy(res specs.Buildable, pushed, main string) (bool, error) {
	if res.Source.Git == "" {
		return main != "" && main == pushed, nil
	}

	parse := repokey.Parse
	if b.keys != nil {
		parse = b.keys.Key
	}
	key, err := parse(res.Source.Git)
	if err != nil {
		// An unparseable clone URL is an error, not a skip that silently stops building.
		return false, fmt.Errorf("artifact: package/%s names %q, which is not a repository: %w",
			res.Name, res.Source.Git, err)
	}
	return key.String() == pushed, nil
}

// Recipe turns one item into the build it runs in checkout.
func Recipe(i Item, checkout string) (Spec, error) {
	build := i.Package.Build
	switch {
	case build.Kind == specs.PackageBuildKindDockerfile:
		// A Dockerfile produces an image, which needs a registry this cloud lacks.
		return Spec{}, fmt.Errorf("%w: package/%s is built from a Dockerfile, and storing an image needs a registry",
			ErrNotBuiltHere, i.Name)
	case len(build.Inputs) > 0:
		// Refused rather than built without them: a build that reads .inputs/<name>
		// would fail, or quietly build without what it embeds.
		return Spec{}, fmt.Errorf("%w: package/%s reads other packages' artifacts as inputs",
			ErrNotBuiltHere, i.Name)
	}

	spec := Spec{
		Dir:    checkout,
		Path:   within(i.Path),
		Output: i.Package.Output,
		Type:   i.Package.Type,
	}
	for _, step := range build.Steps {
		if step.Image == "" {
			return Spec{}, fmt.Errorf("artifact: package/%s's step %q names no toolchain image, and this service does not pick one",
				i.Name, step.Name)
		}
		spec.Steps = append(spec.Steps, Step{Name: step.Name, Image: step.Image, Commands: step.Commands})
	}
	for _, v := range i.Variables {
		if spec.Variables == nil {
			spec.Variables = map[string]string{}
		}
		spec.Variables[v.Name] = v.Value
	}

	if len(spec.Steps) == 0 {
		return Spec{}, fmt.Errorf("artifact: package/%s declares a build that runs nothing", i.Name)
	}
	if spec.Output == "" {
		return Spec{}, fmt.Errorf("artifact: package/%s declares a build that leaves nothing behind", i.Name)
	}
	return spec, nil
}

// projectAt reads one deployment's compiled project.
func (b *Builder) projectAt(ctx context.Context, projectID, environment string) (*specs.Project, error) {
	pointer, err := b.register.Resolve(ctx, projectID, environment)
	if err != nil {
		return nil, fmt.Errorf("artifact: reading what %s/%s runs: %w", projectID, environment, err)
	}

	compiled, err := b.register.Object(ctx, pointer.Object)
	if err != nil {
		return nil, fmt.Errorf("artifact: reading %s: %w", pointer.Object, err)
	}

	p, err := specs.Load(compiled)
	if err != nil {
		return nil, fmt.Errorf("artifact: reading %s/%s's configuration: %w", projectID, environment, err)
	}
	return p, nil
}

func say(out io.Writer, format string, args ...any) {
	fmt.Fprintf(out, format+"\n", args...)
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
