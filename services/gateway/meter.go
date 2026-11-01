package gateway

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"time"
)

// Bandwidth is a billed unit, and this is the only service that sees every byte in
// and out, including everything refused before a worker was asked. The shape matches
// services/storage, services/build and services/worker so a sink totals all four
// alike. A nil Meter costs a nil check.

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
	// rate limit, a body over the ceiling. A worker never sees it, yet it costs
	// bandwidth and CPU.
	OpRefuse Op = "refuse"

	// OpAttach is a browser attached to a bus, reported once when it leaves. No
	// workload was invoked, so it is not a serve, and the edge did what it was asked,
	// so it is not a refusal. Its duration is what a sink totals.
	OpAttach Op = "attach"
)

// Accounted is one request that crossed the edge. For a refusal the project may be
// empty: a name nothing answers on belongs to no project.
type Accounted struct {
	Project     string
	Environment string

	// Host is the name the request arrived on. It is here even when the
	// project is not, because it is the only attribution an unmatched request
	// has.
	Host string

	Op     Op
	Status int

	// In and Out are body bytes. Headers are bounded by the server's limit and
	// counting them would make the figure depend on the client's user agent.
	In  int64
	Out int64

	Duration time.Duration
}

// emit hands over one event, or does nothing without a Meter. The name matches
// the other emitting services.
func (s *Service) emit(ctx context.Context, ev Accounted) {
	if s.meter == nil {
		return
	}
	s.meter.Record(ctx, ev)
}

// crossing is the response writer, counting. Wrapping covers every exit of the
// handler, including ones added later.
type crossing struct {
	w http.ResponseWriter

	host        string
	project     string
	environment string

	started time.Time
	status  int
	in      int64
	out     int64

	// served says a workload answered; anything else is a refusal.
	served bool

	// attached says this crossing was a websocket on a bus.
	attached bool
}

// Hijack passes the connection up, because a websocket cannot be spoken
// through an http.ResponseWriter and this wrapper sits between the two.
//
// WITHOUT IT EVERY ATTACH FAILS, and it fails inside gorilla's Upgrade with
// "response does not implement http.Hijacker" — a message that names this
// file's counter and nothing about a bus.
func (c *crossing) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := c.w.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return h.Hijack()
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
		// net/http writes 200 for a body with no explicit status.
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
	switch {
	case c.attached:
		op = OpAttach
	case c.served:
		op = OpServe
	}
	status := c.status
	if status == 0 {
		// A handler that wrote nothing answered 200 by net/http's rule.
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
