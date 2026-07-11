package migration

import (
	"context"
	"sort"

	"github.com/ipfs/boxo/blockservice"
	"github.com/ipfs/boxo/blockstore"
	offline "github.com/ipfs/boxo/exchange/offline"
	"github.com/ipfs/boxo/ipld/merkledag"
	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
	crdt "github.com/ipfs/go-ds-crdt"
	ipld "github.com/ipfs/go-ipld-format"
)

// crdtPrefix is where every kvdb namespace lives in the node's datastore
// (pkg/kvdb opens path p at /crdt/<p>).
const crdtPrefix = "/crdt/"

// namespaces enumerates the distinct kvdb namespaces held in the node's local
// datastore. On a migrated (or fresh) node this is one empty prefix query.
func (m *Migrator) namespaces(ctx context.Context) ([]string, error) {
	return namespacesOf(ctx, m.node.Store())
}

func namespacesOf(ctx context.Context, store ds.Batching) ([]string, error) {
	res, err := store.Query(ctx, query.Query{Prefix: crdtPrefix, KeysOnly: true})
	if err != nil {
		return nil, err
	}
	defer res.Close()

	set := make(map[string]struct{})
	for e := range res.Next() {
		if e.Error != nil {
			return nil, e.Error
		}
		if h := splitNamespaceKey(e.Key); h != "" {
			set[h] = struct{}{}
		}
	}

	out := make([]string, 0, len(set))
	for h := range set {
		out = append(out, h)
	}
	sort.Strings(out)
	return out, nil
}

// openLegacyView opens a READ-side view of a node-local kvdb namespace with a
// nil broadcaster (go-ds-crdt's offline mode) and an offline-exchange DAG
// service. Reading must never announce heads or fetch remote blocks: the local
// copy joining the live CRDT of the same path would merge data around the
// hoarder write path (and around its at-rest cipher). This is the only place
// the package touches go-ds-crdt.
func openLegacyView(store ds.Batching, hash string) (*crdt.Datastore, error) {
	opts := crdt.DefaultOptions()
	opts.Logger = logger
	return crdt.New(store, ds.NewKey(crdtPrefix+hash), offlineDAG(store), nil, opts)
}

// offlineDAG is a local-only DAG service over the node's datastore, mirroring
// how the node itself wraps it (blocks under /blocks, identity CIDs inlined) —
// reads that miss locally fail instead of touching the network.
func offlineDAG(store ds.Batching) ipld.DAGService {
	bs := blockstore.NewIdStore(blockstore.NewBlockstore(store))
	return merkledag.NewDAGService(blockservice.New(bs, offline.Exchange(bs)))
}

// Namespaces enumerates the kvdb namespaces held in a node datastore — the
// offline-inspection entry point (tools) over the same logic the boot pass
// uses.
func Namespaces(ctx context.Context, store ds.Batching) ([]string, error) {
	return namespacesOf(ctx, store)
}

// Entries reads every logical key/value of one namespace through the offline
// view — never announcing heads, never touching the network.
func Entries(ctx context.Context, store ds.Batching, hash string) (map[string][]byte, error) {
	view, err := openLegacyView(store, hash)
	if err != nil {
		return nil, err
	}
	defer view.Close()
	return localEntries(ctx, view)
}

// FileCids extracts the file content CIDs a storage-shaped namespace's
// metadata records.
func FileCids(entries map[string][]byte) []string {
	return fileCidsOf(entries)
}

// localEntries lists every logical key/value of a namespace through its
// offline view (tombstone-correct, unlike scanning raw set keys).
func localEntries(ctx context.Context, view *crdt.Datastore) (map[string][]byte, error) {
	res, err := view.Query(ctx, query.Query{})
	if err != nil {
		return nil, err
	}
	defer res.Close()

	out := make(map[string][]byte)
	for e := range res.Next() {
		if e.Error != nil {
			return nil, e.Error
		}
		v := make([]byte, len(e.Value))
		copy(v, e.Value)
		out[e.Key] = v
	}
	return out, nil
}
