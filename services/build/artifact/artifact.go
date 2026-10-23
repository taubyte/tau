// Package artifact is what a push to a CODE repository does.
//
// # The other half of what a push can be
//
// A push to the project's configuration repository is a deploy: it compiles and
// publishes (services/build/deploy). A push to a code repository is this: it
// builds what the already-published configuration says this repository makes,
// and hands each artifact to the job's output.
//
// # It reads the config it was deployed with, not the config in git
//
// Everything this needs is already in the register. The push knows a project
// and a branch; the environment table survives the compile, so the branch
// resolves to its deployments from what was published, and each deployment's
// compiled object says which resources this repository backs, how each is made
// and what it leaves behind. No config clone, no second index, and — this is
// the point — the artifact is built from the recipe the running deployment
// actually declares rather than from whatever the config repository's HEAD says
// today.
//
// A project that has never been deployed is refused by name: there is no
// configuration to build against, and the fix is to push the configuration
// first.
//
// # One artifact per deployment, not per resource
//
// A build stanza is per-environment — the toolchain, both commands and where
// the product lands can all differ — so one resource can have a different
// artifact in preview than in production. Each artifact is therefore handed
// over WITH the deployment it was built for; what key those two compose is the
// backend's grammar, because a worker asking what a resource runs has to arrive
// at the same one.
package artifact

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	// ErrNeverDeployed is a code push for a project whose configuration has
	// never been published. There is nothing to build against, and building
	// against the repository's own guess would be this service inventing a
	// recipe.
	ErrNeverDeployed = errors.New("artifact: this project has never been deployed, so there is no configuration to build against; push its configuration first")

	// ErrNoDeployment is a branch no deployment claims.
	ErrNoDeployment = errors.New("artifact: no deployment is fed by that branch")

	// ErrNothingToBuild is a push to a repository the configuration does not
	// name. It is not a failure of the push — a repository can be registered to
	// a project and back nothing in the deployments this branch feeds — so it
	// is reported and the job succeeds.
	ErrNothingToBuild = errors.New("artifact: nothing in these deployments is built from that repository")

	ErrNotABranch = errors.New("artifact: only a branch is a deployment")
	ErrNoRegister = errors.New("artifact: nothing to read the configuration from")
	ErrNoCheckout = errors.New("artifact: no way to read a repository")
	ErrNoProjects = errors.New("artifact: no way to tell which repository backs a project's code")
	ErrNoEngine   = errors.New("artifact: nothing to run a build in")

	// ErrNoSecrets is a build whose author declared a build-time secret on a
	// cloud with no vault. It is a refusal rather than a build run without the
	// value: a build that quietly lost its token fails somewhere further in,
	// with a message about a dependency it could not fetch.
	// ErrUnfilled is a resource that delegates its serving block to a file in
	// its own repository, and whose deployment was compiled without reading
	// it. Building it would publish an artifact served by the config
	// repository's block instead of that file's.
	ErrUnfilled = errors.New("artifact: this resource's serving block is delegated to a file the compile did not read")

	ErrNoSecrets = errors.New("artifact: this build needs a secret and this cloud has nothing to resolve one with")
)

// Register is the read a build makes of what is deployed.
type Register interface {
	Environments(ctx context.Context, project string) ([]string, error)
	Resolve(ctx context.Context, project, environment string) (register.Pointer, error)
	Object(ctx context.Context, id register.ObjectID) ([]byte, error)
}

// Projects says which repository a bare path is in.
//
// IT IS ASKED RATHER THAN INFERRED because it is not in the config: a resource
// whose source is a bare path is in the project's MAIN repository — the
// configuration repository the project is claimed by — and which repository
// that IS is the register's project table to say. A code repository is built
// from only when a source names it by its git URL.
type Projects interface {
	MainRepository(ctx context.Context, projectID string) (string, error)
}

