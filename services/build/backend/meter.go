package backend

import (
	"context"
	"time"
)

// meter.go is how something outside this service learns what it accounted for:
// build minutes and stored bytes, as events rather than the record's current
// figures. Same shape as services/storage's. A nil Meter means no events are
// built.

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
	// OpBuild is one finished run; Duration is the builder time it occupied, and
	// Succeeded tells the two kinds of minute apart.
	OpBuild Op = "build"

	// OpArtifact is bytes stored for one deployment's resource.
	OpArtifact Op = "artifact"

	// OpLog is bytes of build output stored, separate from artifacts since lifetimes
	// and prices differ.
	OpLog Op = "log"
)

// Accounted is one operation, with the tenant spelled out flat for a downstream
// sink. Environment is empty for a build, since a run is not per deployment.
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

// emit hands over one event, or does nothing. Not called `record`: that name is
// reserved for the one record writer the censuses count.
func (s *Service) emit(ctx context.Context, ev Accounted) {
	if s.meter == nil {
		return
	}
	s.meter.Record(ctx, ev)
}
