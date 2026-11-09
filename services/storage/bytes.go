package storage

import (
	"context"
	"fmt"
	"io"

	"github.com/ipfs/boxo/ipld/merkledag"
	ufsio "github.com/ipfs/boxo/ipld/unixfs/io"
	"github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	ipld "github.com/ipfs/go-ipld-format"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/p2p/peer"
)

// peerBytes is a peer's blockstore as this service keeps bytes in it.
type peerBytes struct{ peer.Node }

var _ Bytes = peerBytes{}

func (b peerBytes) Add(ctx context.Context, content io.Reader) (cid.Cid, uint64, []cid.Cid, error) {
	counted := &countingReader{r: content}
	root, err := b.DAG().AddFile(ctx, counted, nil)
	if err != nil {
		return cid.Undef, 0, nil, err
	}
	blocks, err := walk(ctx, b.DAG(), root.Cid())
	return root.Cid(), counted.n, blocks, err
}

func (b peerBytes) Fetch(ctx context.Context, root cid.Cid, from libp2ppeer.ID) ([]cid.Cid, error) {
	// The writer is who has every block, so it is asked first rather than found.
	if err := b.Host().Connect(ctx, libp2ppeer.AddrInfo{ID: from}); err != nil {
		return nil, fmt.Errorf("reaching %s: %w", from, err)
	}
	return walk(ctx, sound{peerBytes: b, NodeGetter: b.DAG().Session(ctx)}, root)
}

// sound reads a block through a session once the copy here, if any, hashes to its
// cid: a block that rotted is deleted first and fetched again. A blockstore hands
// back what it holds without checking it, and a copy that rotted would otherwise be
// kept again by the very fetch that was to replace it.
type sound struct {
	peerBytes
	ipld.NodeGetter
}

func (s sound) Get(ctx context.Context, c cid.Cid) (ipld.Node, error) {
	if err := s.dropRotted(ctx, c); err != nil {
		return nil, err
	}
	return s.NodeGetter.Get(ctx, c)
}

func (s sound) GetMany(ctx context.Context, cids []cid.Cid) <-chan *ipld.NodeOption {
	for _, c := range cids {
		if err := s.dropRotted(ctx, c); err != nil {
			out := make(chan *ipld.NodeOption, 1)
			out <- &ipld.NodeOption{Err: err}
			close(out)
			return out
		}
	}
	return s.NodeGetter.GetMany(ctx, cids)
}

// dropRotted deletes the copy of c here when it does not hash to c.
func (s sound) dropRotted(ctx context.Context, c cid.Cid) error {
	block, err := s.DAG().BlockStore().Get(ctx, c)
	switch {
	case ipld.IsNotFound(err):
		return nil
	case err != nil:
		return err
	}
	sum, err := c.Prefix().Sum(block.RawData())
	if err != nil {
		return err
	}
	if sum.Equals(c) {
		return nil
	}
	if err := s.DAG().DeleteBlocks(ctx, []cid.Cid{c}); err != nil {
		return fmt.Errorf("deleting %s, which rotted: %w", c, err)
	}
	return nil
}

func (b peerBytes) Sync(ctx context.Context) error {
	return b.DAG().BlockLog().Sync(ctx, ds.NewKey("/"))
}

// Read reads every block through sound: what it returns hashes to root, a block that
// rotted here is fetched again, and one not here is fetched.
func (b peerBytes) Read(ctx context.Context, root cid.Cid) (io.ReadSeekCloser, error) {
	get := sound{peerBytes: b, NodeGetter: b.DAG().Session(ctx)}
	nd, err := get.Get(ctx, root)
	if err != nil {
		return nil, err
	}
	return ufsio.NewDagReader(ctx, nd, get)
}

func (b peerBytes) Has(ctx context.Context, c cid.Cid) (bool, error) {
	return b.DAG().HasBlock(ctx, c)
}

// Links reads only this node's copy: a block not here is an error, never a fetch.
func (b peerBytes) Links(ctx context.Context, c cid.Cid) ([]cid.Cid, error) {
	block, err := b.DAG().BlockStore().Get(ctx, c)
	if err != nil {
		return nil, err
	}
	if c.Prefix().Codec == cid.Raw {
		return nil, nil
	}
	nd, err := merkledag.DecodeProtobufBlock(block)
	if err != nil {
		return nil, err
	}
	out := make([]cid.Cid, 0, len(nd.Links()))
	for _, l := range nd.Links() {
		out = append(out, l.Cid)
	}
	return out, nil
}

func (b peerBytes) Block(ctx context.Context, c cid.Cid) ([]byte, error) {
	block, err := b.DAG().BlockStore().Get(ctx, c)
	if ipld.IsNotFound(err) {
		return nil, fmt.Errorf("%w: %s", ErrNotHeld, c)
	}
	if err != nil {
		return nil, err
	}
	return block.RawData(), nil
}

func (b peerBytes) Delete(ctx context.Context, c cid.Cid) error {
	return b.DAG().DeleteBlocks(ctx, []cid.Cid{c})
}

// walk is every block under root, each read through get, which fetches what is not
// here when it is a session.
func walk(ctx context.Context, get ipld.NodeGetter, root cid.Cid) ([]cid.Cid, error) {
	seen := map[cid.Cid]bool{}
	var out []cid.Cid
	stack := []cid.Cid{root}
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[c] {
			continue
		}
		seen[c] = true
		nd, err := get.Get(ctx, c)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", c, err)
		}
		out = append(out, c)
		for _, l := range nd.Links() {
			stack = append(stack, l.Cid)
		}
	}
	return out, nil
}

type countingReader struct {
	r io.Reader
	n uint64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += uint64(n)
	return n, err
}
