package worker

import (
	"context"
	"maps"
	"strings"
	"sync"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/register"
)

// resolved.go keeps what a target resolves to under one compiled project. Everything
// in a spec but its artifact is a function of the compiled project and the target,
// and the project is a content id under its pointer, so it is worked out once per
// pointer rather than once per request: a function's took 200µs, two thirds of it
// walking what the resource reaches. The artifact is asked each time, since a build
// moves it without moving the pointer, and the artifacts lookup keeps its own TTL.
// A spec held here is shared by every request it answers: it is read, never written.

// resolvedKey is a target within one deployment.
type resolvedKey struct {
	scope, kind, name string
}

func keyOf(t Target) resolvedKey {
	return resolvedKey{scope: strings.Join(t.Scope, "/"), kind: t.Kind, name: t.Name}
}

// resolvedSpec is a spec with no artifact yet, and the package the artifact is asked
// about.
type resolvedSpec struct {
	spec invoke.Spec
	pkg  ranPackage
}

// deployed is what one deployment's targets resolve to under its current object. It
// is never written once held: a new target replaces it with a copy.
type deployed struct {
	object  register.ObjectID
	targets map[resolvedKey]resolvedSpec
}

// resolutions is every deployment's *deployed, replaced whole when its pointer moves.
// A read takes no lock; mu orders the writes, so two targets added at once both stay.
type resolutions struct {
	held sync.Map
	mu   sync.Mutex
}

func (r *resolutions) get(d register.Deployment, object register.ObjectID, k resolvedKey) (resolvedSpec, bool) {
	held, ok := r.held.Load(d)
	if !ok || held.(*deployed).object != object {
		return resolvedSpec{}, false
	}
	got, ok := held.(*deployed).targets[k]
	return got, ok
}

func (r *resolutions) put(d register.Deployment, object register.ObjectID, k resolvedKey, s resolvedSpec) {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := &deployed{object: object, targets: map[resolvedKey]resolvedSpec{}}
	if held, ok := r.held.Load(d); ok && held.(*deployed).object == object {
		next.targets = maps.Clone(held.(*deployed).targets)
	}
	next.targets[k] = s
	r.held.Store(d, next)
}

// resolved is t's spec as held, its artifact asked now.
func (s *Service) resolved(ctx context.Context, t Target, held resolvedSpec) (invoke.Spec, error) {
	spec := held.spec
	if servedHere(t.Kind) {
		return spec, nil
	}
	artifact, err := s.artifactOf(ctx, t, held.pkg)
	if err != nil {
		return invoke.Spec{}, err
	}
	spec.Artifact = artifact
	return spec, nil
}
