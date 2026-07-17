package peer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/ipfs/boxo/bitswap"
	"github.com/ipfs/boxo/bitswap/client"
	bsnet "github.com/ipfs/boxo/bitswap/network/bsnet"
	"github.com/ipfs/boxo/blockservice"
	blockstore "github.com/ipfs/boxo/blockstore"
	chunker "github.com/ipfs/boxo/chunker"
	exchange "github.com/ipfs/boxo/exchange"
	"github.com/ipfs/boxo/ipld/merkledag"
	"github.com/ipfs/boxo/ipld/unixfs/importer/balanced"
	ihelpers "github.com/ipfs/boxo/ipld/unixfs/importer/helpers"
	ufsio "github.com/ipfs/boxo/ipld/unixfs/io"
	provider "github.com/ipfs/boxo/provider"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	ipld "github.com/ipfs/go-ipld-format"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/routing"
	multihash "github.com/multiformats/go-multihash"
)

// DAGService is tau's in-house replacement for ipfs-lite's Peer: the minimal
// boxo glue (blockstore + bitswap + merkledag + unixfs + reprovider) that
// provides an ipld.DAGService plus UnixFS Add/Get. Defaults are frozen to match
// ipfs-lite v1.8.6 exactly (CIDv1, sha2-256, balanced layout, default chunker,
// ARC+bloom cached blockstore, 12h reprovide) so CIDs and on-disk/wire formats
// stay interoperable with nodes still running ipfs-lite.
type DAGService struct {
	ipld.DAGService // merkledag: Add/Get/Remove/...

	bstore     blockstore.Blockstore
	bserv      blockservice.BlockService
	exch       exchange.Interface
	reprovider provider.System

	closeOnce sync.Once
	closeErr  error
}

// newDAG replicates ipfslite.New(ctx, store, nil, host, dht, &Config{}) with the
// default (online) config both tau call sites used.
func newDAG(ctx context.Context, store datastore.Batching, host host.Host, dht routing.Routing) (*DAGService, error) {
	// Blockstore: identity multihash support + ARC/bloom cache (hot path for HasBlock).
	bs := blockstore.NewBlockstore(store, blockstore.WriteThrough(true))
	bs = blockstore.NewIdStore(bs)
	bs, err := blockstore.CachedBlockstore(ctx, bs, blockstore.DefaultCacheOpts())
	if err != nil {
		return nil, fmt.Errorf("setting up blockstore failed: %w", err)
	}

	// Bitswap (providerFinder nil: blocks found via broadcast to connected peers).
	bswapnet := bsnet.NewFromIpfsHost(host)
	bswap := bitswap.New(ctx, bswapnet, nil, bs,
		bitswap.ProviderSearchDelay(1000*time.Millisecond),
		bitswap.EngineBlockstoreWorkerCount(128),
		bitswap.TaskWorkerCount(24),
		bitswap.EngineTaskWorkerCount(24),
		bitswap.MaxOutstandingBytesPerPeer(1<<20),
		bitswap.WithWantHaveReplaceSize(1024),
		bitswap.WithClientOption(client.BroadcastControlEnable(true)),
		bitswap.WithClientOption(client.BroadcastControlMaxPeers(-1)),
		bitswap.WithClientOption(client.BroadcastControlLocalPeers(false)),
		bitswap.WithClientOption(client.BroadcastControlPeeredPeers(false)),
		bitswap.WithClientOption(client.BroadcastControlMaxRandomPeers(64)),
		bitswap.WithClientOption(client.BroadcastControlSendToPendingPeers(false)),
	)
	bserv := blockservice.New(bs, bswap)

	reprovider, err := provider.New(store,
		provider.DatastorePrefix(datastore.NewKey("repro")),
		provider.Online(dht),
		provider.ReproviderInterval(12*time.Hour),
		provider.KeyProvider(bs.AllKeysChan),
	)
	if err != nil {
		_ = bserv.Close()
		return nil, fmt.Errorf("setting up reprovider failed: %w", err)
	}

	d := &DAGService{
		DAGService: merkledag.NewDAGService(bserv),
		bstore:     bs,
		bserv:      bserv,
		exch:       bswap,
		reprovider: reprovider,
	}

	// autoclose: mirror ipfs-lite so a cancelled context still tears down.
	// Close is idempotent (sync.Once), so an explicit Close() and this goroutine
	// racing is safe.
	go func() {
		<-ctx.Done()
		_ = d.Close()
	}()

	return d, nil
}

// Close tears down the reprovider and blockservice. Idempotent.
func (d *DAGService) Close() error {
	d.closeOnce.Do(func() {
		d.closeErr = errors.Join(d.reprovider.Close(), d.bserv.Close())
	})
	return d.closeErr
}

