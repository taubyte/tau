package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/project"
	"github.com/taubyte/tau/pkg/register"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// resolve.go turns a name into something the pool can run.
//
//	(project, environment)            -> a pointer      [register, cached for a TTL]
//	pointer.Object                    -> a project      [register, cached forever]
//	(scope, kind, name)               -> a resource     [an index hit]
//	resource                          -> an invoke.Spec [read off the vocabulary]
//
// TWO CACHES, AND THEY ARE CACHED FOR OPPOSITE REASONS. A compiled project is
// named by its content, so one that has been fetched is correct forever and the
// only bound on holding it is memory. A pointer is the thing that MOVES, so it
// is held for as long as a deploy is allowed to take to propagate and no
// longer. Collapsing them into one cache would mean either re-fetching a whole
// compiled project every few seconds or serving replaced code indefinitely.

// Target is what a caller names.
type Target struct {
	Project     string
	Environment string

	// Scope is the chain of namespace names from the project root. It is the
	// ASKER's scope: a name resolves there first and then in each parent.
	Scope []string

	Kind string
	Name string
}

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

// pointerEntry is one cached Resolve.
type pointerEntry struct {
	pointer register.Pointer
	at      time.Time
}

// projectEntry is one cached compiled project. ready is closed when the load
// that claimed this entry has finished, so concurrent readers of the same
// object wait for one fetch rather than each starting their own — the fetch is
// a whole compiled project over the wire, and a deploy makes every in-flight
// request want it at the same instant.
type projectEntry struct {
	ready chan struct{}
	proj  *project.Project
	err   error
}

func (s *Service) pointer(ctx context.Context, projectName, environment string) (register.Pointer, error) {
	key := projectName + "\x00" + environment

	s.mu.Lock()
	entry, ok := s.pointers[key]
	s.mu.Unlock()

	if ok && s.now().Sub(entry.at) < s.cfg.PointerTTL {
		return entry.pointer, nil
	}

	p, err := s.register.Resolve(ctx, projectName, environment)
	if err != nil {
		return register.Pointer{}, err
	}

	s.mu.Lock()
	s.pointers[key] = pointerEntry{pointer: p, at: s.now()}
	s.mu.Unlock()

	return p, nil
}

func (s *Service) project(ctx context.Context, id register.ObjectID) (*project.Project, error) {
	s.mu.Lock()
	if entry, ok := s.projects[id]; ok {
		s.touch(id)
		s.mu.Unlock()
		<-entry.ready
		return entry.proj, entry.err
	}
	entry := &projectEntry{ready: make(chan struct{})}
	s.projects[id] = entry
	s.touch(id)
	s.evict()
	s.mu.Unlock()

	compiled, err := s.register.Object(ctx, id)
	if err == nil {
		entry.proj, err = project.Load(compiled)
	}
	entry.err = err
	close(entry.ready)

	// A FAILED LOAD IS NOT CACHED. The object is immutable, so a successful
	// load is good forever — but a failure is usually the register being
	// unreachable, and remembering it would turn one bad moment into a project
	// that never loads again.
	if err != nil {
		s.mu.Lock()
		if s.projects[id] == entry {
			delete(s.projects, id)
			s.forget(id)
		}
		s.mu.Unlock()
		return nil, err
	}
	return entry.proj, nil
}

// touch, forget and evict are the LRU over compiled projects. They are called
// with s.mu held.
func (s *Service) touch(id register.ObjectID) {
	s.forget(id)
	s.lru = append(s.lru, id)
}

func (s *Service) forget(id register.ObjectID) {
	for i, held := range s.lru {
		if held == id {
			s.lru = append(s.lru[:i], s.lru[i+1:]...)
			return
		}
	}
}

func (s *Service) evict() {
	for len(s.lru) > s.cfg.MaxProjects {
		delete(s.projects, s.lru[0])
		s.lru = s.lru[1:]
	}
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
	return spec, nil
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

	pointer, err := s.pointer(ctx, t.Project, t.Environment)
	if err != nil {
		return invoke.Spec{}, err
	}

	proj, err := s.project(ctx, pointer.Object)
	if err != nil {
		return invoke.Spec{}, err
	}

	resource, resourceID, err := proj.Resource(t.Scope, t.Kind, t.Name)
	if err != nil {
		return invoke.Spec{}, err
	}

	return s.spec(ctx, t, pointer, resource, resourceID)
}
