package kvdb

import (
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	pb "github.com/taubyte/tau/pkg/kvdb/pb"
)

// A dot names one delta: the incarnation that wrote it and that incarnation's
// counter for it. Counters start at 1, so the zero dot names nothing.
//
// An incarnation is the node's peer id followed by a 128-bit nonce drawn at
// random by the process that writes under it. The nonce is never persisted. A
// store restored from a backup is byte-identical to the store it was copied
// from, so nothing on disk could tell the two apart; because a nonce lives
// only in the memory of the process that drew it, a restored store reuses a
// dot only if two random 128-bit draws collide. Within one process, a store
// that closes hands its incarnation to the next open of the same store (see
// incarnationRegistry).
type dot struct {
	replica string
	counter uint64
}

// incarnations is this process's registry of the incarnations closed stores
// wrote under.
var incarnations = newIncarnationRegistry()

type incarnationKey struct {
	replicaID string
	namespace string
}

type incarnation struct {
	replica string
	counter uint64
}

// incarnationRegistry lets a store reopened in the process that closed it
// continue the incarnation it closed, so a load/unload cycle does not grow
// every replica's vector by one entry.
//
// It is safe because an entry is the incarnation's true last counter, written
// under the store's lock as it closes, after its last write: a closed store
// writes nothing more. And resuming takes the entry out, so no two open
// stores ever write under one incarnation. A store whose context does not
// reach that counter draws a new one instead: it is a copy of the store taken
// earlier, or one emptied since, and would chain its own writes past a gap in
// its own context.
type incarnationRegistry struct {
	mu     sync.Mutex
	closed map[incarnationKey]incarnation
}

func newIncarnationRegistry() *incarnationRegistry {
	return &incarnationRegistry{closed: map[incarnationKey]incarnation{}}
}

// resume takes the incarnation closed under k if reached, the opening store's
// covered counter for it, is its last counter.
func (r *incarnationRegistry) resume(k incarnationKey, reached func(replica string) uint64) (incarnation, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	inc, ok := r.closed[k]
	if !ok || reached(inc.replica) != inc.counter {
		return incarnation{}, false
	}
	delete(r.closed, k)
	return inc, true
}

func (r *incarnationRegistry) release(k incarnationKey, inc incarnation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed[k] = inc
}

func (d dot) isZero() bool { return d.counter == 0 }

func (d dot) String() string {
	return fmt.Sprintf("%x:%d", d.replica, d.counter)
}

var segEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// segment encodes the dot as one datastore key segment that sorts by replica
// and then by counter: the counter is zero-padded to 20 digits so byte order
// is numeric order.
func (d dot) segment() string {
	return replicaSegment(d.replica) + "." + fmt.Sprintf("%020d", d.counter)
}

func replicaSegment(replica string) string {
	return "r" + segEncoding.EncodeToString([]byte(replica))
}

func parseDotSegment(s string) (dot, error) {
	r, c, ok := strings.Cut(s, ".")
	if !ok || !strings.HasPrefix(r, "r") {
		return dot{}, fmt.Errorf("bad dot segment %q", s)
	}
	replica, err := replicas.of(r[1:])
	if err != nil {
		return dot{}, fmt.Errorf("bad dot segment %q: %w", s, err)
	}
	counter, err := strconv.ParseUint(c, 10, 64)
	if err != nil || len(c) != 20 {
		return dot{}, fmt.Errorf("bad dot segment %q", s)
	}
	return dot{replica: replica, counter: counter}, nil
}

// replicas interns the replicas dot segments name. A store reads few
// incarnations and many keys naming each, and parsing one would otherwise
// make a string per key read. The table empties itself at internedLimit, so
// a store that sees many incarnations over its life does not keep them all.
var replicas = &internTable{m: map[string]string{}, raw: map[string]string{}}

const internedLimit = 1 << 12

// An internTable maps a replica's segment, and the replica itself, to one
// string of it.
type internTable struct {
	mu     sync.RWMutex
	m, raw map[string]string
}

// bytes is the replica b holds.
func (t *internTable) bytes(b []byte) string {
	t.mu.RLock()
	r, ok := t.raw[string(b)]
	t.mu.RUnlock()
	if ok {
		return r
	}
	r = string(b)
	t.mu.Lock()
	if len(t.raw) >= internedLimit {
		clear(t.raw)
	}
	t.raw[r] = r
	t.mu.Unlock()
	return r
}

// of is the replica the base32 segment enc spells.
func (t *internTable) of(enc string) (string, error) {
	t.mu.RLock()
	r, ok := t.m[enc]
	t.mu.RUnlock()
	if ok {
		return r, nil
	}
	r, err := decodeSegment(enc)
	if err != nil {
		return "", err
	}
	t.mu.Lock()
	if len(t.m) >= internedLimit {
		clear(t.m)
	}
	t.m[strings.Clone(enc)] = r
	t.mu.Unlock()
	return r, nil
}

// decodeSegment is the string the base32 segment enc spells, made in one
// allocation: DecodeString decodes into a buffer of its own, which nothing
// else holds, and the string is made over it.
func decodeSegment(enc string) (string, error) {
	b, err := segEncoding.DecodeString(enc)
	if err != nil || len(b) == 0 {
		return "", err
	}
	return unsafe.String(&b[0], len(b)), nil
}

// keySegment encodes a user key as one segment, so that the markers of "/a"
// are never under the prefix of the markers of "/a/b".
func keySegment(key string) string {
	return "k" + segEncoding.EncodeToString([]byte(key))
}

func parseKeySegment(s string) (string, error) {
	if !strings.HasPrefix(s, "k") {
		return "", fmt.Errorf("bad key segment %q", s)
	}
	k, err := decodeSegment(s[1:])
	if err != nil {
		return "", fmt.Errorf("bad key segment %q: %w", s, err)
	}
	return k, nil
}

func (d dot) toPB() *pb.Dot {
	if d.isZero() {
		return nil
	}
	return &pb.Dot{Replica: []byte(d.replica), Counter: d.counter}
}

// pooledPB is d as a pooled message for a delta this replica writes, its
// replica borrowed until releaseDelta clears it.
func (d dot) pooledPB() *pb.Dot {
	if d.isZero() {
		return nil
	}
	p := pb.DotFromVTPool()
	p.Replica, p.Counter = bytesOf(d.replica), d.counter
	return p
}

func dotFromPB(p *pb.Dot) dot {
	return dot{replica: replicas.bytes(p.GetReplica()), counter: p.GetCounter()}
}

// encodeDot is the value form of a dot: varint counter, then the replica.
func encodeDot(d dot) []byte {
	buf := binary.AppendUvarint(nil, d.counter)
	return append(buf, d.replica...)
}

func decodeDot(b []byte) (dot, error) {
	c, n := binary.Uvarint(b)
	if n <= 0 {
		return dot{}, errors.New("error decoding dot")
	}
	return dot{counter: c, replica: replicas.bytes(b[n:])}, nil
}
