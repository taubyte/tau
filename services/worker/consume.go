package worker

import (
	"context"
	"sort"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/project"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// consume.go answers "what should this node be draining", which is the question
// a queue dispatcher asks and nothing else could.
//
// # Enumeration is the whole problem, and laziness is the answer
//
// The register DELIBERATELY DOES NOT ENUMERATE (register.proto: "NOTHING HERE
// ENUMERATES") — a pointer is read by name, and a node that could list every
// deployment in the cloud would be a node that could walk somebody else's. So
// there is no list to ask for.
//
// What there IS, is what this node already holds: a compiled project reaches
// the cache here because something asked for it — a request routed to this
// node, a publish it saw. So a node drains the queues of the deployments it is
// ALREADY SERVING, and a deployment nobody has touched drains nowhere until
// somebody does.
//
// THAT IS NOT A GAP so much as the same rule the worker already follows: a
// queue matters once its project is running somewhere, and the thing that makes
// it run somewhere is a request. What it does mean is that the FIRST item
// pushed to a queue in a deployment no node holds waits — which is why the
// producer is itself a workload, running on a node that by definition holds it.
//
// # Every node runs the loop and only one of them does anything
//
// A claim is exclusive and leader-only, so no election is needed: every node
// serving a deployment may run the loop, and all but the raft leader get
// ErrNotLeader and do nothing. That is cheaper than agreeing on who consumes
// and it has no split-brain to reason about.

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

	// Concurrency is how many items this workload may hold at once, off its
	// own `limits/concurrency`.
	//
	// IT IS THE CONSUMER'S NUMBER AND NOT THE DISPATCHER'S. The vocabulary
	// says `limits.concurrency` is "how many items an instance may hold at
	// once", which is only meaningful if the platform is the one pushing — so
	// this is where that sentence becomes true. Zero means the author wrote
	// none, and one at a time is the answer that cannot surprise anybody.
	Concurrency int
}

// receiveOp is the vocabulary's own verb for the delivered half of a queue.
const receiveOp = "receive"

// Consumers is every queue consumer in the deployments this node already holds.
//
// IT RESOLVES ONLY WHAT DECLARED ONE. The walk reads the access block off the
// compiled project, which is already in memory; a full resolve happens for the
// handful of resources that asked to receive, and for nothing else.
//
// A refused grant is RETURNED rather than dropped, because the caller has to be
// able to say why a queue is not being drained — a consumer silently missing
// from this list is a queue that fills up with nothing anywhere explaining it.
func (s *Service) Consumers(ctx context.Context) []Consumer {
	var out []Consumer
	for _, held := range s.projects.Environments() {
		name, environment, ok := splitDeployment(held)
		if !ok {
			continue
		}

		proj, _, err := s.projects.At(ctx, name, environment)
		if err != nil {
			// The pointer is held and the object is not, or the deployment
			// moved under us. The next pass is the retry.
			continue
		}
		out = append(out, s.consumersIn(ctx, proj, name, environment, nil)...)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Target.Name != out[j].Target.Name {
			return out[i].Target.Name < out[j].Target.Name
		}
		return out[i].Queue.Label < out[j].Queue.Label
	})
	return out
}

// consumersIn walks one scope and then its namespaces.
func (s *Service) consumersIn(ctx context.Context, proj *project.Project, name, environment string, scope []string) []Consumer {
	var out []Consumer

	kinds, err := proj.Kinds(scope)
	if err != nil {
		return nil
	}
	for _, kind := range kinds {
		// A KIND THAT RUNS NOTHING CANNOT CONSUME. EnginesOf is empty for a
		// queue, a zone, a volume — reading their access blocks would be
		// asking a cache what it receives.
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

	namespaces, err := proj.Namespaces(scope)
	if err != nil {
		return out
	}
	for _, namespace := range namespaces {
		out = append(out, s.consumersIn(ctx, proj, name, environment, append(append([]string{}, scope...), namespace))...)
	}
	return out
}

// consumersOf is one resource's receive entries, resolved.
func (s *Service) consumersOf(ctx context.Context, proj *project.Project, name, environment string, scope []string, kind, resourceName string) []Consumer {
	resource, _, err := proj.Resource(scope, kind, resourceName)
	if err != nil {
		return nil
	}

	wanted := map[string]project.Reach{}
	for _, reach := range resource.Reaches(queueKind) {
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

	// ONE RESOLVE, not one per entry: a spec carries every grant, and resolving
	// twice for a workload that consumes two queues would read the same
	// document twice and could read two revisions of it.
	spec, err := s.Resolve(ctx, target)
	if err != nil {
		return nil
	}

	var out []Consumer
	for _, grant := range spec.Reaches {
		reach, ok := wanted[grant.Label]
		if grant.Kind != queueKind || !ok {
			continue
		}
		out = append(out, Consumer{
			Target:      target,
			Queue:       grant,
			Path:        deliverPath(reach.DeliverPath),
			Concurrency: spec.Concurrency,
		})
	}
	return out
}

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

// splitDeployment reads "project/environment" back apart.
func splitDeployment(held string) (name, environment string, ok bool) {
	for i := len(held) - 1; i >= 0; i-- {
		if held[i] == '/' {
			return held[:i], held[i+1:], i > 0 && i < len(held)-1
		}
	}
	return "", "", false
}
