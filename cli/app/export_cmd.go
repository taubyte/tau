package app

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"

	"github.com/ipfs/boxo/blockservice"
	"github.com/ipfs/boxo/blockstore"
	offline "github.com/ipfs/boxo/exchange/offline"
	"github.com/ipfs/boxo/ipld/merkledag"
	unixfile "github.com/ipfs/boxo/ipld/unixfs/file"
	"github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	helpers "github.com/taubyte/tau/p2p/helpers"
	"github.com/taubyte/tau/services/substrate/migration"
	"github.com/urfave/cli/v2"
)

// exportDataCommand inspects and exports the project data still held in a
// STOPPED node's datastore — the operator action path for namespaces the
// data migration reports but cannot move (no naming config, or bytes below
// the replica target). Read-only: it never writes to the store.
func exportDataCommand() *cli.Command {
	serviceFlag := &cli.StringFlag{
		Name:  "service",
		Value: "substrate",
		Usage: "service whose data directory to open (under the tau root)",
	}
	return &cli.Command{
		Name:  "export",
		Usage: "inspect/export project data held in a stopped node's datastore (read-only)",
		Subcommands: []*cli.Command{
			{
				Name:   "list",
				Usage:  "list data namespaces and their key/file counts",
				Flags:  []cli.Flag{serviceFlag},
				Action: withNodeStore(exportList),
			},
			{
				Name:  "dump",
				Usage: "dump one namespace's keys/values as JSON",
				Flags: []cli.Flag{
					serviceFlag,
					&cli.StringFlag{Name: "namespace", Usage: "namespace hash (see list)", Required: true},
					&cli.StringFlag{Name: "out", Usage: "output file (default: stdout)"},
				},
				Action: withNodeStore(exportDump),
			},
			{
				Name:  "file",
				Usage: "extract a file's bytes from the local blockstore",
				Flags: []cli.Flag{
					serviceFlag,
					&cli.StringFlag{Name: "cid", Usage: "file content CID (see dump)", Required: true},
					&cli.StringFlag{Name: "out", Usage: "output file", Required: true},
				},
				Action: withNodeStore(exportFile),
			},
		},
	}
}

// withNodeStore opens the service's datastore (<root>/<service>) read-side and
// hands it to the action. A running node holds the store's lock — surface that
// plainly.
func withNodeStore(fn func(*cli.Context, ds.Batching) error) cli.ActionFunc {
	return func(c *cli.Context) error {
		dataRoot := path.Join(c.Path("root"), c.String("service"))
		if _, err := os.Stat(dataRoot); err != nil {
			return fmt.Errorf("data root %q: %w", dataRoot, err)
		}
		store, err := helpers.NewDatastore(dataRoot)
		if err != nil {
			return fmt.Errorf("opening datastore at %q failed (is the node still running? it must be stopped): %w", dataRoot, err)
		}
		defer store.Close()
		return fn(c, store)
	}
}

func exportList(c *cli.Context, store ds.Batching) error {
	ctx := c.Context
	hashes, err := migration.Namespaces(ctx, store)
	if err != nil {
		return err
	}
	if len(hashes) == 0 {
		fmt.Println("no data namespaces")
		return nil
	}
	for _, h := range hashes {
		entries, err := migration.Entries(ctx, store, h)
		if err != nil {
			fmt.Printf("%s\tkeys: ?\terror: %s\n", h, err)
			continue
		}
		fmt.Printf("%s\tkeys: %d\tfiles: %d\n", h, len(entries), len(migration.FileCids(entries)))
	}
	return nil
}

type exportEntry struct {
	Key         string `json:"key"`
	ValueBase64 string `json:"value_base64"`
}

type exportDoc struct {
	Namespace string        `json:"namespace"`
	Entries   []exportEntry `json:"entries"`
	FileCids  []exportRef   `json:"file_cids,omitempty"`
}

type exportRef struct {
	Cid   string `json:"cid"`
	Local bool   `json:"local"` // root block present in this store
}

func exportDump(c *cli.Context, store ds.Batching) error {
	ctx := c.Context
	hash := c.String("namespace")
	entries, err := migration.Entries(ctx, store, hash)
	if err != nil {
		return err
	}

	doc := exportDoc{Namespace: hash, Entries: make([]exportEntry, 0, len(entries))}
	for k, v := range entries {
		doc.Entries = append(doc.Entries, exportEntry{Key: k, ValueBase64: base64.StdEncoding.EncodeToString(v)})
	}

	bs := blockstore.NewIdStore(blockstore.NewBlockstore(store))
	for _, cs := range migration.FileCids(entries) {
		ref := exportRef{Cid: cs}
		if c, err := cid.Decode(cs); err == nil {
			ref.Local, _ = bs.Has(ctx, c)
		}
		doc.FileCids = append(doc.FileCids, ref)
	}

	out := os.Stdout
	if p := c.String("out"); p != "" {
		f, err := os.Create(p)
		if err != nil {
			return err
		}
		defer f.Close()
		out = f
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

func exportFile(c *cli.Context, store ds.Batching) error {
	ctx := c.Context
	root, err := cid.Decode(c.String("cid"))
	if err != nil {
		return fmt.Errorf("bad cid: %w", err)
	}

	bs := blockstore.NewIdStore(blockstore.NewBlockstore(store))
	dag := merkledag.NewDAGService(blockservice.New(bs, offline.Exchange(bs)))
	nd, err := dag.Get(ctx, root)
	if err != nil {
		return fmt.Errorf("reading %s from the local store failed with: %w", root, err)
	}
	f, err := unixfile.NewUnixfsFile(ctx, dag, nd)
	if err != nil {
		return fmt.Errorf("interpreting %s as a file failed with: %w", root, err)
	}
	r, ok := f.(io.Reader)
	if !ok {
		return fmt.Errorf("cid is a directory, not a file")
	}

	out, err := os.Create(c.String("out"))
	if err != nil {
		return err
	}
	defer out.Close()
	n, err := io.Copy(out, r)
	if err != nil {
		return err
	}
	fmt.Printf("wrote %d bytes to %s\n", n, c.String("out"))
	return nil
}
