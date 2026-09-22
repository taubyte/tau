package primitives

import (
	"context"
	"net/http"
	"time"

	"connectrpc.com/connect"

	primitivesv1 "github.com/taubyte/tau/pkg/proto/gen/tau/primitives/v1"
	"github.com/taubyte/tau/pkg/proto/gen/tau/primitives/v1/primitivesv1connect"
)

// rpc.go is the whole wire surface: six calls on a named primitive, each one
// resolution and one operation — and a seventh that names none.
//
// EVERY HANDLER IS AN ADAPTER AND NOTHING ELSE: decode an address off the Ref,
// call the same [Service] method the compute tier calls, encode the refusal.
// Nothing here resolves, checks or operates, because the arm a caller gets is
// chosen by p2p/rpc's short-circuit and is invisible above this package — so
// two implementations that merely AGREE would drift the day one was edited and
// nothing would notice. See the operations section of service.go.
//
// NO HANDLER VALIDATES AN ADDRESS. address.valid() is the one check, run where
// a Ref becomes an address. A check repeated here would be a second opinion, and
// the day the two disagreed the answer would depend on which call a caller used.
//
// CronTask IS THE ODD ONE AND IT IS ODD ON PURPOSE. It carries no Ref, because
// a window is not about a resource: what it names is a stretch of the clock,
// and which crons fall inside it is the tasked node's own deployments to say.
// So it resolves nothing and routes instead — see [crons.accept].

// mount is the one handler rpc.Listen serves.
func (s *Service) mount() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(primitivesv1connect.NewPrimitivesServiceHandler(&handler{s}))
	return mux
}

type handler struct{ s *Service }

var _ primitivesv1connect.PrimitivesServiceHandler = (*handler)(nil)

func (h *handler) QueueSend(ctx context.Context, req *connect.Request[primitivesv1.QueueSendRequest]) (*connect.Response[primitivesv1.Empty], error) {
	if err := h.s.send(ctx, addressOf(req.Msg.GetRef()), req.Msg.GetId(), req.Msg.GetData()); err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&primitivesv1.Empty{}), nil
}

// QueueClaim hands items out exclusively. An empty answer is not a refusal: the
// queue may still hold items that are delayed, backing off, or claimed by
// somebody else.
func (h *handler) QueueClaim(ctx context.Context, req *connect.Request[primitivesv1.QueueClaimRequest]) (*connect.Response[primitivesv1.Deliveries], error) {
	claimed, err := h.s.claim(ctx, addressOf(req.Msg.GetRef()), int(req.Msg.GetMax()))
	if err != nil {
		return nil, wire(err)
	}

	out := make([]*primitivesv1.Delivery, 0, len(claimed))
	for _, one := range claimed {
		out = append(out, deliveryTo(one))
	}
	return connect.NewResponse(&primitivesv1.Deliveries{Deliveries: out}), nil
}

func (h *handler) QueueSettle(ctx context.Context, req *connect.Request[primitivesv1.QueueSettleRequest]) (*connect.Response[primitivesv1.Empty], error) {
	at := addressOf(req.Msg.GetRef())
	if err := h.s.settle(ctx, at, deliveryFrom(req.Msg.GetDelivery()), req.Msg.GetDone()); err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&primitivesv1.Empty{}), nil
}

func (h *handler) QueueDepth(ctx context.Context, req *connect.Request[primitivesv1.Ref]) (*connect.Response[primitivesv1.QueueDepthValue], error) {
	depth, err := h.s.depth(ctx, addressOf(req.Msg))
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&primitivesv1.QueueDepthValue{
		Ready: depth.Ready, Claimed: depth.Claimed, Dead: depth.Dead,
	}), nil
}

func (h *handler) LogAppend(ctx context.Context, req *connect.Request[primitivesv1.LogAppendRequest]) (*connect.Response[primitivesv1.LogCursor], error) {
	cursor, err := h.s.appendRecord(ctx, addressOf(req.Msg.GetRef()), req.Msg.GetData())
	if err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&primitivesv1.LogCursor{Cursor: cursor}), nil
}

func (h *handler) LogRead(ctx context.Context, req *connect.Request[primitivesv1.LogReadRequest]) (*connect.Response[primitivesv1.LogRecords], error) {
	read, err := h.s.readRecords(ctx, addressOf(req.Msg.GetRef()), req.Msg.GetAfter(), req.Msg.GetLimit())
	if err != nil {
		return nil, wire(err)
	}

	out := make([]*primitivesv1.LogRecord, 0, len(read))
	for _, record := range read {
		out = append(out, &primitivesv1.LogRecord{
			Cursor: record.Cursor, At: record.At, Data: record.Data,
		})
	}
	return connect.NewResponse(&primitivesv1.LogRecords{Records: out}), nil
}

// CronTask takes one window of the clock. The answer is the whole of the
// re-tasking protocol: a refusal is a window the leader has not spent.
func (h *handler) CronTask(ctx context.Context, req *connect.Request[primitivesv1.CronTaskRequest]) (*connect.Response[primitivesv1.Empty], error) {
	// HOW FAR IT HAS COME IS READ HERE AND NOWHERE ELSE, so the one place that
	// decides whether this window may be sent on reads one number that can
	// never go down. See [hopsIn].
	ctx = hopsIn(ctx, req.Header())

	since, upto := windowOf(req.Msg)
	if err := h.s.crons.accept(ctx, since, upto); err != nil {
		return nil, wire(err)
	}
	return connect.NewResponse(&primitivesv1.Empty{}), nil
}

// windowOf is the tasked stretch of the clock, in UTC.
//
// UTC BECAUSE THE WINDOW IS COMPARED AGAINST MINUTES THE LEADER TRUNCATED IN
// IT, and time.Unix answers in the receiving node's own zone — which would put
// a schedule's slot walk in whatever TZ the operator's shell had.
func windowOf(msg *primitivesv1.CronTaskRequest) (since, upto time.Time) {
	return time.Unix(msg.GetSince(), 0).UTC(), time.Unix(msg.GetUpto(), 0).UTC()
}

func deliveryTo(d Delivery) *primitivesv1.Delivery {
	return &primitivesv1.Delivery{
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

func deliveryFrom(d *primitivesv1.Delivery) Delivery {
	return Delivery{
		ID: d.GetId(), Data: d.GetData(), Attempt: d.GetAttempt(),
		Deadline: nanos(d.GetDeadline()), Token: d.GetToken(),
	}
}
