// Package artifact is what a push to a code repository does: it builds what the
// already-published configuration says this repository makes, and hands each
// artifact to the job's output.
//
// It reads the config as deployed, from the register, not git's HEAD: the branch
// resolves to deployments through the published environment table, and each
// deployment's compiled object gives the recipes. A never-deployed project is
// refused. Build stanzas are per environment, so each artifact is handed over
// with the deployment it was built for.
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
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/services/build/backend"
)

// branchRef is the only ref shape a build answers to, for the same reason a
// deploy does: nothing in an environment table matches a tag.
const branchRef = "refs/heads/"

// buildTime is what a variable's `at` says when it belongs to the build rather
// than to the workload.
const buildTime = specs.VariablesEntryAtBuild

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

	ErrNoSecrets = errors.New("artifact: this build needs a secret and this cloud has nothing to resolve one with")
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

// Secrets resolves a build-time secret, the same shape services/worker resolves a
// run-time one with. Optional: without it, builds needing a secret are refused by
// name.
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
	// Image is the author's toolchain. A build with no image is refused, never
	// defaulted.
	Image string

	// Dir holds the whole checkout, not just the resource's path, because a monorepo
	// resource reaches its siblings (path dependencies, replaces, workspaces).
	Dir string

	// Path is the resource's own directory within Dir, where the commands run
	// and Output is read from. Empty is the checkout root.
	Path string

	// Commands run in order, each in a shell, and the first that fails ends
	// the build.
	Commands []string

	// Variables are the author's build-time environment.
	Variables map[string]string

	// Secrets are the build-time variables whose values came out of a vault.
	// They reach the build exactly as Variables do, and are kept apart so a
	// recipe can be named without its credentials in the name.
	Secrets map[string]string

	// Output is what to collect afterwards, relative to Path. It is a file for
	// a wasm component and a js bundle, and a directory for a website.
	Output string

	// Runtime is the tier the resource declares it runs on, when it declares
	// one. A js bundle is not handed over as the build left it: the platform
	// packs it into the archive that tier reads (see pack.go).
	Runtime string
}

// Digest names what the recipe is, so a developer's build and a cloud's are known
// to be one recipe. A secret is named, never its value: a digest in a job log must
// not let a reader test guesses.
func (s Spec) Digest() string {
	secrets := make([]string, 0, len(s.Secrets))
	for name := range s.Secrets {
		secrets = append(secrets, name)
	}
	sort.Strings(secrets)

	type digestInput struct {
		Image     string            `json:"image"`
		Path      string            `json:"path"`
		Commands  []string          `json:"commands"`
		Variables map[string]string `json:"variables"`
		Secrets   []string          `json:"secrets"`
		Output    string            `json:"output"`
		Runtime   string            `json:"runtime,omitempty"`
	}
	named, _ := json.Marshal(digestInput{s.Image, filepath.ToSlash(s.Path), s.Commands, s.Variables, secrets, s.Output, s.Runtime})
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

	// Every spec is read before any build runs, so one unbuildable resource cannot
	// leave the others rebuilt and it stale.
	recipes := make([]Spec, len(work))
	for i, item := range work {
		spec, err := Recipe(ctx, b.secrets, w.ProjectID, item, dir)
		if err != nil {
			return err
		}
		recipes[i] = spec
	}

	for i, item := range work {
		say(out, "building %s/%s for %s (recipe %s)", item.Kind, item.Name, item.Environment, recipes[i].Digest())

		product, err := b.engine.Make(ctx, recipes[i], out)
		if errors.Is(err, ErrNoEngine) {
			// An engine with nothing to run this in declines rather than fails.
			say(out, "not built: %s/%s for %s: %v", item.Kind, item.Name, item.Environment, err)
			continue
		}
		if err != nil {
			return fmt.Errorf("artifact: building %s/%s for %s: %w", item.Kind, item.Name, item.Environment, err)
		}

		err = out.Asset(ctx, item.Environment, item.ID, product)
		product.Close()
		if err != nil {
			return fmt.Errorf("artifact: storing %s/%s for %s: %w", item.Kind, item.Name, item.Environment, err)
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

// Item is one build: a resource, in one deployment.
type Item struct {
	Environment string
	Scope       []string
	Kind        string
	Name        string
	ID          string
	Build       specs.Build
	Path        string
	Variables   []specs.Variable

	// Runtime is the tier the resource declares, "" for a kind with no choice.
	Runtime string
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

			// Refused: a delegated serving block the compile did not read would be served by
			// the block it replaces.
			if config := buildable.Source.Config; config != "" && !d.Project.Filled(buildable.ID) {
				return nil, fmt.Errorf("%w: %s/%s names source.config %q and the compile of %s did not read it",
					ErrUnfilled, buildable.Kind, buildable.Name, config, d.Environment)
			}

			out = append(out, Item{
				Environment: d.Environment,
				Scope:       buildable.Scope,
				Kind:        buildable.Kind,
				Name:        buildable.Name,
				ID:          buildable.ID,
				Build:       buildable.Build,
				Path:        buildable.Source.Path,
				Variables:   buildable.Variables,
				Runtime:     buildable.Runtime,
			})
		}
	}
	return out, nil
}

// backedBy reports whether a pushed repository is the one this resource is built
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
		return false, fmt.Errorf("artifact: %s/%s names %q, which is not a repository: %w",
			res.Kind, res.Name, res.Source.Git, err)
	}
	return key.String() == pushed, nil
}

