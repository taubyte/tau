package node

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/services/worker"
)

// authorizer.go runs a resource's own `authorizer:` block — the function a
// target names to decide what a caller may do to it.
//
// # It lives here for the reason the instance validator does
//
// The wasm tier runs ONE workload and has no way to reach another; the node has
// both a worker that can resolve a second function and a tier that can
// instantiate it. node/admission.go makes that argument for a validator and it
// holds harder here, because asking means instantiating a DIFFERENT WORLD —
// `taubyte:authorizer` rather than the handler — which a running guest's tier
// has no business arranging on its own.
//
// # The answer is kept, and the contract is what makes that legitimate
//
// An authorizer is asked about one op and answers with every op this caller gets
// on this target. That is not an optimisation somebody bolted on: it is the WIT,
// and it is the difference between a decision that costs one cold start and one
// that costs a wasm instantiation per cache `get`.
//
// THE FIRST QUESTION DECIDES EVERY LATER ONE, and that is only sound because
// the answer is the COMPLETE set. It is worth stating because it is exactly what
// goes wrong with a guard that returns only the op it was handed: such a guard
// looks correct until something keeps its answer, and then whichever operation
// a workload happened to attempt first silently becomes the only one it may
// ever perform. Nothing here can detect that — the shape is identical — so the
// obligation lives in the WIT and the fixture demonstrates the right one.
//
// KEYED BY THE CALLER'S REVISION, so a redeploy re-asks. A revision is the
// immutable build identity, so a workload whose code changed is a different
// caller as far as a guard is concerned — which is the case where a stale answer
// would be most wrong.
//
// WHAT IT COSTS, said plainly: a decision revoked in the guard's own data is not
// seen until something drops this entry. Nothing does yet. That is the shape of
// the caching question rather than an answer to it, and it is written down here
// rather than discovered.

// What the composer wrote onto a guarded grant. The namespace arrives RESOLVED
// — the outward walk from the TARGET happens where the compiled project is
// open, which is what services/worker's guardedBy states at more length.
const (
	settingAuthorizer      = "authorizer/name"
	settingAuthorizerScope = "authorizer/namespace"
)

// authorizers asks a guard, once per caller and target.
type authorizers struct {
	node *Node

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
		return nil, fmt.Errorf("node: %s is guarded by %q and this node cannot run one", grant.Label, name)
	}

	project, environment, _ := splitHandle(grant.Handle)
	if project == "" || environment == "" {
		return nil, fmt.Errorf("node: the handle %q does not name a deployment", grant.Handle)
	}

	// THE GUARD'S OWN SPEC, resolved like any other function: it has its own
	// access block, its own limits and its own artifact, and nothing about being
	// an authorizer changes how it is found.
	guard, err := a.node.worker.Resolve(ctx, worker.Target{
		Project:     project,
		Environment: environment,
		Scope:       splitNamespace(grant.Settings[settingAuthorizerScope]),
		Kind:        "function",
		Name:        name,
	})
	if err != nil {
		return nil, fmt.Errorf("node: resolving the authorizer %q for %s: %w", name, grant.Label, err)
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
		return nil, fmt.Errorf("node: asking the authorizer %q about %s: %w", name, grant.Label, err)
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

// wasmTier is the component tier a guard is instantiated through, or nil on a
// node that runs no components.
func (n *Node) wasmTier() *wasmtier.Runtime { return n.wasm }
