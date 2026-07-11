package migration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/ipfs/boxo/blockstore"
	"github.com/ipfs/go-cid"
	hoarderClient "github.com/taubyte/tau/clients/p2p/hoarder"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	storageSpec "github.com/taubyte/tau/pkg/specs/storage"
)

// replayInstance moves one instance: replay every local key through the
// hoarder data plane conditionally (putnx — a live write is never
// overwritten), push locally-held storage file bytes to the stash, then
// verify every local key reads back through the hoarder path. It deletes
// nothing — scrub decisions happen after the pass, over verified instances.
func (m *Migrator) replayInstance(ctx context.Context, info hoarderIface.InstanceInfo) *InstanceReport {
	rep := &InstanceReport{Match: info.Meta.Match, Kind: kindName(info.Kind)}

	view, err := openLegacyView(m.node.Store(), info.Hash)
	if err != nil {
		rep.Err = fmt.Sprintf("opening local view: %s", err)
		return rep
	}
	defer view.Close()

	entries, err := localEntries(ctx, view)
	if err != nil {
		rep.Err = fmt.Sprintf("listing local keys: %s", err)
		return rep
	}

	remote, err := m.hoarder.KVDB(info.Kind, info.Meta.ProjectId, info.Meta.ApplicationId, info.Meta.Match, info.Meta.Branch)
	if err != nil {
		rep.Err = fmt.Sprintf("opening hoarder kvdb: %s", err)
		return rep
	}
	defer remote.Close()
	nx, ok := remote.(hoarderIface.NxKVDB)
	if !ok {
		rep.Err = "hoarder kvdb handle lacks conditional writes"
		return rep
	}

	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Replay: bounded worker pool — per-key round trips dominate the pass.
	var (
		mu       sync.Mutex
		firstErr error
		sem      = make(chan struct{}, ReplayWorkers)
		wg       sync.WaitGroup
	)
	for _, k := range keys {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(key string, value []byte) {
			defer wg.Done()
			defer func() { <-sem }()
			existed, err := nx.PutNx(ctx, key, value)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("putnx %s: %w", key, err)
				}
				return
			}
			if !existed {
				rep.Written++
			}
		}(k, entries[k])
	}
	wg.Wait()
	if firstErr != nil {
		rep.Err = firstErr.Error()
		return rep
	}
	if ctx.Err() != nil {
		rep.Err = ctx.Err().Error()
		return rep
	}

	if info.Kind == hoarderIface.Storage {
		m.stashInstanceFiles(ctx, info.Hash, entries, rep)
	}

	// Verify: every local key must read back through the hoarder path. A
	// differing value is a live write superseding the replay — expected, and
	// the local copy is then strictly older. A missing key fails verification.
	equal := 0
	for _, k := range keys {
		if ctx.Err() != nil {
			rep.Err = ctx.Err().Error()
			return rep
		}
		got, err := remote.Get(ctx, k)
		if err != nil {
			if errors.Is(err, hoarderClient.ErrNotFound) {
				rep.Err = fmt.Sprintf("verify: key %s missing after replay", k)
			} else {
				rep.Err = fmt.Sprintf("verify: reading %s: %s", k, err)
			}
			return rep
		}
		if bytes.Equal(got, entries[k]) {
			equal++
		} else {
			rep.Superseded++
		}
	}
	// Keys we wrote read back equal too; Existed is the pre-existing remainder.
	if rep.Existed = equal - rep.Written; rep.Existed < 0 {
		rep.Existed = 0
	}
	rep.Verified = true
	return rep
}

// stashInstanceFiles pushes the storage instance's locally-held file bytes to
// the hoarder stash. CIDs this node has no blocks for are another holder's to
// push (counted, reported); a failed push leaves the local bytes protected by
// the sweep keep-set.
func (m *Migrator) stashInstanceFiles(ctx context.Context, hash string, entries map[string][]byte, rep *InstanceReport) {
	bs := blockstore.NewIdStore(blockstore.NewBlockstore(m.node.Store()))
	pushed := make(map[string]struct{})

	for _, cidStr := range fileCidsOf(entries) {
		if _, done := pushed[cidStr]; done {
			continue
		}
		pushed[cidStr] = struct{}{}

		c, err := cid.Decode(cidStr)
		if err != nil {
			continue // not a CID — foreign metadata shape, nothing to push
		}
		if has, err := bs.Has(ctx, c); err != nil || !has {
			rep.FilesElsewhere++
			continue
		}
		rep.fileCids = append(rep.fileCids, cidStr)

		// Online read on purpose: a locally-rooted file with a missing leaf
		// heals from any fleet holder while we stream it out.
		f, err := m.node.GetFile(ctx, cidStr)
		if err != nil {
			rep.Err = fmt.Sprintf("reading file %s: %s", cidStr, err)
			return
		}
		err = m.hoarder.Stash(cidStr, f, hoarderIface.WithOwner(hash))
		f.Close()
		if err != nil {
			rep.Err = fmt.Sprintf("stashing %s: %s", cidStr, err)
			return
		}
		rep.FilesStashed++
	}
}

// fileCidsOf extracts the file content CIDs recorded in a storage instance's
// metadata (keys file/<name>/<version> → CID string).
func fileCidsOf(entries map[string][]byte) []string {
	prefix := "/" + storageSpec.FilePath.String() + "/"
	out := make([]string, 0)
	for k, v := range entries {
		if strings.HasPrefix(k, prefix) {
			out = append(out, string(v))
		}
	}
	sort.Strings(out)
	return out
}

func kindName(k hoarderIface.ResourceKind) string {
	switch k {
	case hoarderIface.Database:
		return "database"
	case hoarderIface.Storage:
		return "storage"
	case hoarderIface.Global:
		return "global"
	default:
		return fmt.Sprintf("kind-%d", int(k))
	}
}
