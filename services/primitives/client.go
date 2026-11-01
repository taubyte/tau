package primitives

import (
	"context"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	primitivesv2 "github.com/taubyte/tau/pkg/proto/gen/tau/primitives/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/primitives/v2/primitivesv2connect"
)

// Client and Tier answer the compute tier's Queues and Logs seams by asking this
// service, so a worker no longer holds queues and logs; pkg/invoke/wasm is unchanged.
// Only the grant's handle is sent; the service reads the declaration itself, so a
// worker cannot describe a queue into something else.

// Consumers is the claim/settle seam a dispatcher uses. Not a host function: claiming
// an item and invoking a workload with it is the worker's; this service defines the
// seam.
type Consumers interface {
	// Claim hands out up to max items. An empty answer means nothing is
	// deliverable, which is NOT an empty queue.
	Claim(ctx context.Context, grant invoke.Grant, max int) ([]Delivery, error)

	// Settle ends one delivery: done discards the item, and not-done gives it
	// back now rather than waiting out the visibility timeout.
	Settle(ctx context.Context, grant invoke.Grant, delivery Delivery, done bool) error

	// Depth is what a queue and its dead letter hold, for an operator asking.
	Depth(ctx context.Context, grant invoke.Grant) (Depth, error)
}

// Clocks is the tasking seam: one window of the clock, handed to a node that
// generates and distributes what it owes. *Tier and *Client both answer it.
type Clocks interface {
	// CronTask hands the window `(since, upto]` to this node. A refusal is a
	// window nothing spent: the leader hands it out again, widened.
	CronTask(ctx context.Context, since, upto time.Time) error
}

// Local is this service as the compute tier's seam, with no stream; a node not
// running the service uses Client.
func (s *Service) Local() *Tier { return &Tier{service: s} }

// Tier answers every seam a compute node needs, in this process.
type Tier struct{ service *Service }

var (
	_ wasmtier.Queues = (*Tier)(nil)
	_ wasmtier.Logs   = (*Tier)(nil)
	_ Consumers       = (*Tier)(nil)
	_ Clocks          = (*Tier)(nil)
)

// CronTask takes one window of the clock in this process, through crons.accept with a
// hop count of zero.
func (t *Tier) CronTask(ctx context.Context, since, upto time.Time) error {
	return t.service.crons.accept(ctx, since, upto)
}

// Every method below adapts the grant to the one implementation in service.go, as
// rpc.go's handlers adapt a Ref, so the local and remote arms agree by construction.

func (t *Tier) Send(ctx context.Context, grant invoke.Grant, id string, data []byte) error {
	at, err := addressFrom(grant.Handle)
	if err != nil {
		return tierError(err)
	}
	return tierError(t.service.send(ctx, at, id, data))
}

func (t *Tier) Claim(ctx context.Context, grant invoke.Grant, max int) ([]Delivery, error) {
	at, err := addressFrom(grant.Handle)
	if err != nil {
		return nil, tierError(err)
	}
	claimed, err := t.service.claim(ctx, at, max)
	return claimed, tierError(err)
}

func (t *Tier) Settle(ctx context.Context, grant invoke.Grant, delivery Delivery, done bool) error {
	at, err := addressFrom(grant.Handle)
	if err != nil {
		return tierError(err)
	}
	return tierError(t.service.settle(ctx, at, delivery, done))
}

func (t *Tier) Depth(ctx context.Context, grant invoke.Grant) (Depth, error) {
	at, err := addressFrom(grant.Handle)
	if err != nil {
		return Depth{}, tierError(err)
	}
	depth, err := t.service.depth(ctx, at)
	return depth, tierError(err)
}

func (t *Tier) Append(ctx context.Context, grant invoke.Grant, data []byte) (string, error) {
	at, err := addressFrom(grant.Handle)
	if err != nil {
		return "", tierError(err)
	}
	cursor, err := t.service.appendRecord(ctx, at, data)
	return cursor, tierError(err)
}

func (t *Tier) Read(ctx context.Context, grant invoke.Grant, after string, limit uint32) ([]wasmtier.Record, error) {
	at, err := addressFrom(grant.Handle)
	if err != nil {
		return nil, tierError(err)
	}
	read, err := t.service.readRecords(ctx, at, after, limit)
	return read, tierError(err)
}

// Client reaches a primitives service.
type Client struct {
	conn *rpc.Client
	svc  primitivesv2connect.PrimitivesServiceClient
}

// NewClient dials one. Which node holds the namespace is the caller's routing
// question.
func NewClient(h host.Host, at libp2ppeer.ID) (*Client, error) {
	conn, err := rpc.NewClient(h, at, Protocol)
	if err != nil {
		return nil, err
	}
	return &Client{
		conn: conn,
		svc:  primitivesv2connect.NewPrimitivesServiceClient(conn.HTTP(), conn.URL()),
	}, nil
}

