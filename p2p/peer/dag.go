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
	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore"
	ipld "github.com/ipfs/go-ipld-format"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/routing"
	multihash "github.com/multiformats/go-multihash"
	"github.com/taubyte/tau/pkg/pebbleds"
)

// DAGService replaces ipfs-lite's Peer: blockstore, bitswap, merkledag, unixfs
// and reprovider. Its defaults match ipfs-lite v1.8.6 exactly (CIDv1, sha2-256,
// balanced layout, default chunker, 12h reprovide), so CIDs and formats stay
// interoperable.
type DAGService struct {
	ipld.DAGService

	bstore     blockstore.Blockstore
	store      datastore.Batching
	bserv      blockservice.BlockService
	exch       exchange.Interface
	reprovider provider.System
	cancel     context.CancelFunc
	bloom      bloomBuild
	writer     *BlockWriter

	closeOnce sync.Once
	closeErr  error
}

func newDAG(ctx context.Context, store datastore.Batching, host host.Host, dht routing.Routing) (*DAGService, error) {
	ctx, cancel := context.WithCancel(ctx)

	// A pebbleds store answers HasBlock from its own filters; any other store gets
	// boxo's ARC/bloom cache.
	var bs blockstore.Blockstore
	var bloom bloomBuild = noBloom{}
	if pst, ok := store.(*pebbleds.Store); ok {
		bs = blockstore.NewIdStore(pst.Blockstore())
	} else {
		cached, err := blockstore.CachedBlockstore(ctx, blockstore.NewIdStore(blockstore.NewBlockstore(store, blockstore.WriteThrough(true))), blockstore.DefaultCacheOpts())
		if err != nil {
			cancel()
			return nil, fmt.Errorf("setting up blockstore failed: %w", err)
		}
		built, ok := cached.(bloomBuild)
		if !ok {
			cancel()
			return nil, errors.New("setting up blockstore failed: its cache builds no bloom filter to wait for")
		}
		bs, bloom = cached, built
	}

	// providerFinder nil: blocks are found by broadcast to connected peers.
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
		cancel()
		bserv.Close()
		bloom.Wait(context.Background())
		return nil, fmt.Errorf("setting up reprovider failed: %w", err)
	}

	d := &DAGService{
		DAGService: merkledag.NewDAGService(bserv),
		bstore:     bs,
		store:      store,
		bserv:      bserv,
		exch:       bswap,
		reprovider: reprovider,
		cancel:     cancel,
		bloom:      bloom,
		writer:     NewBlockWriter(bs, bswap),
	}

	// A cancelled context still tears down, as ipfs-lite does; Close is
	// idempotent, so racing an explicit Close is safe.
	go func() {
		<-ctx.Done()
		_ = d.Close()
	}()

	return d, nil
}

// bloomBuild is a bloom cache built by a goroutine reading the whole store,
// done once Wait returns.
type bloomBuild interface {
	Wait(context.Context) error
}

type noBloom struct{}

func (noBloom) Wait(context.Context) error { return nil }

// Close returns once nothing it started reads the store: its caller closes the
// store next, and pebble panics on reading a closed one. Idempotent.
func (d *DAGService) Close() error {
	d.closeOnce.Do(func() {
		d.cancel()
		d.writer.Close()
		d.closeErr = errors.Join(d.reprovider.Close(), d.bserv.Close())
		d.bloom.Wait(context.Background())
	})
	return d.closeErr
}

// The frozen ipfs-lite defaults (CIDv1, sha2-256, balanced layout, default
// chunker, RawLeaves false) determine the CID and must not change.
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

func (d *DAGService) GetFile(ctx context.Context, c cid.Cid) (ufsio.ReadSeekCloser, error) {
	n, err := d.Get(ctx, c)
	if err != nil {
		return nil, err
	}
	return ufsio.NewDagReader(ctx, n, d)
}

func (d *DAGService) BlockStore() blockstore.Blockstore { return d.bstore }

func (d *DAGService) WriteBlock(ctx context.Context, b blocks.Block) error {
	return d.writer.WriteBlock(ctx, b)
}

func (d *DAGService) DeleteBlocks(ctx context.Context, cids []cid.Cid) error {
	return d.writer.DeleteBlocks(ctx, cids)
}

// BlockLog is the datastore d writes its blocks to, the one it was made
// over, in order with the store's other writes.
func (d *DAGService) BlockLog() datastore.Datastore { return d.store }

// ReadBlock reads c's stored block without IPLD decoding; a block not held is
// fetched as Get fetches it.
func (d *DAGService) ReadBlock(ctx context.Context, c cid.Cid) (blocks.Block, error) {
	b, err := d.bstore.Get(ctx, c)
	if ipld.IsNotFound(err) {
		return d.DAGService.Get(ctx, c)
	}
	return b, err
}

func (d *DAGService) HasBlock(ctx context.Context, c cid.Cid) (bool, error) {
	return d.bstore.Has(ctx, c)
}

// kvdb fetches through a session when its DAG service offers one.
func (d *DAGService) Session(ctx context.Context) ipld.NodeGetter {
	return merkledag.NewSession(ctx, d.DAGService)
}

func (d *DAGService) Exchange() exchange.Interface { return d.exch }

func (d *DAGService) BlockService() blockservice.BlockService { return d.bserv }

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

// ctx should have a timeout and derive from the peer's context.
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
