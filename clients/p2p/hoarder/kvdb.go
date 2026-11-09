package hoarder

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"path"
	"strings"
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
	"golang.org/x/sync/singleflight"
)

// ErrNotFound is returned by the remote KVDB's Get when the key is absent.
var ErrNotFound = errors.New("key not found")

// ErrOverCapacity is returned when the hoarder refused a write: the database
// quota or the node's admission policy.
var ErrOverCapacity = errors.New("no space left for input")

// refused turns a typed refusal in a response into an error.
func refused(resp cr.Response) error {
	if maps.TryString(resp, hoarderSpecs.BodyCode) == hoarderSpecs.CodeOverCapacity {
		return ErrOverCapacity
	}
	return nil
}

// KVDB returns a remote-backed kvdb.KVDB for a database/storage instance. Every
// operation is a p2p call to a hoarder holding the instance; the handle resolves
// replicas lazily and sticks to one with failover for writes and for the reads
// that must see them. Other database reads are spread: see readTarget.
func (c *Client) KVDB(kind hoarderIface.ResourceKind, project, application, match, branch string) (coreKvdb.KVDB, error) {
	return &remoteKV{
		client:      c,
		kind:        kind,
		project:     project,
		application: application,
		match:       match,
		branch:      branch,
		resolve: func() ([]peerCore.ID, error) {
			return c.ReplicasOf(kind, project, application, match)
		},
		send: c.Send,
	}, nil
}

type remoteKV struct {
	client      *Client
	kind        hoarderIface.ResourceKind
	project     string
	application string
	match       string
	branch      string

	resolve func() ([]peerCore.ID, error) // replica lookup; a field so tests can stall it
	send    func(cmd string, body command.Body, peers ...peerCore.ID) (cr.Response, error)

	lookup   singleflight.Group
	mu       sync.Mutex
	replicas []peerCore.ID
	sticky   int

	// readers is apart from replicas so that read routing never moves the
	// sticky replica the writes go to.
	readers   []peerCore.ID
	readersAt time.Time
	written   map[string]time.Time // key -> when this handle last wrote it
	sweptAt   time.Time
}

var _ coreKvdb.KVDB = (*remoteKV)(nil)
var _ hoarderIface.NxKVDB = (*remoteKV)(nil)
var _ hoarderIface.QuotaKVDB = (*remoteKV)(nil)

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
// which first-touches the resource. The lookup is network I/O, so it runs
// outside r.mu and concurrent callers share one lookup; each caller waits only
// until its own ctx ends.
func (r *remoteKV) target(ctx context.Context) (peerCore.ID, error) {
	if pid, ok := r.current(); ok {
		return pid, nil
	}
	ch := r.lookup.DoChan("", func() (interface{}, error) {
		peers, err := r.resolve()
		if err == nil && len(peers) > 0 {
			r.mu.Lock()
			if len(r.replicas) == 0 {
				r.replicas = peers
				r.sticky = 0
			}
			r.mu.Unlock()
		}
		return nil, err
	})
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return "", res.Err
		}
	}
	pid, _ := r.current()
	return pid, nil
}

// current returns the sticky replica if the set is known.
func (r *remoteKV) current() (peerCore.ID, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.replicas) == 0 {
		return "", false
	}
	return r.replicas[r.sticky%len(r.replicas)], true
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

// OpTimeout bounds a kvdb op whose caller gave no deadline, so a stuck
// replica fails the op instead of holding the caller forever.
var OpTimeout = 30 * time.Second

func opContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, OpTimeout)
}

func (r *remoteKV) opBody(extra command.Body) command.Body {
	body := r.instanceBody()
	for k, v := range extra {
		body[k] = v
	}
	return body
}

