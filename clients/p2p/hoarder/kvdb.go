package hoarder

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	coreKvdb "github.com/taubyte/tau/core/kvdb"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	"github.com/taubyte/tau/pkg/kvdb"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
	"github.com/taubyte/tau/utils/maps"
)

// ErrNotFound is returned by the remote KVDB's Get when the key is absent.
var ErrNotFound = errors.New("key not found")

// KVDB returns a remote-backed kvdb.KVDB for a database/storage instance. Every
// operation is a p2p call to a hoarder holding the instance; the handle resolves
// replicas lazily and sticks to one (read-your-writes) with failover.
func (c *Client) KVDB(kind hoarderIface.ResourceKind, project, application, match, branch string) (coreKvdb.KVDB, error) {
	return &remoteKV{
		client:      c,
		kind:        kind,
		project:     project,
		application: application,
		match:       match,
		branch:      branch,
	}, nil
}

type remoteKV struct {
	client      *Client
	kind        hoarderIface.ResourceKind
	project     string
	application string
	match       string
	branch      string

	mu       sync.Mutex
	replicas []peerCore.ID
	sticky   int
}

var _ coreKvdb.KVDB = (*remoteKV)(nil)

func (r *remoteKV) instanceBody() command.Body {
	return command.Body{
		hoarderSpecs.BodyKind:    int(r.kind),
		hoarderSpecs.BodyProject: r.project,
		hoarderSpecs.BodyApp:     r.application,
		hoarderSpecs.BodyMatch:   r.match,
		hoarderSpecs.BodyBranch:  r.branch,
	}
}

// target returns the current sticky replica, resolving the set on first use.
// An empty return means no replicas are known yet — the op goes to any hoarder,
// which first-touches the resource.
func (r *remoteKV) target() (peerCore.ID, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.replicas) == 0 {
		peers, err := r.client.ReplicasOf(r.kind, r.project, r.application, r.match)
		if err != nil {
			return "", err
		}
		r.replicas = peers
		r.sticky = 0
	}
	if len(r.replicas) == 0 {
		return "", nil
	}
	return r.replicas[r.sticky%len(r.replicas)], nil
}

func (r *remoteKV) failover() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sticky++
	return r.sticky < len(r.replicas)
}

// replicaCount reads len(r.replicas) under the lock so callers — notably the
// attempt() retry bound — don't race the concurrent mutators (reset,
// adoptRedirect, pinServer) that replace the slice on a shared handle.
func (r *remoteKV) replicaCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.replicas)
}

func (r *remoteKV) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.replicas = nil
	r.sticky = 0
}

func (r *remoteKV) adoptRedirect(resp cr.Response) {
	ids, _ := maps.StringArray(resp, hoarderSpecs.BodyPeers)
	peers := make([]peerCore.ID, 0, len(ids))
	for _, id := range ids {
		if pid, err := peerCore.Decode(id); err == nil {
			peers = append(peers, pid)
		}
	}
	r.mu.Lock()
	r.replicas = peers
	r.sticky = 0
	r.mu.Unlock()
}

// Cold-start resilience: a freshly-meshed substrate may need a few seconds
// before the streams client discovers a hoarder speaking the protocol, so the
// first op after boot can transiently find no peer. Retry the whole op with
// backoff before giving up rather than failing the user's first request.
const (
	coldStartRetries = 4
	coldStartBackoff = 1500 * time.Millisecond
)

// do sends a kvdb op to the sticky replica, retrying across mesh warmup and
// failing over to other replicas on a transport error and following a
// not-replica redirect. It honors ctx: a caller that gave up neither sleeps
// through the cold-start backoff nor starts another round.
func (r *remoteKV) do(ctx context.Context, extra command.Body) (cr.Response, error) {
	body := r.instanceBody()
	for k, v := range extra {
		body[k] = v
	}

	var lastErr error
	for round := 0; round < coldStartRetries; round++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if round > 0 {
			r.reset() // re-resolve replicas after a warmup miss
			// Cancellable backoff: don't sleep past a caller that gave up.
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(coldStartBackoff):
			}
		}
		resp, err := r.attempt(ctx, body)
		if err == nil {
			return resp, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// attempt makes one pass: resolve the sticky replica, send, failing over across
// replicas and following a not-replica redirect, re-resolving once.
func (r *remoteKV) attempt(ctx context.Context, body command.Body) (cr.Response, error) {
	// Bounded attempts: one full pass over the current replica set + 2 for the
	// re-resolve/redirect. The bound is re-read through replicaCount() each
	// iteration under r.mu: a raw len(r.replicas) here races the mutators
	// (reset/adoptRedirect/pinServer) since a remoteKV handle is shared across
	// goroutines, and re-reading lets a mid-loop redirect that grows the set
	// still get a full pass over the larger set.
	for i := 0; i < r.replicaCount()+2; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := r.target()
		if err != nil {
			// No hoarder reachable yet — fall back to "any hoarder" so an unplaced
			// resource can still first-touch once discovery completes.
			pid = ""
		}

		var resp cr.Response
		if pid == "" {
			resp, err = r.client.Send(hoarderSpecs.KVDBCommand, body)
		} else {
			resp, err = r.client.Send(hoarderSpecs.KVDBCommand, body, pid)
		}
		if err != nil {
			if r.failover() {
				continue
			}
			return nil, err
		}

		if code := maps.TryString(resp, hoarderSpecs.BodyCode); code == hoarderSpecs.CodeNotReplica {
			r.adoptRedirect(resp)
			continue
		}
		// Pin to whichever hoarder served this — especially after a first-touch,
		// where the request went to "any" peer — so later ops read our writes.
		r.pinServer(maps.TryString(resp, hoarderSpecs.BodyServedBy))
		return resp, nil
	}
	return nil, fmt.Errorf("kvdb op exhausted replicas for %s/%s", r.project, r.match)
}

