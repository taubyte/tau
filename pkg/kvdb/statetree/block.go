package statetree

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"unsafe"

	blocks "github.com/ipfs/go-block-format"
	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
	mh "github.com/multiformats/go-multihash"
	pb "github.com/taubyte/tau/pkg/kvdb/statetree/pb"
)

// A Block is a pb.Block of its links then its data, named by the CIDv1 raw
// sha2-256 of its bytes.
type Block struct {
	c    cid.Cid
	raw  []byte
	data []byte
}

var _ ipld.Node = (*Block)(nil)

var ErrNotABlock = errors.New("statetree: not a block")

// NewBlock keeps neither data nor links.
func NewBlock(data []byte, links []cid.Cid) (*Block, error) {
	msg := pb.BlockFromVTPool()
	defer func() {
		clear(msg.Links)
		msg.ReturnToVTPool()
	}()
	msg.Links = slices.Grow(msg.Links[:0], len(links))[:len(links)]
	for i, l := range links {
		msg.Links[i] = cidBytes(l)
	}
	msg.Data = data
	raw := BlockBuffer(msg.SizeVT())
	if _, err := msg.MarshalToSizedBufferVT(raw); err != nil {
		return nil, err
	}
	raw, c := sealBlock(raw)
	// Data is the last field, so the last bytes.
	return &Block{c: c, raw: raw, data: raw[len(raw)-len(data):]}, nil
}

// CIDv1, raw, sha2-256 of 32 bytes.
const BlockPrefix = "\x01\x55\x12\x20"

// BlockBuffer leaves room after size bytes for the cid BlockInBuffer writes.
func BlockBuffer(size int) []byte {
	return make([]byte, size, size+len(BlockPrefix)+sha256.Size)
}

// raw must come from BlockBuffer.
func BlockInBuffer(raw []byte) (*Block, error) {
	if cap(raw)-len(raw) < len(BlockPrefix)+sha256.Size {
		return BlockOf(raw)
	}
	raw, c := sealBlock(raw)
	return ParseBlock(c, raw)
}

// sealBlock writes raw's cid past its end and clips raw so no append reaches
// it.
func sealBlock(raw []byte) ([]byte, cid.Cid) {
	return raw[:len(raw):len(raw)], CastCid(appendBlockCid(raw[len(raw):], raw))
}

func BlockOf(raw []byte) (*Block, error) {
	c, err := blockCid(raw)
	if err != nil {
		return nil, err
	}
	return ParseBlock(c, raw)
}

func blockCid(raw []byte) (cid.Cid, error) {
	return CastCid(appendBlockCid(make([]byte, 0, len(BlockPrefix)+sha256.Size), raw)), nil
}

func appendBlockCid(b, raw []byte) []byte {
	sum := sha256.Sum256(raw)
	return append(append(b, BlockPrefix...), sum[:]...)
}

func IsBlockCid(c cid.Cid) bool {
	k := c.KeyString()
	return len(k) == len(BlockPrefix)+sha256.Size && k[:len(BlockPrefix)] == BlockPrefix
}

// ParseBlock does not check that c is raw's cid.
func ParseBlock(c cid.Cid, raw []byte) (*Block, error) {
	b := &Block{c: c, raw: raw}
	data, err := BlockData(b)
	if err != nil {
		return nil, err
	}
	b.data = data
	return b, nil
}

// BlockData aliases nd's bytes.
func BlockData(nd blocks.Block) ([]byte, error) {
	if b, ok := nd.(*Block); ok && b.data != nil {
		return b.data, nil
	}
	msg := pb.BlockFromVTPool()
	defer func() {
		clear(msg.Links)
		msg.ReturnToVTPool()
	}()
	if err := msg.UnmarshalVTUnsafe(nd.RawData()); err != nil {
		return nil, fmt.Errorf("%w %s: %w", ErrNotABlock, nd.Cid(), err)
	}
	if len(msg.ProtoReflect().GetUnknown()) > 0 {
		return nil, fmt.Errorf("%w %s: unknown fields", ErrNotABlock, nd.Cid())
	}
	return msg.Data, nil
}

