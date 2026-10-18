package worker

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// schedule.go answers "what should this node be firing", which is the cron half
// of the same question consume.go asks about queues.
//
// # A cron is not a workload and that changes the walk
//
// A queue consumer is a workload that DECLARED it receives; a cron is a
// resource of its own that runs nothing and whose access block names what it
// fires. So this walks the `cron` kind rather than every kind that runs, and
// the targets come out of the access block rather than the resource.
//
// # It carries what the vocabulary said and decides nothing
//
// The schedule, the zone it is written in, what happens when a run overlaps the
// next tick, and how far to spread the start. All four are the author's, and
// what is done with them is services/primitives/cron.go's — this walk decides
// nothing about time.

// Schedule is one cron, resolved: when it fires and what it fires.
type Schedule struct {
	// Handle is the deployment and the cron's own id, in the form
	// specs.Handle builds — so the record of what has already fired is
	// scoped the way everything else replicated is.
	Handle string

	// ID is the cron's own resource id, which is the half of the handle a
	// fired workload can do something with: it is what the cron is called
	// everywhere the author's name is not, and it survives a rename.
	//
	// Carried rather than cut back out of Handle, because a joined string
	// read backwards is a separator bug waiting to name the wrong resource —
	// and the id is in hand where the handle is built.
	ID string

	// Name and Scope are where the cron itself lives, for a message that says
	// which one.
	Name  string
	Scope []string

	// Path is Name and Scope written back as the absolute namespace an author
	// would have typed — "/billing/nightly". It is rendered here, where the
	// scope chain is in hand, so that nothing downstream joins segments with a
	// copy of the grammar's leading slash.
	Path string

	// Spec is the cron expression and Timezone is the IANA zone it is written
	// in. A cron expression alone does not name a moment.
	Spec     string
	Timezone string

	// OnOverlap is what to do when the clock comes round and the last run is
	// still going: "skip", "queue" or "allow".
	OnOverlap string

	// Jitter spreads the start randomly across this window, so a fleet reading
	// one schedule does not fire at the same instant.
	Jitter time.Duration

	// Fires is every target this cron edges to, in the form Invoke takes.
	Fires []Target

	// Starts is every workflow it begins a run of, as THE FIRST STEP'S QUEUE
	// GRANT — which is where a start actually lands. A cron that starts a
	// pipeline is the ordinary shape (the fixture's own does it) and starting
	// one is a SEND rather than an invocation, so it cannot travel as a Target.
	//
	// THE HANDLE IS THE QUEUE'S AND NOT THE WORKFLOW'S. The entry record that
	// says which queue is still on Settings, so a caller that wants to see what
	// was resolved can; what it must not have to do is resolve it again.
	Starts []invoke.Grant

	// Refused, when set, is why this cloud will not fire it — and it names the
	// target rather than the cron, for the reason a grant does.
	Refused string
}

// cronKind is the vocabulary's name for a schedule.
const cronKind = specs.KindCron

// Schedules is every cron in the deployments this node already holds.
//
// THE SAME LAZINESS AS Consumers, and for the same reason: the register does
// not enumerate, so what a node can fire is what it is already serving. A cron
// in a deployment nothing has touched does not fire — which for a schedule is a
// sharper consequence than for a queue, since nothing else will wake the
// deployment up. The composer hands this to services/primitives, which runs the
// clock over it.
func (s *Service) Schedules(ctx context.Context) []Schedule {
	var out []Schedule
	s.eachDeployment(ctx, func(proj *specs.Project, name, environment string) {
		eachScope(proj, nil, func(scope []string) {
			out = append(out, schedulesIn(proj, name, environment, scope)...)
		})
	})

	sort.Slice(out, func(i, j int) bool { return out[i].Handle < out[j].Handle })
	return out
}

// schedulesIn is every cron in one scope.
func schedulesIn(proj *specs.Project, name, environment string, scope []string) []Schedule {
	names, err := proj.Names(scope, cronKind)
	if err != nil {
		return nil
	}

	var out []Schedule
	for _, cron := range names {
		if s, ok := scheduleOf(proj, name, environment, scope, cron); ok {
			out = append(out, s)
		}
	}
	return out
}