func (c *Client) Close() error { c.conn.Close(); return nil }

var (
	_ wasmtier.Queues = (*Client)(nil)
	_ wasmtier.Logs   = (*Client)(nil)
	_ Consumers       = (*Client)(nil)
	_ Clocks          = (*Client)(nil)
)

// CronTask hands one window to the node this client reaches. The hop count travels as
// a header, read off the context so a forwarding node cannot forget it.
func (c *Client) CronTask(ctx context.Context, since, upto time.Time) error {
	req := connect.NewRequest(&primitivesv2.CronTaskRequest{
		Since: since.Unix(), Upto: upto.Unix(),
	})
	req.Header().Set(hopHeader, strconv.Itoa(hopsOf(ctx)+1))

	_, err := c.svc.CronTask(ctx, req)
	return taskError(err)
}

func (c *Client) Send(ctx context.Context, grant invoke.Grant, id string, data []byte) error {
	ref, err := refOf(grant)
	if err != nil {
		return err
	}
	_, err = c.svc.QueueSend(ctx, connect.NewRequest(&primitivesv2.QueueSendRequest{
		Ref: ref, Id: id, Data: data,
	}))
	return remoteError(err)
}

func (c *Client) Claim(ctx context.Context, grant invoke.Grant, max int) ([]Delivery, error) {
	ref, err := refOf(grant)
	if err != nil {
		return nil, err
	}
	out, err := c.svc.QueueClaim(ctx, connect.NewRequest(&primitivesv2.QueueClaimRequest{
		Ref: ref, Max: uint32(max),
	}))
	if err != nil {
		return nil, remoteError(err)
	}

	claimed := make([]Delivery, 0, len(out.Msg.GetDeliveries()))
	for _, one := range out.Msg.GetDeliveries() {
		claimed = append(claimed, deliveryFrom(one))
	}
	return claimed, nil
}

func (c *Client) Settle(ctx context.Context, grant invoke.Grant, delivery Delivery, done bool) error {
	ref, err := refOf(grant)
	if err != nil {
		return err
	}
	_, err = c.svc.QueueSettle(ctx, connect.NewRequest(&primitivesv2.QueueSettleRequest{
		Ref: ref, Delivery: deliveryTo(delivery), Done: done,
	}))
	return remoteError(err)
}

func (c *Client) Depth(ctx context.Context, grant invoke.Grant) (Depth, error) {
	ref, err := refOf(grant)
	if err != nil {
		return Depth{}, err
	}
	out, err := c.svc.QueueDepth(ctx, connect.NewRequest(ref))
	if err != nil {
		return Depth{}, remoteError(err)
	}
	return Depth{
		Ready: out.Msg.GetReady(), Claimed: out.Msg.GetClaimed(), Dead: out.Msg.GetDead(),
	}, nil
}

func (c *Client) Append(ctx context.Context, grant invoke.Grant, data []byte) (string, error) {
	ref, err := refOf(grant)
	if err != nil {
		return "", err
	}
	out, err := c.svc.LogAppend(ctx, connect.NewRequest(&primitivesv2.LogAppendRequest{
		Ref: ref, Data: data,
	}))
	if err != nil {
		return "", remoteError(err)
	}
	return out.Msg.GetCursor(), nil
}

func (c *Client) Read(ctx context.Context, grant invoke.Grant, after string, limit uint32) ([]wasmtier.Record, error) {
	ref, err := refOf(grant)
	if err != nil {
		return nil, err
	}
	out, err := c.svc.LogRead(ctx, connect.NewRequest(&primitivesv2.LogReadRequest{
		Ref: ref, After: after, Limit: limit,
	}))
	if err != nil {
		return nil, remoteError(err)
	}

	records := make([]wasmtier.Record, 0, len(out.Msg.GetRecords()))
	for _, one := range out.Msg.GetRecords() {
		records = append(records, wasmtier.Record{
			Cursor: one.GetCursor(), At: one.GetAt(), Data: one.GetData(),
		})
	}
	return records, nil
}

// refOf is the grant's handle as a Ref, refused here rather than sent as three
// empty strings for the service to reject.
func refOf(grant invoke.Grant) (*primitivesv2.Ref, error) {
	at, err := addressFrom(grant.Handle)
	if err != nil {
		return nil, err
	}
	return at.ref(), nil
}

// nanos reads a deadline off the wire. Zero is a delivery with no deadline
// rather than one due at the epoch, which is the difference between "not
// claimed" and "expired fifty years ago".
func nanos(at int64) time.Time {
	if at == 0 {
		return time.Time{}
	}
	return time.Unix(0, at)
}