// The data aliases nd's bytes; the links are appended to links.
func DecodeBlock(nd blocks.Block, links []cid.Cid) ([]byte, []cid.Cid, error) {
	msg := pb.BlockFromVTPool()
	defer func() {
		clear(msg.Links)
		msg.ReturnToVTPool()
	}()
	if err := msg.UnmarshalVTUnsafe(nd.RawData()); err != nil {
		return nil, links, fmt.Errorf("%w %s: %w", ErrNotABlock, nd.Cid(), err)
	}
	if len(msg.ProtoReflect().GetUnknown()) > 0 {
		return nil, links, fmt.Errorf("%w %s: unknown fields", ErrNotABlock, nd.Cid())
	}
	links = slices.Grow(links, len(msg.Links))
	for _, l := range msg.Links {
		c, ok := CidOver(l)
		if !ok {
			return nil, links, fmt.Errorf("%w %s: a link that is not a cid", ErrNotABlock, nd.Cid())
		}
		links = append(links, c)
	}
	return msg.Data, links, nil
}

// CidOver aliases b, which must never change after. It relies on a cid.Cid
// being the cid's bytes as a string, which TestCidOverIsACid checks.
func CidOver(b []byte) (cid.Cid, bool) {
	if !isCid(b) {
		return cid.Undef, false
	}
	return CastCid(b), true
}

// CastCid aliases b, a cid's bytes, which must never change after.
func CastCid(b []byte) cid.Cid {
	s := unsafe.String(unsafe.SliceData(b), len(b))
	return *(*cid.Cid)(unsafe.Pointer(&s))
}

// cidBytes aliases c's string.
func cidBytes(c cid.Cid) []byte {
	k := c.KeyString()
	return unsafe.Slice(unsafe.StringData(k), len(k))
}

func isCid(b []byte) bool {
	if len(b) == 34 && b[0] == mh.SHA2_256 && b[1] == 32 {
		return true
	}
	rest := b
	var v [4]uint64
	for i := range v {
		x, n := binary.Uvarint(rest)
		if n <= 0 || n != uvarintLen(x) {
			return false
		}
		v[i], rest = x, rest[n:]
	}
	return v[0] == 1 && v[3] == uint64(len(rest))
}

func uvarintLen(x uint64) int {
	n := 1
	for ; x >= 0x80; x >>= 7 {
		n++
	}
	return n
}

func (b *Block) RawData() []byte { return b.raw }

func (b *Block) Cid() cid.Cid { return b.c }

func (b *Block) String() string { return "[statetree block " + b.c.String() + "]" }

func (b *Block) Loggable() map[string]any { return map[string]any{"block": b.c} }

func (b *Block) Data() []byte { return b.data }

// Links are named by position in the data, so no path resolves.
func (b *Block) Resolve(path []string) (any, []string, error) {
	if len(path) == 0 {
		return b, nil, nil
	}
	return nil, nil, ipld.ErrNotFound{Cid: b.c}
}

func (b *Block) Tree(string, int) []string { return nil }

func (b *Block) ResolveLink(path []string) (*ipld.Link, []string, error) {
	return nil, nil, ipld.ErrNotFound{Cid: b.c}
}

func (b *Block) Copy() ipld.Node {
	raw := slices.Clone(b.raw)
	return &Block{c: b.c, raw: raw, data: raw[len(raw)-len(b.data):]}
}

// A bad link, which NewBlock never writes, is left out.
func (b *Block) Links() []*ipld.Link {
	links, err := linksOf(b)
	if err != nil {
		return nil
	}
	return links
}

func (b *Block) Stat() (*ipld.NodeStat, error) {
	links, err := linksOf(b)
	if err != nil {
		return nil, err
	}
	return &ipld.NodeStat{Hash: b.c.String(), NumLinks: len(links), BlockSize: len(b.raw), DataSize: len(b.data), CumulativeSize: len(b.raw)}, nil
}

func (b *Block) Size() (uint64, error) { return uint64(len(b.raw)), nil }