// Secrets resolves a build-time secret — the token a private dependency needs,
// the credential a registry wants.
//
// IT IS THE SAME SHAPE services/worker RESOLVES A RUN-TIME SECRET WITH, and
// deliberately: one vault, one method, two callers. What differs is WHICH
// variables reach it, and the config says so with `at`.
//
// OPTIONAL. A cloud with no vault builds everything that needs no secret and
// refuses, by name, the builds that do.
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
	// Image is the toolchain, and it is the author's — this service never
	// picks one. A build with no image is refused rather than defaulted,
	// because a default toolchain is a build that succeeds against something
	// nobody chose.
	Image string

	// Dir is the directory on this machine holding the source, already
	// narrowed to the resource's own path within the checkout.
	Dir string

	// Commands run in order, each in a shell, and the first that fails ends
	// the build.
	Commands []string

	// Variables are the author's build-time environment.
	Variables map[string]string

	// Output is what to collect afterwards, relative to Dir. It is a file for
	// a wasm component and a directory for a website.
	Output string
}

// Engine runs one build.
//
// IT IS A SEAM BECAUSE A CONTAINER DAEMON IS NOT A TEST DEPENDENCY. What this
// package decides — which resources a push touches, which deployments, what
// each build is handed — is a function of a compiled project, and proving it
// should not need docker.
type Engine interface {
	// Make runs the spec, streaming the build's own output to log, and returns
	// what it produced. The caller closes it.
	Make(ctx context.Context, spec Spec, log io.Writer) (io.ReadCloser, error)
}

// Keys turns a clone URL into the repository key it is filed under. A forge
// registry is one: it applies a forge's own spellings (an https path prefix,
// an ssh port or host of its own) so every address of one repository is one
// key. Without one, a URL keys by repokey's parse alone.
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

	// WHAT TO BUILD IS DECIDED BEFORE ANYTHING IS CLONED, so a push that
	// touches nothing costs no network and says so.
	work, err := b.plan(ctx, w.ProjectID, deployments, w.RepositoryKey, main)
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

	// EVERY SPEC IS READ BEFORE ANY BUILD RUNS, which is the rule a deploy
	// follows for the same reason: a push where one resource declares a build
	// this cloud cannot run must not leave the others rebuilt and that one
	// stale. A partially rebuilt deployment looks exactly like a healthy one.
	specs := make([]Spec, len(work))
	for i, item := range work {
		spec, err := b.specOf(ctx, w.ProjectID, item, dir)
		if err != nil {
			return err
		}
		specs[i] = spec
	}

	for i, item := range work {
		say(out, "building %s/%s for %s", item.Kind, item.Name, item.Environment)

		product, err := b.engine.Make(ctx, specs[i], out)
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

// deployments is which of a project's environments this branch feeds.
//
// IT READS ONE PUBLISHED OBJECT TO FIND OUT, because the environment table is
// the same in every deployment of a project — it is the project's own document,
// not a per-environment one — so any of them answers.
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

	// ONLY DEPLOYMENTS THAT ARE ACTUALLY PUBLISHED. A branch can feed one the
	// configuration declares and nobody has deployed yet, and building for it
	// would file an artifact under a pointer no worker will ever read.
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

// item is one build: a resource, in one deployment.
type item struct {
	Environment string
	Scope       []string
	Kind        string
	Name        string
	ID          string
	Build       specs.Build
	Path        string
	Variables   []specs.Variable
}

// plan is every build this push touches, across every deployment it feeds.
func (b *Builder) plan(ctx context.Context, projectID string, deployments []string, pushed, main string) ([]item, error) {
	var out []item

	for _, environment := range deployments {
		p, err := b.projectAt(ctx, projectID, environment)
		if err != nil {
			return nil, err
		}

		for _, buildable := range p.Buildables() {
			backs, err := b.backedBy(buildable, pushed, main)
			if err != nil {
				return nil, err
			}
			if !backs {
				continue
			}

			// REFUSED, NOT BUILT AROUND. `source.config` says what this
			// resource serves is decided in its own repository; the compiled
			// object records every such file the compile read, and one it did
			// not would be served by the block it was meant to replace.
			if config := buildable.Source.Config; config != "" && !p.Filled(buildable.ID) {
				return nil, fmt.Errorf("%w: %s/%s names source.config %q and the compile of %s did not read it",
					ErrUnfilled, buildable.Kind, buildable.Name, config, environment)
			}

			out = append(out, item{
				Environment: environment,
				Scope:       buildable.Scope,
				Kind:        buildable.Kind,
				Name:        buildable.Name,
				ID:          buildable.ID,
				Build:       buildable.Build,
				Path:        buildable.Source.Path,
				Variables:   buildable.Variables,
			})
		}
	}
	return out, nil
}

// backedBy reports whether a pushed repository is the one this resource is
// built from.
//
// A BARE PATH IS THE PROJECT'S MAIN REPOSITORY, the configuration repository
// it is claimed by, which the config cannot say — so `main` comes from the
// project table. A code repository backs only a source naming it.
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
		// A CLONE URL THE CONFIG CARRIES AND THIS CANNOT PARSE IS AN ERROR AND
		// NOT A SKIP. Skipping would silently stop building a resource whose
		// author thinks it is wired up.
		return false, fmt.Errorf("artifact: %s/%s names %q, which is not a repository: %w",
			res.Kind, res.Name, res.Source.Git, err)
	}
	return key.String() == pushed, nil
}

