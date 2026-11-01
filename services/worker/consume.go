package worker

import (
	"context"
	"sort"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// The register deliberately does not enumerate, so a node drains the queues of
// deployments it already serves (held because a request or publish reached it). The
// first item pushed in a deployment no node holds waits, which is why the producer is
// itself a workload. Every serving node runs the loop; a claim is exclusive and
// leader-only, so all but the raft leader get ErrNotLeader and do nothing.

// Consumer is one workload that declared it receives from one queue.
type Consumer struct {
	// Target is the workload to invoke, in the form Invoke takes.
	Target Target

	// Queue is the grant the consumer's own access block produced: the handle
	// the queue is opened under, the settings the target declared, and the
	// refusal if this cloud will not serve it.
	Queue invoke.Grant

	// Path is where an item is posted. A container answers on the path it
	// declared; a workload that declared none is called at "/".
	Path string

	// Builtin is the platform program this consumer runs, empty for ordinary
	// code. A workflow step's driver is one.
	Builtin string

	// Invokes is what a builtin driver was granted `invoke` on, resolved into
	// addresses here because the grant carries a label and namespace and resolving
	// them needs the project.
	Invokes []Target

	// Sends is what it was granted `send` on: for a step driver, the next
	// step's queue. The last step has none, which is exactly where a run ends.
	Sends []invoke.Grant

	// Concurrency is how many items this workload may hold at once, off its own
	// `limits/concurrency`. Zero means none written: one at a time.
	Concurrency int
}

// receiveOp is the vocabulary's own verb for the delivered half of a queue.
const receiveOp = "receive"

// Consumers is every queue consumer in the deployments this node holds. Only
// resources that declared `receive` are resolved. A refused grant is returned, not
// dropped, so the caller can say why a queue is not drained.
func (s *Service) Consumers(ctx context.Context) []Consumer {
	var out []Consumer
	s.eachDeployment(ctx, func(proj *specs.Project, name, environment string) {
		eachScope(proj, nil, func(scope []string) {
			out = append(out, s.consumersIn(ctx, proj, name, environment, scope)...)
		})
	})

	sort.Slice(out, func(i, j int) bool {
		if out[i].Target.Name != out[j].Target.Name {
			return out[i].Target.Name < out[j].Target.Name
		}
		return out[i].Queue.Label < out[j].Queue.Label
	})
	return out
}

// consumersIn is every consumer declared in one scope.
func (s *Service) consumersIn(ctx context.Context, proj *specs.Project, name, environment string, scope []string) []Consumer {
	var out []Consumer

	kinds, err := proj.Kinds(scope)
	if err != nil {
		return nil
	}
	for _, kind := range kinds {
		// A kind that runs nothing (no engines) cannot consume.
		if len(schema.EnginesOf(kind)) == 0 {
			continue
		}

		names, err := proj.Names(scope, kind)
		if err != nil {
			continue
		}
		for _, resourceName := range names {
			out = append(out, s.consumersOf(ctx, proj, name, environment, scope, kind, resourceName)...)
		}
	}
	return out
}

// consumersOf is one resource's receive entries, resolved.
func (s *Service) consumersOf(ctx context.Context, proj *specs.Project, name, environment string, scope []string, kind, resourceName string) []Consumer {
	resource, _, err := proj.Resource(scope, kind, resourceName)
	if err != nil {
		return nil
	}

	wanted := map[string]specs.Reach{}
	for _, reach := range resource.Reaches(syncKind) {
		if contains(reach.Ops, receiveOp) {
			wanted[reach.Label] = reach
		}
	}
	if len(wanted) == 0 {
		return nil
	}

	target := Target{
		Project: name, Environment: environment,
		Scope: scope, Kind: kind, Name: resourceName,
	}

	// One resolve per workload, not per entry, so two entries cannot read two revisions.
	spec, err := s.Resolve(ctx, target)
	if err != nil {
		return nil
	}

	var out []Consumer
	for _, grant := range spec.Reaches {
		reach, ok := wanted[grant.Label]
		// `receive` is only a queue's verb (checkSyncOps refuses it on a log); the type
		// check keeps this true if another primitive ever declares a delivery.
		if grant.Kind != syncKind || grant.Settings[settingSyncType] != queueType || !ok {
			continue
		}
		out = append(out, Consumer{
			Target:      target,
			Queue:       grant,
			Path:        deliverPath(reach.DeliverPath),
			Concurrency: spec.Concurrency,
			Builtin:     spec.Builtin,
			Invokes:     invokedBy(resource, name, environment, scope),
			Sends:       sentToBy(spec, grant.Label),
		})
	}
	return out
}

// invokedBy is every function this resource may invoke, as an address. A step's
// driver is granted `invoke` on exactly its step's function.
func invokedBy(resource specs.Resource, name, environment string, scope []string) []Target {
	var out []Target
	for _, reach := range resource.Reaches(functionKind) {
		if !contains(reach.Ops, opInvoke) {
			continue
		}
		out = append(out, Target{
			Project: name, Environment: environment,
			Scope: specs.ScopeOf(scope, reach.Namespace),
			Kind:  functionKind, Name: reach.Target(),
		})
	}
	return out
}

// sentToBy is every queue this resource may send to, except the one it receives
// from: a driver sending to its own queue would never leave its step.
func sentToBy(spec invoke.Spec, receiving string) []invoke.Grant {
	var out []invoke.Grant
	for _, g := range spec.Reaches {
		if g.Kind != syncKind || g.Settings[settingSyncType] != queueType ||
			g.Label == receiving || !g.Allows(opSend) {
			continue
		}
		out = append(out, g)
	}
	return out
}

// The vocabulary's words for what a driver is granted.
const (
	functionKind = specs.KindFunction
	opInvoke     = "invoke"
	opSend       = "send"
)

// deliverPath is where an item is posted. A workload that declared none is
// called at the root, which is what a WASM consumer wants — it is dispatched
// and its entrypoint takes whatever request it is handed.
func deliverPath(declared string) string {
	if declared == "" {
		return "/"
	}
	return declared
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
