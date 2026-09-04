package worker

import (
	"context"
	"time"
)

// meter.go is how something OUTSIDE this service learns what it accounted for.
//
// AN INVOCATION IS THE OTHER OBVIOUSLY BILLED THING A CLOUD DOES. Requests and
// GB-seconds are what every serverless platform in the market charges in, and
// this service knew neither — it resolved a spec, ran it, and answered.
//
// IT IS THE SHAPE services/storage AND services/build DECLARE, deliberately: a
// sink that can total one should not have to learn a third vocabulary.
//
// A nil Meter is the ordinary case and costs a nil check. There is no default
// implementation and no registry — a sink is handed in by whatever composes the
// node, or there is none and the events are not built at all.

// Meter is told about an invocation this service accounted for. Record must not
// block: it is called on the request path, holding no lock but spending the
// caller's latency.
type Meter interface {
	Record(ctx context.Context, ev Accounted)
}

// Op is what happened. It is a closed set because an emission nothing can
// enumerate is an emission nothing can total.
type Op string

const (
	// OpInvoke is one request that reached a workload and was answered.
	OpInvoke Op = "invoke"

	// OpRefuse is a request that never reached one: an unknown resource, a
	// tier that could not start it, a workload at capacity.
	//
	// IT IS EMITTED BECAUSE A REFUSAL IS THE EVENT A CALLER MOST WANTS
	// EXPLAINED, and it is the one an invocation counter cannot show — the
	// figures look identical whether a request was served or turned away.
	OpRefuse Op = "refuse"
)

// Accounted is one invocation, with the tenant on it.
//
// THE TENANT IS SPELLED OUT rather than carried as a Target: a sink is
// downstream of this package and should not have to know how an address is
// built, and a value crossing a service boundary should be flat enough to
// serialise without this package's constructors.
type Accounted struct {
	Project     string
	Environment string
	Kind        string
	Name        string

	Op Op

	// Duration is how long the invocation took, which with Memory is the
	// GB-second every serverless price list is written in.
	Duration time.Duration

	// Memory is the ceiling the resource declared, in bytes. It is what a
	// workload was ALLOWED, not what it used — which is what every platform
	// bills, because what it used is not observable from here.
	Memory int64

	// Revision is the compiled object the workload was running. A bill somebody
	// disputes is reconciled against a deploy.
	Revision string
}

// emit hands over one event, or does nothing at all.
//
// IT IS NOT CALLED record: services/build holds that name for the method that
// writes a job record and its censuses count call sites by the identifier. The
// two packages are read together often enough that one spelling is worth more
// than a name that reads marginally better in one of them.
func (s *Service) emit(ctx context.Context, ev Accounted) {
	if s.meter == nil {
		return
	}
	s.meter.Record(ctx, ev)
}

// accounted builds one event from a target and what running it cost.
func accounted(t Target, revision string, memory int64, op Op, took time.Duration) Accounted {
	return Accounted{
		Project:     t.Project,
		Environment: t.Environment,
		Kind:        t.Kind,
		Name:        t.Name,
		Op:          op,
		Duration:    took,
		Memory:      memory,
		Revision:    revision,
	}
}
