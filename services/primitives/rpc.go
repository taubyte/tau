package primitives

import (
	"context"
	"net/http"
	"time"

	"connectrpc.com/connect"

	primitivesv2 "github.com/taubyte/tau/pkg/proto/gen/tau/primitives/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/primitives/v2/primitivesv2connect"
)

// The wire surface. Every handler is an adapter: decode the address off the Ref,
// call the same Service method the compute tier calls, encode the refusal. No handler
// validates an address (address.valid is the one check). CronTask carries no Ref: a
// window names a stretch of the clock, so it routes rather than resolves.

// mount is the one handler rpc.Listen serves.
func (s *Service) mount() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(primitivesv2connect.NewPrimitivesServiceHandler(&handler{s}))
	return mux
}

type handler struct{ s *Service }

var _ primitivesv2connect.PrimitivesServiceHandler = (*handler)(nil)

func (h *handler) QueueSend(ctx context.Context, req *connect.Request[primitivesv2.QueueSendRequest]) (*connect.Response[primitivesv2.Empty], error) {
	if err := h.s.send(ctx, addressOf(req.Msg.GetRef()), req.Msg.GetId(), req.Msg.GetData()); err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&primitivesv2.Empty{}), nil
}

// QueueClaim hands items out exclusively. An empty answer is not a refusal: the
// queue may still hold items that are delayed, backing off, or claimed by
// somebody else.
func (h *handler) QueueClaim(ctx context.Context, req *connect.Request[primitivesv2.QueueClaimRequest]) (*connect.Response[primitivesv2.Deliveries], error) {
	claimed, err := h.s.claim(ctx, addressOf(req.Msg.GetRef()), int(req.Msg.GetMax()))
	if err != nil {
		return nil, wire(err)
	}

	out := make([]*primitivesv2.Delivery, 0, len(claimed))
	for _, one := range claimed {
		out = append(out, deliveryTo(one))
	}
	return connect.NewResponse(&primitivesv2.Deliveries{Deliveries: out}), nil
}

func (h *handler) QueueSettle(ctx context.Context, req *connect.Request[primitivesv2.QueueSettleRequest]) (*connect.Response[primitivesv2.Empty], error) {
	at := addressOf(req.Msg.GetRef())
	if err := h.s.settle(ctx, at, deliveryFrom(req.Msg.GetDelivery()), req.Msg.GetDone()); err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&primitivesv2.Empty{}), nil
}

func (h *handler) QueueDepth(ctx context.Context, req *connect.Request[primitivesv2.Ref]) (*connect.Response[primitivesv2.QueueDepthValue], error) {
	depth, err := h.s.depth(ctx, addressOf(req.Msg))
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&primitivesv2.QueueDepthValue{
		Ready: depth.Ready, Claimed: depth.Claimed, Dead: depth.Dead,
	}), nil
}

func (h *handler) LogAppend(ctx context.Context, req *connect.Request[primitivesv2.LogAppendRequest]) (*connect.Response[primitivesv2.LogCursor], error) {
	cursor, err := h.s.appendRecord(ctx, addressOf(req.Msg.GetRef()), req.Msg.GetData())
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&primitivesv2.LogCursor{Cursor: cursor}), nil
}

func (h *handler) LogRead(ctx context.Context, req *connect.Request[primitivesv2.LogReadRequest]) (*connect.Response[primitivesv2.LogRecords], error) {
	read, err := h.s.readRecords(ctx, addressOf(req.Msg.GetRef()), req.Msg.GetAfter(), req.Msg.GetLimit())
	if err != nil {
		return nil, wire(err)
	}

	out := make([]*primitivesv2.LogRecord, 0, len(read))
	for _, record := range read {
		out = append(out, &primitivesv2.LogRecord{
			Cursor: record.Cursor, At: record.At, Data: record.Data,
		})
	}
	return connect.NewResponse(&primitivesv2.LogRecords{Records: out}), nil
}

// CronTask takes one window of the clock. The answer is the whole of the
// re-tasking protocol: a refusal is a window the leader has not spent.
func (h *handler) CronTask(ctx context.Context, req *connect.Request[primitivesv2.CronTaskRequest]) (*connect.Response[primitivesv2.Empty], error) {
	// HOW FAR IT HAS COME IS READ HERE AND NOWHERE ELSE, so the one place that
	// decides whether this window may be sent on reads one number that can
	// never go down. See [hopsIn].
	ctx = hopsIn(ctx, req.Header())

	since, upto := windowOf(req.Msg)
	if err := h.s.crons.accept(ctx, since, upto); err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&primitivesv2.Empty{}), nil
}

// windowOf is the tasked stretch of the clock, in UTC.
//
// UTC BECAUSE THE WINDOW IS COMPARED AGAINST MINUTES THE LEADER TRUNCATED IN
// IT, and time.Unix answers in the receiving node's own zone — which would put
// a schedule's slot walk in whatever TZ the operator's shell had.
func windowOf(msg *primitivesv2.CronTaskRequest) (since, upto time.Time) {
	return time.Unix(msg.GetSince(), 0).UTC(), time.Unix(msg.GetUpto(), 0).UTC()
}

func deliveryTo(d Delivery) *primitivesv2.Delivery {
	return &primitivesv2.Delivery{
		Id: d.ID, Data: d.Data, Attempt: d.Attempt,
		Deadline: unixNanos(d.Deadline), Token: d.Token,
	}
}

// unixNanos is the half of the round trip [nanos] reads back. A delivery with
// no deadline travels as ZERO, which is the value nanos has an arm for —
// time.Time's own zero is year one, and UnixNano() on it is a large negative
// number that comes back as a deadline decades in the wrong direction.
func unixNanos(at time.Time) int64 {
	if at.IsZero() {
		return 0
	}
	return at.UnixNano()
}

func deliveryFrom(d *primitivesv2.Delivery) Delivery {
	return Delivery{
		ID: d.GetId(), Data: d.GetData(), Attempt: d.GetAttempt(),
		Deadline: nanos(d.GetDeadline()), Token: d.GetToken(),
	}
}
