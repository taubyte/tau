package database

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/database"
	kvengine "github.com/taubyte/tau/pkg/database/engines/kv"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	databasev1 "github.com/taubyte/tau/pkg/proto/gen/tau/database/v1"
	"github.com/taubyte/tau/pkg/proto/gen/tau/database/v1/databasev1connect"
)

// client.go is the other side of rpc.go, and it is what lets a worker stop
// holding databases.
//
// IT SATISFIES THE TIER'S OWN SEAM. pkg/invoke/wasm asks for a KVs — "open the
// kv this grant names" — and node/database.go answers it today by opening the
// engine in the worker's own process. This answers the same seam by asking the
// database service, so the swap is one option at composition and nothing above
// the seam changes: the guest calls kv::get exactly as before and the bytes
// come from a node that owns the engine.
//
// THE GRANT IS THE ADDRESS AND THE SERVICE READS THE REST. A grant carries the
// handle — project, environment, resource id — which is precisely the Ref the
// wire takes. Everything else on the grant (the engine, the host, the tls mode)
// is deliberately NOT sent: the service reads the declaration itself, so a
// worker cannot describe a database into being something else.

// Client reaches a database service.
type Client struct {
	conn *rpc.Client
	svc  databasev1connect.DatabaseServiceClient
}

// NewClient dials one. The peer is the caller's to choose — placement, or a
// view of who runs the service — because which node holds an engine is a
// routing question and not this package's.
func NewClient(h host.Host, at libp2ppeer.ID) (*Client, error) {
	conn, err := rpc.NewClient(h, at, Protocol)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, svc: databasev1connect.NewDatabaseServiceClient(conn.HTTP(), conn.URL())}, nil
}

func (c *Client) Close() error { c.conn.Close(); return nil }

var _ wasmtier.KVs = (*Client)(nil)

// OpenKV answers the tier's seam. It opens NOTHING: the handle is carried and
// every operation names it, because what is resident is the service's business
// and a handle here would be this process holding a database again.
func (c *Client) OpenKV(_ context.Context, grant invoke.Grant) (wasmtier.KV, error) {
	project, environment, id := splitHandle(grant.Handle)
	if project == "" || environment == "" || id == "" {
		return nil, fmt.Errorf("%w: %q", ErrNoID, grant.Handle)
	}
	return &remoteKV{svc: c.svc, ref: &databasev1.Ref{
		Project: project, Environment: environment, Id: id,
	}}, nil
}

// remoteKV is one database, addressed per call.
type remoteKV struct {
	svc databasev1connect.DatabaseServiceClient
	ref *databasev1.Ref
}

func (r *remoteKV) Get(ctx context.Context, key string) ([]byte, error) {
	out, err := r.svc.KVGet(ctx, connect.NewRequest(&databasev1.KVGetRequest{Ref: r.ref, Key: key}))
	if err != nil {
		return nil, tierError(err)
	}
	return out.Msg.GetValue(), nil
}

func (r *remoteKV) Put(ctx context.Context, key string, value []byte) error {
	_, err := r.svc.KVPut(ctx, connect.NewRequest(&databasev1.KVPutRequest{Ref: r.ref, Key: key, Value: value}))
	return tierError(err)
}

func (r *remoteKV) Delete(ctx context.Context, key string) error {
	_, err := r.svc.KVDelete(ctx, connect.NewRequest(&databasev1.KVDeleteRequest{Ref: r.ref, Key: key}))
	return tierError(err)
}

// Keys pages until the service says there are no more, because the tier's seam
// asks for every key under a prefix and the wire deliberately pages.
func (r *remoteKV) Keys(ctx context.Context, prefix string) ([]string, error) {
	var out []string
	after := ""
	for {
		page, err := r.svc.KVList(ctx, connect.NewRequest(&databasev1.KVListRequest{
			Ref: r.ref, Prefix: prefix, After: after,
		}))
		if err != nil {
			return nil, tierError(err)
		}

		keys := page.Msg.GetKeys()
		out = append(out, keys...)
		if !page.Msg.GetMore() || len(keys) == 0 {
			return out, nil
		}
		after = keys[len(keys)-1]
	}
}

