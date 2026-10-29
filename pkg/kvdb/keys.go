package kvdb

import (
	"encoding/binary"
	"strconv"
	"unsafe"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
)

// A key is built in one allocation: its prefix is computed once per store,
// and each segment is encoded straight into the key's buffer, in the form
// keySegment, dot.segment and dshelp's block keys give.

// prefixes are the store's key prefixes, each without its trailing slash.
type prefixes struct {
	blocks, missing, owned, reclaim, vector, cloud, redo string
}

func newPrefixes(ns ds.Key) prefixes {
	return prefixes{
		blocks:  ns.ChildString(processedBlocksNs).String(),
		missing: ns.ChildString(missingNs).String(),
		owned:   ns.ChildString(ownedNs).String(),
		reclaim: ns.ChildString(reclaimNs).String(),
		vector:  ns.ChildString(contextNs).ChildString("v").String(),
		cloud:   ns.ChildString(contextNs).ChildString("d").String(),
		redo:    ns.ChildString(redoNs).String(),
	}
}

// dsKey is the key b spells. b must not change after.
func dsKey(b []byte) ds.Key {
	return ds.RawKey(unsafe.String(unsafe.SliceData(b), len(b)))
}

func bytesOf(s string) []byte {
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

func keySegmentLen(key string) int { return 1 + segEncoding.EncodedLen(len(key)) }

func appendKeySegment(b []byte, key string) []byte {
	return segEncoding.AppendEncode(append(b, 'k'), bytesOf(key))
}

func replicaSegmentLen(replica string) int { return 1 + segEncoding.EncodedLen(len(replica)) }

func appendReplicaSegment(b []byte, replica string) []byte {
	return segEncoding.AppendEncode(append(b, 'r'), bytesOf(replica))
}

const counterDigits = 20

func dotSegmentLen(d dot) int { return replicaSegmentLen(d.replica) + 1 + counterDigits }

func appendDotSegment(b []byte, d dot) []byte {
	return appendCounter(append(appendReplicaSegment(b, d.replica), '.'), d.counter)
}

// appendCounter appends n zero-padded to counterDigits, so byte order is
// numeric order.
func appendCounter(b []byte, n uint64) []byte {
	var digits [counterDigits]byte
	s := strconv.AppendUint(digits[:0], n, 10)
	for range counterDigits - len(s) {
		b = append(b, '0')
	}
	return append(b, s...)
}

// hashOf is the multihash of c, without copying it.
func hashOf(c cid.Cid) []byte {
	b := bytesOf(c.KeyString())
	if len(b) == 0 || c.Version() == 0 {
		return b
	}
	_, n := binary.Uvarint(b)
	_, m := binary.Uvarint(b[n:])
	return b[n+m:]
}

// hashKey is prefix/<the multihash of c, as dshelp encodes it>.
func hashKey(prefix string, c cid.Cid) ds.Key {
	h := hashOf(c)
	if len(h) == 0 {
		return ds.RawKey(prefix)
	}
	b := make([]byte, 0, len(prefix)+1+segEncoding.EncodedLen(len(h)))
	return dsKey(segEncoding.AppendEncode(append(append(b, prefix...), '/'), h))
}

// dotKey is prefix/<the segment of d>.
func dotKey(prefix string, d dot) ds.Key {
	b := make([]byte, 0, len(prefix)+1+dotSegmentLen(d))
	return dsKey(appendDotSegment(append(append(b, prefix...), '/'), d))
}