// AddFile chunks and stores a reader as a UnixFS DAG, returning the root node.
// Frozen ipfs-lite defaults: CIDv1, sha2-256, balanced layout, default chunker,
// RawLeaves=false — these determine the CID and MUST NOT change.
func (d *DAGService) AddFile(ctx context.Context, r io.Reader, _ any) (ipld.Node, error) {
	prefix, err := merkledag.PrefixForCidVersion(1)
	if err != nil {
		return nil, fmt.Errorf("bad CID version: %w", err)
	}
	prefix.MhType = multihash.SHA2_256
	prefix.MhLength = -1

	dbp := ihelpers.DagBuilderParams{
		Dagserv:    d,
		Maxlinks:   ihelpers.DefaultLinksPerBlock,
		CidBuilder: &prefix,
	}
	dbh, err := dbp.New(chunker.DefaultSplitter(r))
	if err != nil {
		return nil, err
	}
	return balanced.Layout(dbh)
}

// GetFile returns a reader to a UnixFS file identified by its root CID.
func (d *DAGService) GetFile(ctx context.Context, c cid.Cid) (ufsio.ReadSeekCloser, error) {
	n, err := d.Get(ctx, c)
	if err != nil {
		return nil, err
	}
	return ufsio.NewDagReader(ctx, n, d)
}

// BlockStore exposes the underlying blockstore.
func (d *DAGService) BlockStore() blockstore.Blockstore { return d.bstore }

// HasBlock reports whether a block is available locally.
func (d *DAGService) HasBlock(ctx context.Context, c cid.Cid) (bool, error) {
	return d.bstore.Has(ctx, c)
}

// Session returns a session-based NodeGetter. Required: go-ds-crdt type-asserts
// SessionDAGService on the value kvdb passes it, and degrades to non-session
// fetching without it.
func (d *DAGService) Session(ctx context.Context) ipld.NodeGetter {
	return merkledag.NewSession(ctx, d.DAGService)
}

// Exchange returns the underlying bitswap exchange.
func (d *DAGService) Exchange() exchange.Interface { return d.exch }

// BlockService returns the underlying blockservice.
func (d *DAGService) BlockService() blockservice.BlockService { return d.bserv }

// --- node file operations (UnixFS over the DAG service) ---

type ReadSeekCloser interface {
	io.ReadSeekCloser
	io.WriterTo
}

var errorClosed = errors.New("node is closed")

func (p *node) DeleteFile(id string) error {
	if p.closed.Load() {
		return errorClosed
	}

	_cid, err := cid.Decode(id)
	if err != nil {
		return fmt.Errorf("decoding CID %q failed: %w", id, err)
	}

	if err := p.dag.Remove(p.ctx, _cid); err != nil {
		return fmt.Errorf("removing file with CID %q failed: %w", id, err)
	}

	return nil
}

func (p *node) AddFile(r io.Reader) (_cid string, err error) {
	if p.closed.Load() {
		return "", errorClosed
	}

	var n ipld.Node
	n, err = p.dag.AddFile(p.ctx, r, nil)
	if err != nil {
		return "", fmt.Errorf("adding file to IPFS failed: %w", err)
	}
	_cid = n.Cid().String()
	return
}

// Note: context should have a timeout and depend on the peer context as parent
func (p *node) GetFile(ctx context.Context, id string) (ReadSeekCloser, error) {
	if p.closed.Load() {
		return nil, errorClosed
	}

	_cid, err := cid.Decode(id)
	if err != nil {
		return nil, fmt.Errorf("decoding CID %q failed: %w", id, err)
	}

	file, err := p.dag.GetFile(ctx, _cid)
	if err != nil {
		return nil, fmt.Errorf("getting file with CID %q failed: %w", id, err)
	}

	return file, nil
}

func (p *node) GetFileFromCid(ctx context.Context, cid cid.Cid) (ReadSeekCloser, error) {
	if p.closed.Load() {
		return nil, errorClosed
	}

	file, err := p.dag.GetFile(ctx, cid)
	if err != nil {
		return nil, fmt.Errorf("getting file with CID %q failed: %w", cid.String(), err)
	}

	return file, nil
}

func (p *node) AddFileForCid(r io.Reader) (cid.Cid, error) {
	if p.closed.Load() {
		return cid.Cid{}, errorClosed
	}

	n, err := p.dag.AddFile(p.ctx, r, nil)
	if err != nil {
		return cid.Cid{}, fmt.Errorf("adding file to IPFS failed: %w", err)
	}

	return n.Cid(), nil
}
