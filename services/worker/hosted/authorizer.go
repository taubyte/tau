package hosted

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/services/worker"
)

// Runs a resource's `authorizer:` block. It lives in the node because asking means
// instantiating a different world (`taubyte:authorizer`) through a worker and tier
// the running guest cannot reach.
//
// An authorizer answers with every op this caller gets on the target, per the WIT,
// so the answer is kept and the first question decides later ones. That is sound
// only because the answer is complete. Keyed by the caller's revision, so a redeploy
// re-asks. A revocation in the guard's own data is not seen until the entry is
// dropped, which nothing does yet.

// What the composer wrote onto a guarded grant. The namespace arrives RESOLVED
// — the outward walk from the TARGET happens where the compiled project is
// open, which is what services/worker's guardedBy states at more length.
const (
	settingAuthorizer      = specs.AuthorizerKeyName
	settingAuthorizerScope = specs.AuthorizerKeyNamespace
)

// authorizers asks a guard, once per caller and target.
type authorizers struct {
	node *Service

	mu   sync.Mutex
	kept map[string][]string
}

// Ops is every op this caller gets on this grant's target.
func (a *authorizers) Ops(ctx context.Context, spec invoke.Spec, grant invoke.Grant, op string) ([]string, error) {
	name := grant.Settings[settingAuthorizer]
	if name == "" {
		// Nothing guards this. addressed does not call here in that case, so
		// reaching it means a grant changed shape underneath us.
		return grant.Ops, nil
	}

	key := strings.Join([]string{spec.Revision, spec.Kind, spec.Label, strings.Join(spec.Scope, "/"), grant.Handle}, "\x00")
	if ops, held := a.remembered(key); held {
		return ops, nil
	}

	tier := a.node.wasmTier()
	if tier == nil || a.node.worker == nil {
		return nil, fmt.Errorf("worker: %s is guarded by %q and this node cannot run one", grant.Label, name)
	}

	project, environment, _ := specs.SplitHandle(grant.Handle)
	if project == "" || environment == "" {
		return nil, fmt.Errorf("worker: the handle %q does not name a deployment", grant.Handle)
	}

	// THE GUARD'S OWN SPEC, resolved like any other function: it has its own
	// access block, its own limits and its own artifact, and nothing about being
	// an authorizer changes how it is found.
	guard, err := a.node.worker.Resolve(ctx, worker.Target{
		Project:     project,
		Environment: environment,
		Scope:       specs.ScopeOf(nil, grant.Settings[settingAuthorizerScope]),
		Kind:        specs.KindFunction,
		Name:        name,
	})
	if err != nil {
		return nil, fmt.Errorf("worker: resolving the authorizer %q for %s: %w", name, grant.Label, err)
	}

	decision, err := tier.Authorize(ctx, guard, wasmtier.Question{
		Who: wasmtier.Caller{
			Project:     spec.Project,
			Environment: spec.Environment,
			Kind:        spec.Kind,
			Name:        spec.Label,
			Scope:       spec.Scope,
		},
		What: wasmtier.Target{
			Kind:     grant.Kind,
			Label:    grant.Label,
			Instance: instanceOf(grant.Handle),
		},
		Op: op,
	})
	if err != nil {
		return nil, fmt.Errorf("worker: asking the authorizer %q about %s: %w", name, grant.Label, err)
	}
	if decision.Undecided != "" {
		// NOT CACHED. A guard that declined to decide may decide next time, and
		// keeping "no answer" would turn one bad moment into a lasting one.
		return nil, fmt.Errorf("%w: %s about %s: %s", wasmtier.ErrUndecided, name, grant.Label, decision.Undecided)
	}

	a.remember(key, decision.Granted)
	return decision.Granted, nil
}

func (a *authorizers) remembered(key string) ([]string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	ops, held := a.kept[key]
	return ops, held
}

func (a *authorizers) remember(key string, ops []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.kept == nil {
		a.kept = map[string][]string{}
	}
	a.kept[key] = ops
}

// instanceOf is the instance an addressed handle carries, or empty for a target
// that holds one. addressed appends it, so the shape is `project/env/id[/name]`.
func instanceOf(handle string) string {
	parts := strings.Split(handle, "/")
	if len(parts) < 4 {
		return ""
	}
	return parts[3]
}

// decides is the part of the component tier this node's policy needs: the worlds that
// are not the handler. Narrow, so the policy cannot reach the rest of the tier and
// can be tested without a wasm toolchain.
type decides interface {
	Authorize(ctx context.Context, spec invoke.Spec, q wasmtier.Question) (wasmtier.Decision, error)
	Admit(ctx context.Context, spec invoke.Spec, o wasmtier.Opening) (wasmtier.Verdict, error)

	// AuthorizeRequest is the third, and the one asked about traffic rather
	// than about a workload: `who: caller` has no answer when the caller came
	// from the internet, so the question is the REQUEST and the answer is the
	// ops it gets.
	AuthorizeRequest(ctx context.Context, spec invoke.Spec, req *invoke.Request) (*invoke.Admission, error)

	// Resolve is the fourth, and the only one asked about a NAME. A DNS question
	// carries no caller at all -- not a workload, not even a socket this node
	// kept -- so it is the one decision here whose question is the thing being
	// asked about and nothing else.
	Resolve(ctx context.Context, spec invoke.Spec, q invoke.Question) (*invoke.Resolution, error)
}

// wasmTier is what a guard or a validator is instantiated through, or nil on a
// node that runs no components.
func (n *Service) wasmTier() decides { return n.decider }