// tierError turns a wire status back into the error the tier's own ABI answers
// with, so a guest sees the same arm whether the engine was in this process or
// on another node.
//
// THE CODE AND NOT THE SENTENCE. rpc.go decides the mapping out; this is the
// mapping back, and the two are the one table read in both directions.
func tierError(err error) error {
	if err == nil {
		return nil
	}
	// A MISS IS THE ONE ARM THE ABI NAMES, so it is the one translated: a guest
	// asking for a key nobody wrote must see the same variant whether the engine
	// was in this process or on another node.
	//
	// EVERYTHING ELSE PASSES THROUGH WITH THE SERVICE'S OWN SENTENCE. Folding a
	// refusal this tier has no arm for into one it does — malformed, say —
	// would tell a guest its json was bad when the truth is that this cloud does
	// not serve raft.
	if connect.CodeOf(err) == connect.CodeNotFound {
		return fmt.Errorf("%w: %s", wasmtier.ErrNotFound, err)
	}
	return err
}

// splitHandle takes a grant's handle apart. It is the same three segments
// node/database.go reads, spelled once here because this package is the other
// end of that move.
func splitHandle(handle string) (project, environment, id string) {
	var parts []string
	start := 0
	for i := range len(handle) {
		if handle[i] == '/' {
			parts = append(parts, handle[start:i])
			start = i + 1
		}
	}
	parts = append(parts, handle[start:])
	if len(parts) != 3 {
		return "", "", ""
	}
	return parts[0], parts[1], parts[2]
}

// kv is one database on the node this client reaches, in THIS package's own
// vocabulary rather than the tier's.
//
// IT IS FOR A FORWARD AND NOT FOR A WORKER. OpenKV above answers the compute
// tier's seam, where a miss is the tier's miss; this answers database.KVConn,
// so a routed connection can put the same operation somewhere else without the
// two surfaces having to agree about anything but the wire.
//
// THE HOP COUNT TRAVELS WITH THE CALL, which is what bounds forwarding — see
// forward.go.
func (c *Client) kv(at address, hops int) database.KVConn {
	return &oneKV{svc: c.svc, ref: c.ref(at), hops: hops}
}

// oneKV is one database on another node.
type oneKV struct {
	svc  databasev1connect.DatabaseServiceClient
	ref  *databasev1.Ref
	hops int
}

var _ database.KVConn = (*oneKV)(nil)

// sent marks a request with how far it has come, so the node that answers it
// knows whether it may send it on again.
func (o *oneKV) sent(req interface{ Header() http.Header }) {
	req.Header().Set(hopHeader, strconv.Itoa(o.hops))
}

func (o *oneKV) Get(ctx context.Context, key string) ([]byte, error) {
	req := connect.NewRequest(&databasev1.KVGetRequest{Ref: o.ref, Key: key})
	o.sent(req)
	out, err := o.svc.KVGet(ctx, req)
	if err != nil {
		return nil, refusal(err)
	}
	return out.Msg.GetValue(), nil
}

func (o *oneKV) Put(ctx context.Context, key string, value []byte) error {
	req := connect.NewRequest(&databasev1.KVPutRequest{Ref: o.ref, Key: key, Value: value})
	o.sent(req)
	_, err := o.svc.KVPut(ctx, req)
	return refusal(err)
}

func (o *oneKV) Delete(ctx context.Context, key string) error {
	req := connect.NewRequest(&databasev1.KVDeleteRequest{Ref: o.ref, Key: key})
	o.sent(req)
	_, err := o.svc.KVDelete(ctx, req)
	return refusal(err)
}

// List asks for every key under a prefix, paging until the node says there are
// no more — the surface this answers has no paging and the wire deliberately
// does, exactly as remoteKV.Keys resolves it for the tier.
func (o *oneKV) List(ctx context.Context, prefix string) ([]string, error) {
	var out []string
	after := ""
	for {
		req := connect.NewRequest(&databasev1.KVListRequest{Ref: o.ref, Prefix: prefix, After: after})
		o.sent(req)
		page, err := o.svc.KVList(ctx, req)
		if err != nil {
			return nil, refusal(err)
		}

		keys := page.Msg.GetKeys()
		out = append(out, keys...)
		if !page.Msg.GetMore() || len(keys) == 0 {
			return out, nil
		}
		after = keys[len(keys)-1]
	}
}