// pinServer sticks to the peer that served the last request when we had no
// replica set yet (first-touch), giving read-your-writes on subsequent ops.
func (r *remoteKV) pinServer(id string) {
	if id == "" {
		return
	}
	pid, err := peerCore.Decode(id)
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.replicas) == 0 {
		r.replicas = []peerCore.ID{pid}
		r.sticky = 0
	}
}

func (r *remoteKV) Get(ctx context.Context, key string) ([]byte, error) {
	resp, err := r.do(ctx, command.Body{hoarderSpecs.BodyKVOp: hoarderSpecs.KVGet, hoarderSpecs.BodyKey: key})
	if err != nil {
		return nil, err
	}
	if maps.TryString(resp, hoarderSpecs.BodyCode) == hoarderSpecs.CodeNotFound {
		return nil, ErrNotFound
	}
	return maps.ByteArray(resp, hoarderSpecs.BodyValue)
}

func (r *remoteKV) Put(ctx context.Context, key string, v []byte) error {
	_, err := r.do(ctx, command.Body{hoarderSpecs.BodyKVOp: hoarderSpecs.KVPut, hoarderSpecs.BodyKey: key, hoarderSpecs.BodyValue: v})
	return err
}

func (r *remoteKV) Delete(ctx context.Context, key string) error {
	_, err := r.do(ctx, command.Body{hoarderSpecs.BodyKVOp: hoarderSpecs.KVDelete, hoarderSpecs.BodyKey: key})
	return err
}

func (r *remoteKV) List(ctx context.Context, prefix string) ([]string, error) {
	resp, err := r.do(ctx, command.Body{hoarderSpecs.BodyKVOp: hoarderSpecs.KVList, hoarderSpecs.BodyPrefix: prefix})
	if err != nil {
		return nil, err
	}
	return maps.StringArray(resp, hoarderSpecs.BodyKeys)
}

func (r *remoteKV) ListRegEx(ctx context.Context, prefix string, regexs ...string) ([]string, error) {
	resp, err := r.do(ctx, command.Body{
		hoarderSpecs.BodyKVOp:   hoarderSpecs.KVListRegex,
		hoarderSpecs.BodyPrefix: prefix,
		hoarderSpecs.BodyRegexs: regexs,
	})
	if err != nil {
		return nil, err
	}
	return maps.StringArray(resp, hoarderSpecs.BodyKeys)
}

// ListAsync/ListRegExAsync feed the (already-paginated) result through a channel
// — the SDK surface is unchanged even though the wire call is synchronous.
func (r *remoteKV) ListAsync(ctx context.Context, prefix string) (chan string, error) {
	keys, err := r.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	return streamKeys(ctx, keys), nil
}

func (r *remoteKV) ListRegExAsync(ctx context.Context, prefix string, regexs ...string) (chan string, error) {
	keys, err := r.ListRegEx(ctx, prefix, regexs...)
	if err != nil {
		return nil, err
	}
	return streamKeys(ctx, keys), nil
}

func streamKeys(ctx context.Context, keys []string) chan string {
	ch := make(chan string, len(keys))
	go func() {
		defer close(ch)
		for _, k := range keys {
			select {
			case <-ctx.Done():
				return
			case ch <- k:
			}
		}
	}()
	return ch
}

func (r *remoteKV) Sync(ctx context.Context, key string) error {
	_, err := r.do(ctx, command.Body{hoarderSpecs.BodyKVOp: hoarderSpecs.KVSync, hoarderSpecs.BodyKey: key})
	return err
}

func (r *remoteKV) Batch(ctx context.Context) (coreKvdb.Batch, error) {
	return &remoteBatch{kv: r}, nil
}

// Stats over the wire is not yet mapped; return an empty CRDT stats so callers
// that only inspect the type don't panic.
func (r *remoteKV) Stats(ctx context.Context) coreKvdb.Stats {
	return kvdb.NewStats()
}

// Factory is nil: remote handles are not owned by a local factory.
func (r *remoteKV) Factory() coreKvdb.Factory { return nil }

// Close is client-local; the underlying stream client is shared and long-lived.
func (r *remoteKV) Close() {}

// remoteBatch buffers ops and applies them as one grouped batch on Commit.
type remoteBatch struct {
	kv  *remoteKV
	ops []command.Body
}

func (b *remoteBatch) Put(key string, value []byte) error {
	b.ops = append(b.ops, command.Body{
		hoarderSpecs.BodyKVOp:  hoarderSpecs.KVPut,
		hoarderSpecs.BodyKey:   key,
		hoarderSpecs.BodyValue: value,
	})
	return nil
}

func (b *remoteBatch) Delete(key string) error {
	b.ops = append(b.ops, command.Body{
		hoarderSpecs.BodyKVOp: hoarderSpecs.KVDelete,
		hoarderSpecs.BodyKey:  key,
	})
	return nil
}

func (b *remoteBatch) Commit() error {
	if len(b.ops) == 0 {
		return nil
	}
	ops := make([]interface{}, len(b.ops))
	for i, o := range b.ops {
		ops[i] = map[string]interface{}(o)
	}
	// The Batch interface's Commit carries no ctx; the op is not caller-cancellable.
	_, err := b.kv.do(context.Background(), command.Body{hoarderSpecs.BodyKVOp: hoarderSpecs.KVBatch, hoarderSpecs.BodyOps: ops})
	return err
}
