package storage

import "context"

// quota.go's counter enforces capacity; an emission says what happened, with the
// tenant on it, for anything outside this service. A nil Meter is the ordinary case;
// no events are built.

// Meter is told about an operation this service accounted for. Record must not
// block — it is called on the write path, holding no lock but spending the
// caller's latency.
type Meter interface {
	Record(ctx context.Context, ev Accounted)
}

// Op is what happened. It is a closed set because an emission nothing can
// enumerate is an emission nothing can total.
type Op string

const (
	// OpAdd is bytes written into an instance.
	OpAdd Op = "add"
	// OpRemove is bytes released from an instance.
	OpRemove Op = "remove"
	// OpRefuse is a write the ceiling turned away; Bytes is what was asked for.
	OpRefuse Op = "refuse"
)

// Accounted is one operation, with the tenant spelled out flat. Environment is a
// tenancy axis: two environments of one project are separately accounted.
type Accounted struct {
	Project     string
	Environment string
	Bucket      string
	Instance    string

	// Writer is the node whose row moved. A sum over one writer is what that
	// node did; a sum over an instance is what the cloud did.
	Writer string

	Op    Op
	Bytes uint64
}

// record builds and hands over one event, or does nothing. On index, where the
// accounting rows are written, so the two cannot drift.
func (i index) record(ctx context.Context, a Address, writer string, op Op, bytes uint64) {
	if i.meter == nil {
		return
	}
	b := a.Bucket()
	i.meter.Record(ctx, Accounted{
		Project:     b.Project(),
		Environment: b.Environment(),
		Bucket:      b.Name(),
		Instance:    a.Instance(),
		Writer:      writer,
		Op:          op,
		Bytes:       bytes,
	})
}