// Recipe turns one item into the build it runs in checkout; secrets may be nil.
func Recipe(ctx context.Context, secrets Secrets, projectID string, i Item, checkout string) (Spec, error) {
	spec := Spec{
		Image:   i.Build.Image,
		Dir:     checkout,
		Path:    within(i.Path),
		Output:  i.Build.Output,
		Runtime: i.Runtime,
	}

	// Kind before image: a dockerfile build has no `image`.
	switch i.Build.Kind {
	case specs.BuildCommands:
		for _, command := range []string{i.Build.Install, i.Build.Run} {
			if command != "" {
				spec.Commands = append(spec.Commands, command)
			}
		}
	case specs.BuildScript:
		if i.Build.Script == "" {
			return Spec{}, fmt.Errorf("artifact: %s/%s is a script build that names no script", i.Kind, i.Name)
		}
		spec.Commands = []string{"sh " + i.Build.Script}
	default:
		// A dockerfile build produces an image, which needs a registry this cloud lacks.
		return Spec{}, fmt.Errorf("artifact: %s/%s is a %q build, and only %q and %q produce something this cloud can store",
			i.Kind, i.Name, i.Build.Kind, specs.BuildCommands, specs.BuildScript)
	}

	// Only variables with `at: build` reach the build: no runtime secret in someone
	// else's toolchain container.
	for _, v := range i.Variables {
		if v.At != buildTime {
			continue
		}
		if !v.FromSecret() {
			if spec.Variables == nil {
				spec.Variables = map[string]string{}
			}
			spec.Variables[v.Name] = v.Value
			continue
		}

		// No resolver means the build is refused, not run without its secret.
		if secrets == nil {
			return Spec{}, fmt.Errorf("%w: %s/%s's %s wants %s",
				ErrNoSecrets, i.Kind, i.Name, v.Name, v.Secret)
		}

		// The item's environment: each deployment builds with its own secret.
		value, err := secrets.Secret(ctx, projectID, i.Environment, v.Secret)
		if err != nil {
			// No value in the message: this error reaches the job log.
			return Spec{}, fmt.Errorf("artifact: %s/%s's %s needs the secret %s in %s: %w",
				i.Kind, i.Name, v.Name, v.Secret, i.Environment, err)
		}
		if spec.Secrets == nil {
			spec.Secrets = map[string]string{}
		}
		spec.Secrets[v.Name] = value
	}

	if spec.Image == "" {
		return Spec{}, fmt.Errorf("artifact: %s/%s declares no build image, and this service does not pick one",
			i.Kind, i.Name)
	}
	if len(spec.Commands) == 0 {
		return Spec{}, fmt.Errorf("artifact: %s/%s declares a build that runs nothing", i.Kind, i.Name)
	}
	if spec.Output == "" {
		return Spec{}, fmt.Errorf("artifact: %s/%s declares a build that leaves nothing behind", i.Kind, i.Name)
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
