package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/project"
	"github.com/taubyte/tau/pkg/register"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// resolve.go turns a name into something the pool can run.
//
//	(project, environment)            -> what it runs   [project.Cache]
//	(scope, kind, name)               -> a resource     [an index hit]
//	resource                          -> an invoke.Spec [read off the vocabulary]
//
// THE FIRST STEP IS project.Cache AND NOT A CACHE OF ITS OWN. A gateway does
// exactly the same read on exactly the same request, and two copies of it would
// be two answers to "how long after a deploy does the old code stop being
// served" — which is a promise a platform makes once.

// Target is what a caller names. It is pkg/project's Address under the name a
// worker's callers use for it: a gateway names one too, and two spellings of
// the same five fields is where a caller starts converting between them.
type Target = project.Address

var (
	ErrNoTarget   = errors.New("worker: a target with no project, environment, kind or name names nothing")
	ErrNoRuntime  = errors.New("worker: nothing runs this kind")
	ErrAmbiguous  = errors.New("worker: the kind runs on more than one tier and the resource does not say which")
	ErrNoArtifact = errors.New("worker: the resource is built by the platform and nothing here resolves what it built")
	ErrNoSecrets  = errors.New("worker: the resource wants a secret and nothing here resolves one")
)

// Artifacts answers what a BUILT resource currently runs.
//
// IT IS A SEAM BECAUSE THE COMPILED CONFIG GENUINELY DOES NOT KNOW. A resource
// whose source is an image or a component names the artifact itself and needs
// nothing here. One whose source is a path in the repository names the SOURCE,
// and what came out of building it is the build service's to say — so a worker
// that guessed a naming convention would be hardcoding one, and a worker that
// silently ran nothing would be worse.
//
// Absent, a built resource is refused with ErrNoArtifact, which says what is
// missing rather than failing later as an image that does not exist.
type Artifacts interface {
	Artifact(ctx context.Context, project, environment, kind, resourceID string) (string, error)
}

// Secrets resolves a secret a run-time variable refers to.
//
// A MISSING SECRET IS A REFUSAL AND NOT AN EMPTY STRING. A workload started
// without its database password does not fail where the password is missing; it
// fails somewhere else, later, in a way that reads as a broken application.
type Secrets interface {
	Secret(ctx context.Context, project, environment, secretID string) (string, error)
}

// spec reads a resource and says what to run.
//
// EVERY VALUE COMES OFF THE COMPILED RESOURCE, and none of them is a
// constructor argument or an operator setting. `execution/memory` and
// `execution/timeout` are PerEnv in the vocabulary, which means they are
// expected to differ between an environment and the next — a worker that had
// captured either at construction would serve a number the author changed and
// nothing would happen.
func (s *Service) spec(ctx context.Context, t Target, p register.Pointer, r project.Resource, resourceID string) (invoke.Spec, error) {
	runtime, err := runtimeOf(t.Kind, r)
	if err != nil {
		return invoke.Spec{}, err
	}

	artifact, err := s.artifactOf(ctx, t, r, resourceID)
	if err != nil {
		return invoke.Spec{}, err
	}

	env, err := s.envOf(ctx, t, r)
	if err != nil {
		return invoke.Spec{}, err
	}

	spec := invoke.Spec{
		Runtime:  runtime,
		Name:     strings.Join([]string{t.Project, t.Environment, t.Kind, resourceID}, "/"),
		Revision: string(p.Object),
		Artifact: artifact,
		Env:      env,
	}
	if memory, ok := r.Int("execution", "memory"); ok {
		spec.Memory = memory
	}
	if timeout, ok := r.Duration("execution", "timeout"); ok {
		spec.Timeout = timeout
	}
	if concurrency, ok := r.Int("limits", "concurrency"); ok {
		spec.Concurrency = int(concurrency)
	}
	spec.Settings = settingsOf(r)

	// WHERE A WORKLOAD MAY REACH IS THE RESOURCE'S, and empty is nowhere. The
	// tier enforces it; this only carries it, because which mechanism does the
	// enforcing is the tier's business and the allowlist is the author's.
	spec.Egress = r.Strings("egress")
	return spec, nil
}

