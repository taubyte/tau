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

// A dot names one delta: its incarnation and that incarnation's counter, from
// 1. An incarnation is the node's peer id and a random 128-bit nonce that is
// never persisted, so a store restored from a backup draws a new one; within
// one process a closing store hands its incarnation to the next open (see
// incarnationRegistry).
type dot struct {
	replica string
	counter uint64
}

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
// continue its incarnation, so a load/unload cycle does not grow every vector.
// An entry is the true last counter, written under the store's lock after its
// last write, and resuming takes it out, so no two open stores share an
// incarnation. A store whose context does not reach that counter draws a new
// one.
type incarnationRegistry struct {
	mu     sync.Mutex
	closed map[incarnationKey]incarnation
}

func newIncarnationRegistry() *incarnationRegistry {
	return &incarnationRegistry{closed: map[incarnationKey]incarnation{}}
}

// resume takes the incarnation closed under k when reached gives its last
// counter.
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

const segAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

// 0xff marks a byte outside segAlphabet.
var segValues = func() (t [256]byte) {
	for i := range t {
		t[i] = 0xff
	}
	for i := range len(segAlphabet) {
		t[segAlphabet[i]] = byte(i)
	}
	return
}()

var errSegment = errors.New("not a segment")

// appendSegDecode is segEncoding.AppendDecode without the copy of src it makes,
// and it refuses a trailing partial byte AppendDecode drops.
func appendSegDecode(b, src []byte) ([]byte, error) {
	var acc uint64
	bits := 0
	for _, ch := range src {
		v := segValues[ch]
		if v == 0xff {
			return b, errSegment
		}
		acc = acc<<5 | uint64(v)
		if bits += 5; bits >= 8 {
			bits -= 8
			b = append(b, byte(acc>>bits))
		}
	}
	if bits >= 5 {
		return b, errSegment
	}
	return b, nil
}

// The counter is zero-padded to 20 digits so byte order is numeric order.
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

// replicas interns the replicas dot segments name, so parsing a key does not
// make a string per key. It empties at internedLimit.
var replicas = &internTable{m: map[string]string{}, raw: map[string]string{}}

const internedLimit = 1 << 12

type internTable struct {
	mu     sync.RWMutex
	m, raw map[string]string
}

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

// One allocation: DecodeString's buffer is held by nothing else, and the
// string is made over it.
func decodeSegment(enc string) (string, error) {
	b, err := segEncoding.DecodeString(enc)
	if err != nil || len(b) == 0 {
		return "", err
	}
	return unsafe.String(&b[0], len(b)), nil
}

// keySegment encodes a key as one segment, so the markers of "/a" are never
// under the prefix of those of "/a/b".
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

// The replica is borrowed until releaseDelta clears it.
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

// A varint counter, then the replica.
func encodeDot(d dot) []byte { return appendDot(nil, d) }

func appendDot(b []byte, d dot) []byte {
	return append(binary.AppendUvarint(b, d.counter), d.replica...)
}

func decodeDot(b []byte) (dot, error) {
	c, n := binary.Uvarint(b)
	if n <= 0 {
		return dot{}, errors.New("error decoding dot")
	}
	return dot{counter: c, replica: replicas.bytes(b[n:])}, nil
}
