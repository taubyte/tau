package mst

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"unsafe"

	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
	mh "github.com/multiformats/go-multihash"
	pb "github.com/taubyte/tau/pkg/kvdb/mst/pb"
)

// A Block is a block of a tree, or of kvdb: the pb.Block of the cids it
// links, in order, then its data, under a CIDv1 raw sha2-256 cid of its
// bytes. It is an ipld.Node, so a DAG service keeps and fetches it as it
// does any block; what one returns for its cid is a raw node, which
// DecodeBlock reads like a Block.
type Block struct {
	c    cid.Cid
	raw  []byte
	data []byte
}

var _ ipld.Node = (*Block)(nil)

// ErrNotABlock is returned for bytes that are not a Block.
var ErrNotABlock = errors.New("mst: not a block")

// NewBlock is the block of data linking links. It keeps neither.
func NewBlock(data []byte, links []cid.Cid) (*Block, error) {
	msg := pb.BlockFromVTPool()
	defer func() {
		clear(msg.Links)
		msg.ReturnToVTPool()
	}()
	msg.Links = slices.Grow(msg.Links[:0], len(links))[:len(links)]
	for i, l := range links {
		k := l.KeyString()
		msg.Links[i] = unsafe.Slice(unsafe.StringData(k), len(k))
	}
	msg.Data = data
	raw := make([]byte, msg.SizeVT())
	if _, err := msg.MarshalToSizedBufferVT(raw); err != nil {
		return nil, err
	}
	c, err := blockCid(raw)
	if err != nil {
		return nil, err
	}
	// Data is the last field, so the last bytes.
	return &Block{c: c, raw: raw, data: raw[len(raw)-len(data):]}, nil
}

// BlockPrefix is the first bytes of every Block's cid: CIDv1, raw, and a
// 32-byte sha2-256.
const BlockPrefix = "\x01\x55\x12\x20"

// blockCid is the CIDv1 raw sha2-256 cid of raw.
func blockCid(raw []byte) (cid.Cid, error) {
	var h [2 + sha256.Size]byte
	h[0], h[1] = mh.SHA2_256, sha256.Size
	sum := sha256.Sum256(raw)
	copy(h[2:], sum[:])
	return cid.NewCidV1(cid.Raw, h[:]), nil
}

// IsBlockCid reports whether c names a Block: CIDv1, raw, a 32-byte
// sha2-256.
func IsBlockCid(c cid.Cid) bool {
	k := c.KeyString()
	return len(k) == len(BlockPrefix)+sha256.Size && k[:len(BlockPrefix)] == BlockPrefix
}

// ParseBlock is the Block raw spells under c, over raw. It does not check
// that c is raw's cid.
func ParseBlock(c cid.Cid, raw []byte) (*Block, error) {
	b := &Block{c: c, raw: raw}
	data, err := BlockData(b)
	if err != nil {
		return nil, err
	}
	b.data = data
	return b, nil
}

// BlockData is nd's block's data, over nd's bytes.
func BlockData(nd ipld.Node) ([]byte, error) {
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

// DecodeBlock reads nd as a Block: its data, over nd's bytes, and its links
// appended to links.
func DecodeBlock(nd ipld.Node, links []cid.Cid) ([]byte, []cid.Cid, error) {
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
		c, err := cid.Cast(l)
		if err != nil {
			return nil, links, fmt.Errorf("%w %s: %w", ErrNotABlock, nd.Cid(), err)
		}
		links = append(links, c)
	}
	return msg.Data, links, nil
}

func (b *Block) RawData() []byte { return b.raw }

func (b *Block) Cid() cid.Cid { return b.c }

func (b *Block) String() string { return "[mst block " + b.c.String() + "]" }

func (b *Block) Loggable() map[string]any { return map[string]any{"block": b.c} }

// Data is the block's data.
func (b *Block) Data() []byte { return b.data }

// Resolve resolves no path: a block's links are named by position, in its
// data.
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

// Links is the block's links. A bad link, which NewBlock never writes, is
// left out.
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
