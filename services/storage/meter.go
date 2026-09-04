package storage

import "context"

// meter.go is how something OUTSIDE this service learns what it accounted for.
//
// quota.go already counts: every write bumps a per-writer row and Admit reads
// the sum back. That counter is the right thing for what it does — enforcing a
// bucket instance's declared capacity — but it is this service answering its
// own question. It cannot be aggregated with anything, it carries no tenant on
// it, and the only code that ever reads a row is the code that wrote it.
//
// A COUNTER IS NOT AN EMISSION. The two are different in the way that matters
// here: a counter says what the current figure is, an emission says what
// happened. Anything that wants to attribute, aggregate across services, or
// answer a question this package did not anticipate needs the second, and
// cannot reconstruct it from the first.
//
// It is declared now, with the write paths, rather than added later. Retro-
// fitting an emission means touching every accounted path in a service that by
// then has a test suite pinned to those paths, which is how the same thing was
// done expensively once already.
//
// A nil Meter is the ordinary case and costs a nil check. There is no default
// implementation and no registry: a sink is handed in by whatever composes the
// node, or there is none and the events are not built at all.

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
	// OpRefuse is a write the ceiling turned away. Bytes is what was asked
	// for, not what was stored — nothing was stored. It is emitted because a
	// refusal is the event a caller most wants explained and the one the
	// counter cannot show: the rows look identical before and after.
	OpRefuse Op = "refuse"
)

// Accounted is one operation, with the tenant on it.
//
// THE TENANT IS SPELLED OUT RATHER THAN CARRIED AS A Bucket. A sink is
// downstream of this package and should not have to know how an address is
// built, and a value that crosses a service boundary should be flat enough to
// serialise without this package's constructors.
//
// Environment is a field of its own because environment is a tenancy axis
// here, not a label: two environments of one project are separately accounted.
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

// record builds and hands over one event, or does nothing at all. It is a
// method on index because index is where the accounting rows are written, and
// an emission that could drift from the row it describes is worse than none.
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
