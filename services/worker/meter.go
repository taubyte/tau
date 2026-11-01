package worker

import (
	"context"
	"time"
)

// Meter is how something outside this service learns what it accounted for, in the
// shape services/storage and services/build declare. A nil Meter is the ordinary
// case; no events are built.

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

	// OpRefuse is a request that never reached a workload: an unknown resource, a tier
	// that could not start it, a workload at capacity.
	OpRefuse Op = "refuse"
)

// Accounted is one invocation, with the tenant spelled out flat rather than as a
// Target so a sink needs none of this package's types.
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

// emit hands over one event, or does nothing. Not named record: services/build uses
// that name for writing a job record.
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
