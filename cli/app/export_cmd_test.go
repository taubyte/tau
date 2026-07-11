package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ipfs/boxo/blockservice"
	"github.com/ipfs/boxo/blockstore"
	chunker "github.com/ipfs/boxo/chunker"
	offline "github.com/ipfs/boxo/exchange/offline"
	"github.com/ipfs/boxo/ipld/merkledag"
	"github.com/ipfs/boxo/ipld/unixfs/importer/balanced"
	ihelper "github.com/ipfs/boxo/ipld/unixfs/importer/helpers"
	ds "github.com/ipfs/go-datastore"
	crdt "github.com/ipfs/go-ds-crdt"
	ipld "github.com/ipfs/go-ipld-format"
	helpers "github.com/taubyte/tau/p2p/helpers"
)

// seedExportFixture writes a namespace into a service data dir under the tau
// root, the same shape a node's kvdb layer leaves behind (/crdt/<hash>).
func seedExportFixture(t *testing.T, root, service, hash string, entries map[string][]byte) {
	t.Helper()
	dataRoot := filepath.Join(root, service)
	if err := os.MkdirAll(dataRoot, 0755); err != nil {
		t.Fatal(err)
	}
	store, err := helpers.NewDatastore(dataRoot)
	if err != nil {
		t.Fatalf("opening fixture store failed: %v", err)
	}
	defer store.Close()

	bs := blockstore.NewIdStore(blockstore.NewBlockstore(store))
	dag := merkledag.NewDAGService(blockservice.New(bs, offline.Exchange(bs)))
	view, err := crdt.New(store, ds.NewKey("crdt/"+hash), dag, nil, crdt.DefaultOptions())
	if err != nil {
		t.Fatalf("opening fixture namespace failed: %v", err)
	}
	defer view.Close()
	for k, v := range entries {
		if err := view.Put(context.Background(), ds.NewKey(k), v); err != nil {
			t.Fatalf("seeding key failed: %v", err)
		}
	}
}

func TestExportListAndDump(t *testing.T) {
	root := t.TempDir()
	hash := "fixturehash123"
	seedExportFixture(t, root, "substrate", hash, map[string][]byte{
		"/some/key": []byte("value"),
		"/file/a/1": []byte("QmYwAPJzv5CZsnAzt8auVZRn1pfejgxk2GYDdVQGWvVFrH"), // not present locally
	})

	outFile := filepath.Join(t.TempDir(), "dump.json")
	if err := newApp().Run([]string{"tau", "--root", root, "export", "dump", "--namespace", hash, "--out", outFile}); err != nil {
		t.Fatalf("dump failed: %v", err)
	}

	raw, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	var doc exportDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("dump output not JSON: %v", err)
	}
	if doc.Namespace != hash || len(doc.Entries) != 2 {
		t.Fatalf("unexpected dump: %+v", doc)
	}
	if len(doc.FileCids) != 1 || doc.FileCids[0].Local {
		t.Fatalf("file cid presence wrong (bytes are NOT local): %+v", doc.FileCids)
	}

	if err := newApp().Run([]string{"tau", "--root", root, "export", "list"}); err != nil {
		t.Fatalf("list failed: %v", err)
	}

	// A service dir with no data namespaces lists nothing and succeeds.
	if err := os.MkdirAll(filepath.Join(root, "hoarder"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := newApp().Run([]string{"tau", "--root", root, "export", "list", "--service", "hoarder"}); err != nil {
		t.Fatalf("list on empty service failed: %v", err)
	}

	// Dumping a nonexistent namespace yields an empty doc, not an error.
	if err := newApp().Run([]string{"tau", "--root", root, "export", "dump", "--namespace", "nope"}); err != nil {
		t.Fatalf("dump of empty namespace should succeed: %v", err)
	}
}

func TestExportFileBytes(t *testing.T) {
	root := t.TempDir()
	data := bytes.Repeat([]byte("file payload "), 4096) // multi-block

	// Build a UnixFS DAG in the fixture store, the shape AddFile leaves behind.
	dataRoot := filepath.Join(root, "substrate")
	if err := os.MkdirAll(dataRoot, 0755); err != nil {
		t.Fatal(err)
	}
	store, err := helpers.NewDatastore(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	bs := blockstore.NewIdStore(blockstore.NewBlockstore(store))
	dag := merkledag.NewDAGService(blockservice.New(bs, offline.Exchange(bs)))
	nd, err := balanced.Layout(exportDagBuilder(t, dag, bytes.NewReader(data)))
	if err != nil {
		t.Fatalf("building file dag failed: %v", err)
	}
	store.Close()

	out := filepath.Join(t.TempDir(), "exported.bin")
	if err := newApp().Run([]string{"tau", "--root", root, "export", "file", "--cid", nd.Cid().String(), "--out", out}); err != nil {
		t.Fatalf("export file failed: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("exported bytes differ: %d vs %d", len(got), len(data))
	}

	// A CID the store has never seen fails plainly.
	bad := nd.Cid().String()[:len(nd.Cid().String())-2] + "aa"
	if err := newApp().Run([]string{"tau", "--root", root, "export", "file", "--cid", bad, "--out", out}); err == nil {
		t.Fatal("expected an error for an unknown/invalid cid")
	}
}

func exportDagBuilder(t *testing.T, dag ipld.DAGService, r io.Reader) *ihelper.DagBuilderHelper {
	t.Helper()
	params := ihelper.DagBuilderParams{Dagserv: dag, Maxlinks: ihelper.DefaultLinksPerBlock, CidBuilder: merkledag.V0CidPrefix()}
	db, err := params.New(chunker.NewSizeSplitter(r, 16384))
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestExportRefusesLockedOrMissingStore(t *testing.T) {
	root := t.TempDir()
	seedExportFixture(t, root, "substrate", "somehash", map[string][]byte{"/k": []byte("v")})

	// Hold the store open, as a running node would.
	store, err := helpers.NewDatastore(filepath.Join(root, "substrate"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	err = newApp().Run([]string{"tau", "--root", root, "export", "list"})
	if err == nil {
		t.Fatal("expected an error against a locked store")
	}
	if !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("error must tell the operator to stop the node, got: %v", err)
	}

	// A service dir that doesn't exist errors plainly.
	if err := newApp().Run([]string{"tau", "--root", root, "export", "list", "--service", "nosuch"}); err == nil {
		t.Fatal("expected an error for a missing service data dir")
	}
}