// scheduleOf reads one cron.
func scheduleOf(proj *specs.Project, name, environment string, scope []string, cron string) (Schedule, bool) {
	resource, id, err := proj.Resource(scope, cronKind, cron)
	if err != nil {
		return Schedule{}, false
	}

	declared := specs.ReadCron(resource)
	spec := declared.Schedule
	if spec == "" {
		// Required by the vocabulary, so this is a compiled object that did not
		// come from tcc. Firing it would mean inventing a schedule.
		return Schedule{}, false
	}

	out := Schedule{
		Handle:    specs.Handle(name, environment, id),
		ID:        id,
		Name:      cron,
		Scope:     scope,
		Path:      specs.NamespaceOf(scope, cron),
		Spec:      spec,
		Timezone:  declared.Timezone,
		OnOverlap: declared.OnOverlap,
	}
	if jitter := declared.Jitter; jitter != nil {
		out.Jitter = *jitter
	}

	// WHAT IT FIRES IS ITS ACCESS BLOCK, which is the vocabulary's own
	// sentence: "it fires whatever it edges to; the target declares nothing".
	// So this reads every kind rather than a list of the ones a cron is
	// expected to name.
	for _, kind := range schema.Kinds() {
		for _, reach := range resource.Reaches(kind) {
			// A WORKFLOW IS STARTED AND NOT INVOKED. It runs on no tier — it is
			// queues — so firing it means sending to its first step, and the
			// grant the composer built already carries where that is.
			if kind == workflowKind && contains(reach.Ops, opStart) {
				grant, err := startGrant(proj, name, environment, scope, reach)
				if err != nil {
					if out.Refused == "" {
						out.Refused = err.Error()
					}
					continue
				}
				out.Starts = append(out.Starts, grant)
				continue
			}

			if why := unfireable(kind, reach); why != "" {
				if out.Refused == "" {
					out.Refused = why
				}
				continue
			}
			out.Fires = append(out.Fires, Target{
				Project:     name,
				Environment: environment,
				Scope:       specs.ScopeOf(scope, reach.Namespace),
				Kind:        kind,
				Name:        reach.Target(),
			})
		}
	}

	if len(out.Fires) == 0 && len(out.Starts) == 0 && out.Refused == "" {
		// A cron that edges to nothing is a clock nobody set an alarm on. It is
		// legal and it is not something to run a loop for.
		return Schedule{}, false
	}
	return out, true
}

// startGrant is the grant a cron spends to begin a run.
//
// WHAT IT ANSWERS IS THE QUEUE'S GRANT AND NOT THE WORKFLOW'S, and that is the
// whole of what a firer needs: a workflow runs on no tier, so starting one is a
// SEND to its first step's queue and a grant handled by the workflow's own id
// names a queue nothing drains. That is what this used to answer — the item was
// accepted, the cron's slot was claimed, and the run never happened, with
// nothing red anywhere.
//
// THE REBUILD IS invoke.StartGrant's, which is also what pkg/invoke/wasm's
// `workflow.start` calls. A convention with two implementations is a convention
// one of them gets wrong, and this is the one that did.
func startGrant(proj *specs.Project, name, environment string, scope []string, reach specs.Reach) (invoke.Grant, error) {
	target, id, err := proj.Reached(scope, reach)
	if err != nil {
		return invoke.Grant{}, err
	}

	grant := invoke.Grant{
		Kind:   reach.Kind,
		Label:  reach.Label,
		Handle: specs.Handle(name, environment, id),
		Ops:    reach.Ops,
	}
	if err := startedBy(proj, Target{Project: name, Environment: environment}, target, id, &grant); err != nil {
		return invoke.Grant{}, err
	}
	if grant.Refused != "" {
		return invoke.Grant{}, fmt.Errorf("%s", grant.Refused)
	}

	queued, ok := invoke.StartGrant(grant)
	if !ok {
		return invoke.Grant{}, fmt.Errorf(
			"workflow %q records no queue to start at, so a tick has nowhere to land", reach.Label)
	}
	return queued, nil
}

// unfireable says why a target cannot be invoked on a tick, or "" when it can.
//
// A KIND THAT RUNS NOTHING CANNOT BE FIRED. `EnginesOf` is empty for exactly
// those — a workflow, a queue, a zone — and the fixture's own cron edges to a
// workflow, so this is the ordinary case rather than a corner. Saying so is the
// difference between "this cloud does not run workflows yet" and a schedule
// that silently never happens.
func unfireable(kind string, reach specs.Reach) string {
	if len(schema.EnginesOf(kind)) == 0 {
		return fmt.Sprintf("this cron fires %s %q, and nothing in this cloud runs a %s",
			kind, reach.Label, kind)
	}
	return ""
}