// specOf turns one item into a build to run.
func (b *Builder) specOf(ctx context.Context, projectID string, i item, checkout string) (Spec, error) {
	spec := Spec{
		Image:  i.Build.Image,
		Dir:    joinWithin(checkout, i.Path),
		Output: i.Build.Output,
	}

	// THE KIND IS READ BEFORE THE IMAGE, because a dockerfile build has no
	// `image` — it IS one — so checking the image first would refuse it with a
	// message about the wrong thing.
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
		// A DOCKERFILE BUILD PRODUCES AN IMAGE, not a file, and an image has to
		// be pushed somewhere before anything can run it. That is a registry
		// this cloud does not have, so it is refused by name rather than run
		// into a failure nobody can read.
		return Spec{}, fmt.Errorf("artifact: %s/%s is a %q build, and only %q and %q produce something this cloud can store",
			i.Kind, i.Name, i.Build.Kind, specs.BuildCommands, specs.BuildScript)
	}

	// A BUILD-TIME VARIABLE IS NOT A RUN-TIME ONE, and the config says which
	// with `at`. Passing everything would put a runtime secret into a toolchain
	// container somebody else wrote; passing nothing would drop the token a
	// private dependency needs.
	for _, v := range i.Variables {
		if v.At != buildTime {
			continue
		}
		if spec.Variables == nil {
			spec.Variables = map[string]string{}
		}

		if !v.FromSecret() {
			spec.Variables[v.Name] = v.Value
			continue
		}

		// A SECRET NEEDS A RESOLVER, and with none the build is refused rather
		// than run without it. A build that quietly lost its token fails
		// somewhere further in, with a message about a dependency it could not
		// fetch.
		if b.secrets == nil {
			return Spec{}, fmt.Errorf("%w: %s/%s's %s wants %s",
				ErrNoSecrets, i.Kind, i.Name, v.Name, v.Secret)
		}

		// THE ENVIRONMENT IS THE ITEM'S. One repository push builds for every
		// deployment its branch feeds, and each of those reads its own
		// environment's value — so staging builds with staging's token and
		// production with production's, out of one plan.
		value, err := b.secrets.Secret(ctx, projectID, i.Environment, v.Secret)
		if err != nil {
			// THE VALUE CANNOT BE IN THIS MESSAGE and neither can anything
			// resolved so far: this error reaches the job log, which the
			// repository's readers can read.
			return Spec{}, fmt.Errorf("artifact: %s/%s's %s needs the secret %s in %s: %w",
				i.Kind, i.Name, v.Name, v.Secret, i.Environment, err)
		}
		spec.Variables[v.Name] = value
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