// Watch is not carried over the wire, because nothing carries it: the engine
// underneath answers the same refusal and a channel that never delivers would
// be worse than being told.
func (o *oneKV) Watch(context.Context, string) (<-chan string, error) {
	return nil, kvengine.ErrNoWatch
}

// Close releases nothing: the stream belongs to the Client that made this.
func (o *oneKV) Close() error { return nil }

// refusal is wire() read backwards: a status from another node becomes this
// package's own sentinel, so a forwarded answer reaches the caller as the arm a
// local one would have produced.
//
// THE CODE AND NOT THE SENTENCE, which is what tierError already says in the
// other direction. The two are the one table read both ways, and the sentence
// the answering node wrote travels along with it so an operator reads what
// actually happened rather than this node's guess at it.
//
// ONE CODE CAN STAND FOR TWO SENTINELS — NotFound is a key nobody wrote and an
// id nobody declared — and this picks the one a caller acts on. What survives
// the round trip is the CODE, which is the thing a caller decides with; naming
// that here is better than a second wire field nobody else would read.
func refusal(err error) error {
	if err == nil {
		return nil
	}

	// AN ANSWER OR A SILENCE, BEFORE ANY CODE IS READ. A peer that is gone, a
	// peer that never ran this service, a stream that died mid-call -- all of
	// them arrive as Unavailable, which is the same code a voter's own refusal
	// arrives as, and the caller has to walk on from one and stop at the other.
	// What tells them apart is the mark the answering node put on it.
	//
	// IT WRAPS ErrNotHere, so a walk over the voters continues past a node that
	// did not answer exactly as it continues past one that answered "not me".
	var status *connect.Error
	if !errors.As(err, &status) || status.Meta().Get(answerHeader) == "" {
		return fmt.Errorf("%w: %w", ErrUnreachable, err)
	}

	switch connect.CodeOf(err) {
	case connect.CodeNotFound:
		return fmt.Errorf("%w: %s", kvengine.ErrNotFound, err)
	case connect.CodeUnimplemented:
		return fmt.Errorf("%w: %s", ErrConsensusUnserved, err)
	case connect.CodeFailedPrecondition:
		return fmt.Errorf("%w: %s", ErrWrongModel, err)
	case connect.CodeInvalidArgument:
		return fmt.Errorf("%w: %s", ErrNoKey, err)
	case connect.CodeResourceExhausted:
		return fmt.Errorf("%w: %s", ErrTooManyResident, err)
	default:
		// UNAVAILABLE AND EVERYTHING UNNAMED ARE A RETRY, which is what wire()
		// answers for both — so passing it through unchanged reproduces the
		// code it came in as.
		return err
	}
}

// Digest is what the node this client reaches holds for one database, and
// whether it means to keep it.
//
// IT IS THE QUESTION A DEPARTING NODE ASKS ITS REPLACEMENTS. Placement says who
// SHOULD hold a database; this says who DOES, and whether they are about to
// stop. Dropping on the strength of the first loses the data when the second has
// not caught up; dropping on the strength of `held` alone loses it when the
// other node is leaving too.
func (c *Client) Digest(ctx context.Context, at address) (keeper, error) {
	out, err := c.svc.KVDigest(ctx, connect.NewRequest(c.ref(at)))
	if err != nil {
		return keeper{}, tierError(err)
	}
	return keeper{
		keys:  out.Msg.GetKeys(),
		sum:   out.Msg.GetDigest(),
		held:  out.Msg.GetHeld(),
		keeps: out.Msg.GetKeeps(),
	}, nil
}

// Hold is Digest without the digest: what the repair sweep asks of a co-holder,
// which needs presence rather than proof of convergence.
func (c *Client) Hold(ctx context.Context, at address) (keeper, error) {
	out, err := c.svc.Hold(ctx, connect.NewRequest(c.ref(at)))
	if err != nil {
		return keeper{}, tierError(err)
	}
	return keeper{held: out.Msg.GetHeld(), keeps: out.Msg.GetKeeps()}, nil
}

func (c *Client) ref(at address) *databasev1.Ref {
	return &databasev1.Ref{
		Project: at.project, Environment: at.environment, Id: at.id, Instance: at.instance,
	}
}
