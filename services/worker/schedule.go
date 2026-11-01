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

// Schedules answers what this node should fire, the cron half of consume.go. A cron
// runs nothing; its access block names what it fires. The schedule, zone, overlap
// policy and jitter are carried as written; services/primitives/cron.go acts on them.

// Schedule is one cron, resolved: when it fires and what it fires.
type Schedule struct {
	// Handle is the deployment and the cron's own id (specs.Handle), scoping the record
	// of what has fired.
	Handle string

	// ID is the cron's own resource id, which survives a rename. Carried rather than cut
	// back out of Handle.
	ID string

	// Name and Scope are where the cron itself lives, for a message that says
	// which one.
	Name  string
	Scope []string

	// Path is Name and Scope as the absolute namespace an author would type
	// ("/billing/nightly"), rendered where the scope chain is in hand.
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

	// Starts is every workflow it begins a run of, as the first step's queue grant: a
	// start is a send, not an invocation. The entry record remains on Settings.
	Starts []invoke.Grant

	// Refused, when set, is why this cloud will not fire it, naming the target.
	Refused string
}

// cronKind is the vocabulary's name for a schedule.
const cronKind = specs.KindCron

// Schedules is every cron in the deployments this node already holds; the register
// does not enumerate. A cron in an untouched deployment does not fire. The composer
// hands this to services/primitives.
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

	// What it fires is its access block, so every kind is read.
	for _, kind := range schema.Kinds() {
		for _, reach := range resource.Reaches(kind) {
			// A workflow is started, not invoked: firing it sends to its first step.
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

// startGrant is the grant a cron spends to begin a run: the first step's queue
// grant, since a grant handled by the workflow's id names a queue nothing drains.
// Built by invoke.StartGrant, as wasm's `workflow.start` is.
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

// unfireable says why a target cannot be invoked on a tick, or "" when it can. A kind
// with no engines cannot be fired.
func unfireable(kind string, reach specs.Reach) string {
	if len(schema.EnginesOf(kind)) == 0 {
		return fmt.Sprintf("this cron fires %s %q, and nothing in this cloud runs a %s",
			kind, reach.Label, kind)
	}
	return ""
}
