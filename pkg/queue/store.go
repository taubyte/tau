package queue

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/taubyte/tau/pkg/raft"
)

// Store is the part of a replicated key-value cluster this queue needs, and no
// more of it: a leader, a consistent read, a prefix listing, and a write of
// several keys that is one entry in the log. raft.Cluster satisfies it, and so
// does raft.MockCluster; neither is named here, because a queue does not need a
// consensus implementation, a transport, or a membership list.
//
// Batch MUST be atomic — every operation applied or none. Every state
// transition in this package is one Batch, and that is what makes an item
// impossible to lose between two places.
type Store interface {
	// Get reads a key from local committed state.
	Get(key string) ([]byte, bool)

	// Keys lists every key with the given prefix, in no particular order.
	Keys(prefix string) []string

	// Batch atomically applies the operations as a single replicated entry.
	// It returns raft.ErrNotLeader off the leader.
	Batch(ops []raft.BatchOp, timeout time.Duration) error

	// Barrier blocks until everything committed before it is applied locally.
	Barrier(timeout time.Duration) error

	// IsLeader reports whether this node may write.
	IsLeader() bool
}

// The key layout. This is the only file that builds a key.
//
//	queue/<name>/ready/<deliverable-at>/<id>   an item waiting to be delivered
//	queue/<name>/claimed/<deadline>/<id>       an item a consumer holds
//	queue/<name>/index/<id>                    -> the key above that holds it
//
// <name> and <id> are escaped, so neither can contain a '/' or forge a segment.
// The time is fixed-width decimal nanoseconds, so a lexical sort of a prefix
// listing is a chronological one: the first ready key is the next item due, and
// the claimed scan can stop at the first deadline in the future.
const (
	rootSegment    = "queue"
	readySegment   = "ready"
	claimedSegment = "claimed"
	indexSegment   = "index"

	stampWidth = 20
)

type keyspace struct {
	prefix string // "queue/<escaped name>/"
}

func newKeyspace(name string) keyspace {
	return keyspace{prefix: rootSegment + "/" + escape(name) + "/"}
}

func (k keyspace) readyPrefix() string   { return k.prefix + readySegment + "/" }
func (k keyspace) claimedPrefix() string { return k.prefix + claimedSegment + "/" }

func (k keyspace) ready(at time.Time, id string) string {
	return k.readyPrefix() + stamp(at) + "/" + escape(id)
}

func (k keyspace) claimed(deadline time.Time, id string) string {
	return k.claimedPrefix() + stamp(deadline) + "/" + escape(id)
}

func (k keyspace) index(id string) string {
	return k.prefix + indexSegment + "/" + escape(id)
}

// stateKey is a parsed ready or claimed key.
type stateKey struct {
	state string
	at    time.Time
}

// parse reads a key this queue wrote. A key under this queue's prefix that it
// did not write is a fault somewhere else, so it is reported rather than
// skipped: skipping it would leave an item that is invisible and never expires.
func (k keyspace) parse(key string) (stateKey, error) {
	rest, ok := strings.CutPrefix(key, k.prefix)
	if !ok {
		return stateKey{}, fmt.Errorf("%w: %q is not under %q", ErrForeignKey, key, k.prefix)
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 3 || (parts[0] != readySegment && parts[0] != claimedSegment) {
		return stateKey{}, fmt.Errorf("%w: %q", ErrForeignKey, key)
	}
	if len(parts[1]) != stampWidth {
		return stateKey{}, fmt.Errorf("%w: %q has a malformed timestamp", ErrForeignKey, key)
	}
	nanos, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || nanos < 0 {
		return stateKey{}, fmt.Errorf("%w: %q has a malformed timestamp", ErrForeignKey, key)
	}
	return stateKey{state: parts[0], at: time.Unix(0, nanos)}, nil
}

// stamp encodes a time as a fixed-width decimal so that lexical order is
// chronological order. Times before the epoch clamp to it; a queue whose clock
// reports 1969 has a worse problem than ordering.
func stamp(t time.Time) string {
	nanos := t.UnixNano()
	if nanos < 0 {
		nanos = 0
	}
	return fmt.Sprintf("%0*d", stampWidth, nanos)
}

const hexDigits = "0123456789ABCDEF"

// escape turns an arbitrary string into one key segment. Everything outside
// [A-Za-z0-9_-] becomes %XX, which means the result can never contain '/',
// never be "." or "..", and never be empty for a non-empty input — the three
// ways a segment can be eaten by a path cleaner. '%' escapes itself, so the
// encoding is injective and two ids can never share a key.
func escape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0x0f])
		}
	}
	return b.String()
}

func set(key string, value []byte) raft.BatchOp {
	return raft.BatchOp{Set: &raft.SetCommand{Key: key, Value: value}}
}

func del(key string) raft.BatchOp {
	return raft.BatchOp{Delete: &raft.DeleteCommand{Key: key}}
}
