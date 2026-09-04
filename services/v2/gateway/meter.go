package gateway

import (
	"context"
	"net/http"
	"time"
)

// meter.go is how something OUTSIDE this service learns what it accounted for.
//
// BANDWIDTH IS THE THIRD BILLED UNIT and the one with the widest spread between
// plans, and this is the only service that sees every byte in and out. A worker
// sees a request that already arrived and a response it hands back; what
// crossed the edge — including everything this refused before a worker was ever
// asked — is visible here and nowhere else.
//
// IT IS THE SHAPE services/storage, services/build AND services/worker DECLARE,
// deliberately: a sink that can total three should not have to learn a fourth
// vocabulary.
//
// A nil Meter is the ordinary case and costs a nil check.

// Meter is told about a request this service accounted for. Record must not
// block: it is called on the request path.
type Meter interface {
	Record(ctx context.Context, ev Accounted)
}

// Op is what happened. It is a closed set because an emission nothing can
// enumerate is an emission nothing can total.
type Op string

const (
	// OpServe is a request that reached a workload and was answered.
	OpServe Op = "serve"

	// OpRefuse is a request the edge turned away: an unknown name, no route, a
	// rate limit, a body over the ceiling.
	//
	// IT IS THE HALF A WORKER CANNOT SEE. A flood that never reaches a
	// workload costs this cloud bandwidth and CPU and shows up in no
	// invocation count, which is exactly the traffic somebody disputing a bill
	// asks about.
	OpRefuse Op = "refuse"
)

// Accounted is one request that crossed the edge.
//
// THE TENANT IS SPELLED OUT, and for a refusal it may be empty: a request for a
// name nothing answers on belongs to no project, which is a real answer and one
// a sink has to be able to total separately.
type Accounted struct {
	Project     string
	Environment string

	// Host is the name the request arrived on. It is here even when the
	// project is not, because it is the only attribution an unmatched request
	// has.
	Host string

	Op     Op
	Status int

	// In and Out are the bytes of the request and response BODIES. Headers are
	// not counted: they are bounded by the server's own header limit and
	// counting them would make the figure depend on how a client spells its
	// user agent.
	In  int64
	Out int64

	Duration time.Duration
}

// emit hands over one event, or does nothing at all.
//
// IT IS NOT CALLED record, for the reason services/worker's is not: one
// spelling across the services that emit is worth more than a name that reads
// marginally better in one of them.
func (s *Service) emit(ctx context.Context, ev Accounted) {
	if s.meter == nil {
		return
	}
	s.meter.Record(ctx, ev)
}

// crossing is the response writer, counting.
//
// IT WRAPS RATHER THAN COUNTING AT EACH RETURN because this handler has
// seventeen exits and a counter written as a line before each one is a counter
// that is wrong the first time somebody adds an eighteenth.
type crossing struct {
	w http.ResponseWriter

	host        string
	project     string
	environment string

	started time.Time
	status  int
	in      int64
	out     int64

	// served says a workload answered. Everything else — an unknown name, no
	// route, a rate limit, a body over the ceiling — is a refusal, and it is
	// the half a worker cannot see.
	served bool
}

func (c *crossing) Header() http.Header { return c.w.Header() }

func (c *crossing) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
	c.w.WriteHeader(status)
}

func (c *crossing) Write(p []byte) (int, error) {
	if c.status == 0 {
		// net/http writes 200 for a body with no explicit status, and an
		// accounting that missed that would report every plain success as
		// having no status at all.
		c.status = http.StatusOK
	}
	n, err := c.w.Write(p)
	c.out += int64(n)
	return n, err
}

// Flush passes a flush through, because a streamed response that stopped
// flushing at this layer would be a response that arrives all at once.
func (c *crossing) Flush() {
	if f, ok := c.w.(http.Flusher); ok {
		f.Flush()
	}
}

func (c *crossing) accounted(now time.Time) Accounted {
	op := OpRefuse
	if c.served {
		op = OpServe
	}
	status := c.status
	if status == 0 {
		// A handler that returned without writing anything answered 200 by
		// net/http's own rule.
		status = http.StatusOK
	}
	return Accounted{
		Project:     c.project,
		Environment: c.environment,
		Host:        c.host,
		Op:          op,
		Status:      status,
		In:          c.in,
		Out:         c.out,
		Duration:    now.Sub(c.started),
	}
}
