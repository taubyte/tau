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

// client.go is the other side of rpc.go, and it is what lets a worker stop
// holding queues and logs.
//
// IT SATISFIES THE TIER'S OWN SEAMS. pkg/invoke/wasm asks for a Queues — "send
// to the queue this grant names" — and a Logs, and node/queue.go and node/log.go
// answer them today by opening a raft cluster in the worker's own process. Both
// surfaces below answer the SAME seams by asking this service, so the swap is
// one option at composition and nothing above the seam changes: the guest calls
// queue.send exactly as before and the item lands on a node that owns the
// namespace. Nothing in pkg/invoke/wasm changes. That is the value of the seam.
//
// THE GRANT IS THE ADDRESS AND THE SERVICE READS THE REST. A grant carries the
// handle — project, environment, resource id — which is precisely the Ref the
// wire takes. Everything else on it (the visibility timeout, the retry budget,
// the dead letter, the retention) is deliberately NOT sent: the service reads
// the declaration itself, so a worker cannot describe a queue into being
// something else.

// Consumers is the claim/settle seam a dispatcher uses.
//
// IT IS NOT AN ABI AND IT IS NOT A HOST FUNCTION, which node/dispatch.go already
// says: `send` is something a guest calls and `receive` is a DELIVERY — something
// claims an item and invokes a workload with it. That claiming half is the
// worker's, so this service defines the seam and drives nothing through it.
//
// THE GRANT IS THE ADDRESS HERE TOO, so a dispatcher holding what it needs to
// invoke a workload already holds what it needs to claim for one.
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
// will generate and distribute what it owes.
//
// IT IS THE SAME SEAM LOCAL AND REMOTE, which is what lets the composer decide
// WHICH node without this package knowing whether that means a stream. Both
// *Tier and *Client answer it, so the leader's own node is the absence of a hop
// rather than a second code path.
type Clocks interface {
	// CronTask hands the window `(since, upto]` to this node. A refusal is a
	// window nothing spent: the leader hands it out again, widened.
	CronTask(ctx context.Context, since, upto time.Time) error
}

// ---------------------------------------------------------------- local

// Local is this service as the compute tier's own seam, with no stream.
//
// IT IS THE ABSENCE OF A HOP AND NOT A SHORTCUT. A node that runs the primitives
// service already holds the namespace, so dialling itself to reach it would add
// a stream and a serialization to every call — the rule node/secrets.go keeps
// for a vault and services/database keeps for an engine. A node that does NOT
// run the service uses Client, and nothing above the seam can tell which it got.
func (s *Service) Local() *Tier { return &Tier{service: s} }

// Tier answers every seam a compute node needs, in this process.
type Tier struct{ service *Service }

var (
	_ wasmtier.Queues = (*Tier)(nil)
	_ wasmtier.Logs   = (*Tier)(nil)
	_ Consumers       = (*Tier)(nil)
	_ Clocks          = (*Tier)(nil)
)

// CronTask takes one window of the clock in this process.
//
// IT IS THE HANDLER'S OWN CALL WITHOUT THE STREAM. rpc.go reads how far the
// tasking has come off a header and then reaches [crons.accept]; a window that
// never left this node has come nowhere, so it reaches the same door with a
// hop count of zero and everything after it is decided in one place.
func (t *Tier) CronTask(ctx context.Context, since, upto time.Time) error {
	return t.service.crons.accept(ctx, since, upto)
}

// EVERY METHOD BELOW IS AN ADAPTER, exactly as rpc.go's handlers are: take the
// address off the grant, call the ONE implementation in service.go, put the
// tier's arm on what comes back. The wire adapter differs in two lines — it
// takes the address off a Ref and encodes a status instead — and in nothing
// else, which is what makes "the local arm and the remote arm agree" a property
// of the code rather than of two authors' attention.

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

// ---------------------------------------------------------------- remote

// Client reaches a primitives service.
type Client struct {
	conn *rpc.Client
	svc  primitivesv2connect.PrimitivesServiceClient
}

// NewClient dials one. The peer is the caller's to choose — placement, or a view
// of who runs the service — because which node holds the namespace is a routing
// question and not this package's.
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

// CronTask hands one window of the clock to the node this client reaches.
//
// THE HOP COUNT IS THE CALLER'S ROUTE AND NOT THE WINDOW'S, so it travels as a
// header — services/database/forward.go's rule — and it is read off the
// context rather than taken as an argument, so a node passing a tasking on
// cannot forget to say that it did.
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
