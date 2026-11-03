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
	"github.com/taubyte/tau/pkg/proto/codec"
	databasev2 "github.com/taubyte/tau/pkg/proto/gen/tau/database/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/database/v2/databasev2connect"
)

// Client satisfies the tier's KVs seam by asking the database service, so a worker
// need not hold databases: the guest calls kv::get as before and the bytes come from
// the node owning the engine. Only the grant's handle is sent; the service reads the
// declaration itself, so a worker cannot describe a database into something else.

// Client reaches a database service.
type Client struct {
	conn *rpc.Client
	svc  databasev2connect.DatabaseServiceClient
}

// NewClient dials one. Which peer holds an engine is the caller's routing question.
func NewClient(h host.Host, at libp2ppeer.ID) (*Client, error) {
	conn, err := rpc.NewClient(h, at, Protocol)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, svc: databasev2connect.NewDatabaseServiceClient(conn.HTTP(), conn.URL(), codec.Option())}, nil
}

func (c *Client) Close() error { c.conn.Close(); return nil }

var _ wasmtier.KVs = (*Client)(nil)

// OpenKV answers the tier's seam. It opens nothing: every operation names the handle.
func (c *Client) OpenKV(_ context.Context, grant invoke.Grant) (wasmtier.KV, error) {
	project, environment, id := splitHandle(grant.Handle)
	if project == "" || environment == "" || id == "" {
		return nil, fmt.Errorf("%w: %q", ErrNoID, grant.Handle)
	}
	return &remoteKV{svc: c.svc, ref: &databasev2.Ref{
		Project: project, Environment: environment, Id: id,
	}}, nil
}

// remoteKV is one database, addressed per call.
type remoteKV struct {
	svc databasev2connect.DatabaseServiceClient
	ref *databasev2.Ref
}

func (r *remoteKV) Get(ctx context.Context, key string) ([]byte, error) {
	out, err := r.svc.KVGet(ctx, connect.NewRequest(&databasev2.KVGetRequest{Ref: r.ref, Key: key}))
	if err != nil {
		return nil, tierError(err)
	}
	return out.Msg.GetValue(), nil
}

func (r *remoteKV) Put(ctx context.Context, key string, value []byte) error {
	_, err := r.svc.KVPut(ctx, connect.NewRequest(&databasev2.KVPutRequest{Ref: r.ref, Key: key, Value: value}))
	return tierError(err)
}

func (r *remoteKV) Delete(ctx context.Context, key string) error {
	_, err := r.svc.KVDelete(ctx, connect.NewRequest(&databasev2.KVDeleteRequest{Ref: r.ref, Key: key}))
	return tierError(err)
}

// Keys pages until the service says there are no more, because the tier's seam
// asks for every key under a prefix and the wire deliberately pages.
func (r *remoteKV) Keys(ctx context.Context, prefix string) ([]string, error) {
	var out []string
	after := ""
	for {
		page, err := r.svc.KVList(ctx, connect.NewRequest(&databasev2.KVListRequest{
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

// tierError turns a wire status back into the tier ABI's error, so a guest sees the
// same arm whether the engine is local or remote. It is rpc.go's table read back.
func tierError(err error) error {
	if err == nil {
		return nil
	}
	// A miss is the one arm the ABI names, so it is the one translated; everything else
	// passes through with the service's own sentence.
	if connect.CodeOf(err) == connect.CodeNotFound {
		return fmt.Errorf("%w: %s", wasmtier.ErrNotFound, err)
	}
	return err
}

// splitHandle takes a grant's handle apart. It is the same three segments
// instance/database.go reads, spelled once here because this package is the other
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

// kv is one database on the node this client reaches, as a database.KVConn for a
// forward rather than the tier's seam. The hop count travels with the call (see
// forward.go).
func (c *Client) kv(at address, hops int) database.KVConn {
	return &oneKV{svc: c.svc, ref: c.ref(at), hops: hops}
}

// oneKV is one database on another node.
type oneKV struct {
	svc  databasev2connect.DatabaseServiceClient
	ref  *databasev2.Ref
	hops int
}

var _ database.KVConn = (*oneKV)(nil)

// sent marks a request with how far it has come, so the node that answers it
// knows whether it may send it on again.
func (o *oneKV) sent(req interface{ Header() http.Header }) {
	req.Header().Set(hopHeader, strconv.Itoa(o.hops))
}

func (o *oneKV) Get(ctx context.Context, key string) ([]byte, error) {
	req := connect.NewRequest(&databasev2.KVGetRequest{Ref: o.ref, Key: key})
	o.sent(req)
	out, err := o.svc.KVGet(ctx, req)
	if err != nil {
		return nil, refusal(err)
	}
	return out.Msg.GetValue(), nil
}

func (o *oneKV) Put(ctx context.Context, key string, value []byte) error {
	req := connect.NewRequest(&databasev2.KVPutRequest{Ref: o.ref, Key: key, Value: value})
	o.sent(req)
	_, err := o.svc.KVPut(ctx, req)
	return refusal(err)
}

func (o *oneKV) Delete(ctx context.Context, key string) error {
	req := connect.NewRequest(&databasev2.KVDeleteRequest{Ref: o.ref, Key: key})
	o.sent(req)
	_, err := o.svc.KVDelete(ctx, req)
	return refusal(err)
}

// List pages until the node says there are no more.
func (o *oneKV) List(ctx context.Context, prefix string) ([]string, error) {
	var out []string
	after := ""
	for {
		req := connect.NewRequest(&databasev2.KVListRequest{Ref: o.ref, Prefix: prefix, After: after})
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

// Watch is not carried over the wire; the engine refuses it too.
func (o *oneKV) Watch(context.Context, string) (<-chan string, error) {
	return nil, kvengine.ErrNoWatch
}

// Close releases nothing: the stream belongs to the Client that made this.
func (o *oneKV) Close() error { return nil }

// refusal is wire() read backwards: a status from another node becomes this
// package's sentinel, carrying the answering node's sentence. Where one code stands
// for two sentinels (NotFound), it picks the one a caller acts on.
func refusal(err error) error {
	if err == nil {
		return nil
	}

	// A gone peer, a peer without this service and a dead stream all arrive as
	// Unavailable, like a voter's own refusal; the mark the answering node set tells
	// them apart. A silence wraps ErrNotHere, so a walk over voters continues past it.
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
		// Unavailable and everything unnamed are a retry, passed through unchanged.
		return err
	}
}

// Digest is what the reached node holds for one database, and whether it means to
// keep it: the question a departing node asks its replacements before dropping.
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

func (c *Client) ref(at address) *databasev2.Ref {
	return &databasev2.Ref{
		Project: at.project, Environment: at.environment, Id: at.id, Instance: at.instance,
	}
}
