package worker

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/taubyte/tau/pkg/project"
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
// what a node does with them is node/cron.go's.

// Schedule is one cron, resolved: when it fires and what it fires.
type Schedule struct {
	// Handle is the deployment and the cron's own id, in the form
	// project.Handle builds — so the record of what has already fired is
	// scoped the way everything else replicated is.
	Handle string

	// Name and Scope are where the cron itself lives, for a message that says
	// which one.
	Name  string
	Scope []string

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

	// Refused, when set, is why this cloud will not fire it — and it names the
	// target rather than the cron, for the reason a grant does.
	Refused string
}

// cronKind is the vocabulary's name for a schedule.
const cronKind = "cron"

// Schedules is every cron in the deployments this node already holds.
//
// THE SAME LAZINESS AS Consumers, and for the same reason: the register does
// not enumerate, so what a node can fire is what it is already serving. A cron
// in a deployment nothing has touched does not fire — which for a schedule is a
// sharper consequence than for a queue, since nothing else will wake the
// deployment up. See node/cron.go, which is where that is dealt with.
func (s *Service) Schedules(ctx context.Context) []Schedule {
	var out []Schedule
	for _, held := range s.projects.Environments() {
		name, environment, ok := splitDeployment(held)
		if !ok {
			continue
		}

		proj, _, err := s.projects.At(ctx, name, environment)
		if err != nil {
			continue
		}
		out = append(out, schedulesIn(proj, name, environment, nil)...)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Handle < out[j].Handle })
	return out
}

// schedulesIn walks one scope and then its namespaces.
func schedulesIn(proj *project.Project, name, environment string, scope []string) []Schedule {
	var out []Schedule

	names, err := proj.Names(scope, cronKind)
	if err == nil {
		for _, cron := range names {
			if s, ok := scheduleOf(proj, name, environment, scope, cron); ok {
				out = append(out, s)
			}
		}
	}

	namespaces, err := proj.Namespaces(scope)
	if err != nil {
		return out
	}
	for _, namespace := range namespaces {
		out = append(out, schedulesIn(proj, name, environment, append(append([]string{}, scope...), namespace))...)
	}
	return out
}

// scheduleOf reads one cron.
func scheduleOf(proj *project.Project, name, environment string, scope []string, cron string) (Schedule, bool) {
	resource, id, err := proj.Resource(scope, cronKind, cron)
	if err != nil {
		return Schedule{}, false
	}

	spec := resource.String("schedule")
	if spec == "" {
		// Required by the vocabulary, so this is a compiled object that did not
		// come from tcc. Firing it would mean inventing a schedule.
		return Schedule{}, false
	}

	out := Schedule{
		Handle:    project.Handle(name, environment, id),
		Name:      cron,
		Scope:     scope,
		Spec:      spec,
		Timezone:  resource.String("timezone"),
		OnOverlap: resource.String("on_overlap"),
	}
	out.Jitter, _ = resource.Duration("jitter")

	// WHAT IT FIRES IS ITS ACCESS BLOCK, which is the vocabulary's own
	// sentence: "it fires whatever it edges to; the target declares nothing".
	// So this reads every kind rather than a list of the ones a cron is
	// expected to name.
	for _, kind := range schema.Kinds() {
		for _, reach := range resource.Reaches(kind) {
			if why := unfireable(kind, reach); why != "" {
				if out.Refused == "" {
					out.Refused = why
				}
				continue
			}
			out.Fires = append(out.Fires, Target{
				Project:     name,
				Environment: environment,
				Scope:       project.ScopeOf(scope, reach.Namespace),
				Kind:        kind,
				Name:        reach.Target(),
			})
		}
	}

	if len(out.Fires) == 0 && out.Refused == "" {
		// A cron that edges to nothing is a clock nobody set an alarm on. It is
		// legal and it is not something to run a loop for.
		return Schedule{}, false
	}
	return out, true
}

// unfireable says why a target cannot be invoked on a tick, or "" when it can.
//
// A KIND THAT RUNS NOTHING CANNOT BE FIRED. `EnginesOf` is empty for exactly
// those — a workflow, a queue, a zone — and the fixture's own cron edges to a
// workflow, so this is the ordinary case rather than a corner. Saying so is the
// difference between "this cloud does not run workflows yet" and a schedule
// that silently never happens.
func unfireable(kind string, reach project.Reach) string {
	if len(schema.EnginesOf(kind)) == 0 {
		return fmt.Sprintf("this cron fires %s %q, and nothing in this cloud runs a %s",
			kind, reach.Label, kind)
	}
	return ""
}