// settingsOf is the `serve` block, flattened, for whichever tier runs this.
//
// IT IS ONE BLOCK AND NOT THE WHOLE RESOURCE. `serve` is by definition how a
// thing is served, so it is the block a tier reads and the one this can hand
// over without deciding what any tier needs — a static site's mode and index
// live there, and so will whatever a later tier is told about serving. Passing
// the entire resource would put every access rule and every variable into every
// Spec, which is a wider thing to hold for one that is not read.
//
// Nothing here is per-environment beyond what the compile already resolved:
// this reads the compiled object, so a `mode_preview` is already `mode`.
func settingsOf(r project.Resource) map[string]string {
	block, ok := r.Map("serve")
	if !ok || len(block) == 0 {
		return nil
	}

	out := make(map[string]string, len(block))
	for key, value := range block {
		if s, ok := value.(string); ok {
			out["serve/"+key] = s
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// runtimeOf says which tier runs this.
//
// IT ASKS THE VOCABULARY RATHER THAN HOLDING A TABLE. Which tiers a kind may
// run on is `Engines()` on that kind in pkg/tcc/taubyte/v2/schema, and a copy
// of that here would be a second place to add a tier — one of which would be
// forgotten, and the failure would be a workload that will not start on the
// tier its config selected.
func runtimeOf(kind string, r project.Resource) (string, error) {
	// A kind with more than one tier authors the choice; `worker` is the one
	// that does today.
	if declared := r.String("runtime"); declared != "" {
		return declared, nil
	}

	engines := schema.EnginesOf(kind)
	switch len(engines) {
	case 1:
		return engines[0], nil
	case 0:
		return "", fmt.Errorf("%w: %s", ErrNoRuntime, kind)
	default:
		return "", fmt.Errorf("%w: %s runs on %s", ErrAmbiguous, kind, strings.Join(engines, " or "))
	}
}

// artifactOf reads what to run out of the resource, or asks the build service
// when the resource names a source rather than an artifact.
func (s *Service) artifactOf(ctx context.Context, t Target, r project.Resource, resourceID string) (string, error) {
	// `source/kind` is the vocabulary's own discriminator, so which branch was
	// taken is read rather than guessed from which field happens to be set.
	switch r.String("source", "kind") {
	case "image":
		return r.String("source", "image"), nil
	case "component":
		return r.String("source", "component"), nil
	}

	if s.artifacts == nil {
		return "", fmt.Errorf("%w: %s/%s", ErrNoArtifact, t.Kind, t.Name)
	}
	return s.artifacts.Artifact(ctx, t.Project, t.Environment, t.Kind, resourceID)
}

// envOf is the run-time half of the variables block.
//
// BUILD-TIME VARIABLES ARE NOT HERE. `at: build` says the value is baked into
// the artifact, and handing one to a running workload would be handing it a
// value that may not be the one it was built with.
func (s *Service) envOf(ctx context.Context, t Target, r project.Resource) (map[string]string, error) {
	variables := r.Sub("variables")
	if len(variables) == 0 {
		return nil, nil
	}

	out := map[string]string{}
	for _, name := range r.Keys("variables") {
		v := variables.Sub(name)
		if v.String("at") == "build" || v.String("as") != "env" {
			continue
		}

		if id := v.String("secret", "name"); id != "" {
			if s.secrets == nil {
				return nil, fmt.Errorf("%w: %s wants %s", ErrNoSecrets, t.Name, name)
			}
			value, err := s.secrets.Secret(ctx, t.Project, t.Environment, id)
			if err != nil {
				return nil, fmt.Errorf("worker: %s: resolving %s: %w", t.Name, name, err)
			}
			out[name] = value
			continue
		}
		out[name] = v.String("value")
	}
	return out, nil
}

// Resolve is the whole read: a target in, a spec out. It is exported because a
// gateway placing a request, and an operator asking what a name resolves to,
// want exactly this and nothing after it.
func (s *Service) Resolve(ctx context.Context, t Target) (invoke.Spec, error) {
	if t.Project == "" || t.Environment == "" || t.Kind == "" || t.Name == "" {
		return invoke.Spec{}, ErrNoTarget
	}

	proj, pointer, err := s.projects.At(ctx, t.Project, t.Environment)
	if err != nil {
		return invoke.Spec{}, err
	}

	resource, resourceID, err := proj.Resource(t.Scope, t.Kind, t.Name)
	if err != nil {
		return invoke.Spec{}, err
	}

	return s.spec(ctx, t, pointer, resource, resourceID)
}
