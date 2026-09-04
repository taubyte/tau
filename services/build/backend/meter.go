package backend

import (
	"context"
	"time"
)

// meter.go is how something OUTSIDE this service learns what it accounted for.
//
// A BUILD IS THE MOST OBVIOUSLY BILLED THING A CLOUD DOES. Build minutes are
// the unit every CI product in the market charges in, and the bytes an
// artifact and a log occupy are the second — and this service knew none of it.
// The job record already carries the outcome, but a record is a counter: it
// says what the current figure is, where an emission says what happened.
// Anything that wants to attribute, aggregate across services, or answer a
// question this package did not anticipate needs the second and cannot
// reconstruct it from the first.
//
// It is the same shape services/storage declares, deliberately: a sink that can
// total one should be able to total the other without learning a second
// vocabulary.
//
// A nil Meter is the ordinary case and costs a nil check. There is no default
// implementation and no registry — a sink is handed in by whatever composes the
// node, or there is none and the events are not built at all.

// Meter is told about an operation this service accounted for. Record must not
// block: it is called from the run loop, holding no lock but spending the
// caller's latency.
type Meter interface {
	Record(ctx context.Context, ev Accounted)
}

// Op is what happened. It is a closed set because an emission nothing can
// enumerate is an emission nothing can total.
type Op string

const (
	// OpBuild is one RUN, finished. Duration is what it occupied a builder for
	// — which is what a build minute is — and Succeeded says which kind of
	// minute it was. A failed build costs the same to run as one that worked,
	// and a plan that charges for one and not the other has to be able to tell
	// them apart.
	OpBuild Op = "build"

	// OpArtifact is bytes stored for one deployment's resource.
	OpArtifact Op = "artifact"

	// OpLog is bytes of build output stored. It is separate from the artifact
	// because they have different lifetimes and, in every pricing model that
	// exists, different prices.
	OpLog Op = "log"
)

// Accounted is one operation, with the tenant on it.
//
// THE TENANT IS SPELLED OUT rather than carried as a record: a sink is
// downstream of this package and should not have to know how a job is stored,
// and a value crossing a service boundary should be flat enough to serialise
// without this package's constructors.
//
// Environment is empty for a build and set for an artifact, and that is not an
// omission: a RUN is not per-deployment — one clone, one set of commands — and
// what it produces is. Filling it in for a build would be inventing an
// attribution the run does not have.
type Accounted struct {
	Project     string
	Environment string
	Repository  string

	// Job and Attempt are what an operator reconciles a line item against.
	Job     string
	Attempt uint32

	Op Op

	// Duration is set for OpBuild and zero otherwise.
	Duration time.Duration

	// Bytes is set for OpArtifact and OpLog and zero otherwise.
	Bytes uint64

	// Succeeded is meaningful for OpBuild.
	Succeeded bool
}

// emit hands over one event, or does nothing at all.
//
// IT IS NOT CALLED record, and the reason is a census rather than taste: this
// package holds the NAME `record` for the one method that writes a job record,
// and every scan that asks "who writes a record" counts call sites by that
// identifier. A meter method sharing it would have joined that set silently —
// which is precisely the class of drift those scans exist to catch, and they
// caught this one.
func (s *Service) emit(ctx context.Context, ev Accounted) {
	if s.meter == nil {
		return
	}
	s.meter.Record(ctx, ev)
}