// do sends a kvdb op to the sticky replica, retrying across mesh warmup and
// failing over to other replicas on a transport error and following a
// not-replica redirect. It honors ctx: a caller that gave up neither sleeps
// through the cold-start backoff nor starts another round.
func (r *remoteKV) do(ctx context.Context, extra command.Body) (cr.Response, error) {
	ctx, cancel := opContext(ctx)
	defer cancel()
	body := r.opBody(extra)

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
		pid, err := r.target(ctx)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if err != nil {
			// No hoarder reachable yet — fall back to "any hoarder" so an unplaced
			// resource can still first-touch once discovery completes.
			pid = ""
		}

		var resp cr.Response
		if pid == "" {
			resp, err = r.send(hoarderSpecs.KVDBCommand, body)
		} else {
			resp, err = r.send(hoarderSpecs.KVDBCommand, body, pid)
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

// LocalHandler answers a kvdb command inside this process.
type LocalHandler func(ctx context.Context, body command.Body) (cr.Response, error)

// localHoarders holds the hoarders running in this process, by peer id. Substrate
// dials through its client node, whose id differs from the hoarder's, so the
// replica list alone cannot say which replica is on this machine.
var localHoarders sync.Map // peerCore.ID -> LocalHandler

// ServeLocal lets clients in this process reach the hoarder id without a
// network hop. The returned func withdraws it.
func ServeLocal(id peerCore.ID, h LocalHandler) (stop func()) {
	localHoarders.Store(id, h)
	return func() { localHoarders.Delete(id) }
}

const (
	// readYourWritesWindow is how long a handle's reads of a key it wrote stay on
	// the sticky replica. Minutes, not a few rebroadcasts: a replica behind a DAG
	// backlog can lag far longer than the gossip interval. After it, and for keys
	// written elsewhere, a read can be stale for as long as its replica lags.
	readYourWritesWindow = 5 * time.Minute
	readersRefresh       = 30 * time.Second
)

func canonicalKey(key string) string { return path.Clean("/" + key) }

// wrote starts the read-your-writes window of keys. Callers run it whether or
// not the write reported success, since a timed-out write may still have landed.
func (r *remoteKV) wrote(keys ...string) {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.written == nil {
		r.written = make(map[string]time.Time, len(keys))
	}
	if now.Sub(r.sweptAt) > readYourWritesWindow {
		r.sweptAt = now
		for k, at := range r.written {
			if now.Sub(at) >= readYourWritesWindow {
				delete(r.written, k)
			}
		}
	}
	for _, k := range keys {
		r.written[canonicalKey(k)] = now
	}
}

func (r *remoteKV) refreshReaders() {
	peers, err := r.resolve()
	if err != nil || len(peers) == 0 {
		return
	}
	r.mu.Lock()
	r.readers = peers
	r.mu.Unlock()
}

// readTarget picks the replica a Get may use instead of the sticky one: the
// hoarder in this process when it is a replica, otherwise a random replica. It
// reports false when the Get must stay on the sticky path: storage metadata
// (a stale version would be reused), quota inputs, keys this handle just wrote,
// or no replica known yet. The lookup runs in the background so a hung
// ReplicasOf never delays a read.
func (r *remoteKV) readTarget(key string) (peerCore.ID, bool) {
	k := canonicalKey(key)
	if r.kind == hoarderIface.Storage || strings.HasPrefix(k, "/size/") {
		return "", false
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if now.Sub(r.written[k]) < readYourWritesWindow {
		return "", false
	}
	if now.Sub(r.readersAt) > readersRefresh {
		r.readersAt = now
		go r.refreshReaders()
	}
	if len(r.readers) == 0 {
		return "", false
	}
	candidates := r.readers
	var local []peerCore.ID
	for _, pid := range r.readers {
		if _, ok := localHoarders.Load(pid); ok {
			local = append(local, pid)
		}
	}
	if len(local) > 0 {
		candidates = local
	}
	return candidates[rand.IntN(len(candidates))], true
}

func (r *remoteKV) dropReader(pid peerCore.ID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := make([]peerCore.ID, 0, len(r.readers))
	for _, p := range r.readers {
		if p != pid {
			kept = append(kept, p)
		}
	}
	r.readers = kept
}

// readReplica tries a Get on a spread replica. A nil response sends the Get down
// the sticky path, which keeps every failover, redirect and first-touch rule. A
// miss goes there too, as a replica that lacks the key may simply be behind.
func (r *remoteKV) readReplica(ctx context.Context, key string, extra command.Body) cr.Response {
	pid, ok := r.readTarget(key)
	if !ok {
		return nil
	}
	ctx, cancel := opContext(ctx)
	defer cancel()

	var resp cr.Response
	var err error
	if h, local := localHoarders.Load(pid); local {
		resp, err = h.(LocalHandler)(ctx, r.opBody(extra))
	} else {
		resp, err = r.send(hoarderSpecs.KVDBCommand, r.opBody(extra), pid)
	}
	if err != nil {
		if ctx.Err() == nil {
			r.dropReader(pid) // back at the next refresh if it is still a live claimant
		}
		return nil
	}
	switch maps.TryString(resp, hoarderSpecs.BodyCode) {
	case "":
		return resp
	case hoarderSpecs.CodeNotReplica:
		r.dropReader(pid) // a local replica is otherwise asked first on every read
	}
	return nil
}

func (r *remoteKV) Get(ctx context.Context, key string) ([]byte, error) {
	op := command.Body{hoarderSpecs.BodyKVOp: hoarderSpecs.KVGet, hoarderSpecs.BodyKey: key}
	resp := r.readReplica(ctx, key, op)
	if resp == nil {
		var err error
		if resp, err = r.do(ctx, op); err != nil {
			return nil, err
		}
	}
	if maps.TryString(resp, hoarderSpecs.BodyCode) == hoarderSpecs.CodeNotFound {
		return nil, ErrNotFound
	}
	return maps.ByteArray(resp, hoarderSpecs.BodyValue)
}

func (r *remoteKV) Put(ctx context.Context, key string, v []byte) error {
	defer r.wrote(key)
	resp, err := r.do(ctx, command.Body{hoarderSpecs.BodyKVOp: hoarderSpecs.KVPut, hoarderSpecs.BodyKey: key, hoarderSpecs.BodyValue: v})
	if err != nil {
		return err
	}
	return refused(resp)
}

// PutNx writes the key only if absent on the serving replica, atomically
// against concurrent writes there. Returns existed=true when the key was
// already present (nothing written).
func (r *remoteKV) PutNx(ctx context.Context, key string, v []byte) (bool, error) {
	defer r.wrote(key)
	resp, err := r.do(ctx, command.Body{hoarderSpecs.BodyKVOp: hoarderSpecs.KVPutNx, hoarderSpecs.BodyKey: key, hoarderSpecs.BodyValue: v})
	if err != nil {
		return false, err
	}
	if err = refused(resp); err != nil {
		return false, err
	}
	existed, _ := maps.Bool(resp, hoarderSpecs.BodyExisted)
	return existed, nil
}

func (r *remoteKV) Delete(ctx context.Context, key string) error {
	defer r.wrote(key)
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
	return &remoteBatch{ctx: ctx, kv: r}, nil
}

// BatchWithMax is Batch with the database quota sent along, enforced by the
// hoarder from its local data. A hoarder that predates the field ignores it,
// so during a rolling upgrade the quota is not enforced.
func (r *remoteKV) BatchWithMax(ctx context.Context, max uint64) (coreKvdb.Batch, error) {
	return &remoteBatch{ctx: ctx, kv: r, max: max}, nil
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
	ctx context.Context // from Batch(ctx): Commit takes no ctx
	kv  *remoteKV
	ops []command.Body
	max uint64 // database quota in bytes; 0 = not sent
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
	keys := make([]string, len(b.ops))
	for i, o := range b.ops {
		ops[i] = map[string]interface{}(o)
		keys[i] = maps.TryString(o, hoarderSpecs.BodyKey)
	}
	defer b.kv.wrote(keys...)
	body := command.Body{hoarderSpecs.BodyKVOp: hoarderSpecs.KVBatch, hoarderSpecs.BodyOps: ops}
	if b.max > 0 {
		body[hoarderSpecs.BodyMaxSize] = b.max
	}
	resp, err := b.kv.do(b.ctx, body)
	if err != nil {
		return err
	}
	return refused(resp)
}
