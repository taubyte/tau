package database

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/p2p/rpc"
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
