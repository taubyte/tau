package migration

import (
	"context"
	"fmt"
	"strings"

	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	tnsIface "github.com/taubyte/tau/core/services/tns"
	spec "github.com/taubyte/tau/pkg/specs/common"
	mh "github.com/taubyte/tau/utils/multihash"
)

// resolve maps local namespace hashes to instance identities. Two sources:
//
//  1. TNS — exact-match database/storage configs hash directly
//     (mh.Hash(project+app+match)), and each project's node-local global kvdb
//     lives at mh.Hash("global"+project) (the hoarder-hosted global for the
//     same project is keyed mh.Hash(project+"global"), hence the remap).
//  2. The hoarder registry — regex-matched instances have no derivable name
//     until live traffic first-touches them, which records their identity;
//     Metas recovers it by hash.
//
// Hashes neither source names are returned as unresolved: left intact,
// reported, retried next pass. A failed listing aborts the pass (err != nil) —
// an outage must carry everything over rather than classify anything.
func (m *Migrator) resolve(ctx context.Context, hashes []string) (map[string]hoarderIface.InstanceInfo, []string, error) {
	index, err := m.tnsIndex(ctx)
	if err != nil {
		return nil, nil, err
	}

	resolved := make(map[string]hoarderIface.InstanceInfo, len(hashes))
	unmatched := make([]string, 0, len(hashes))
	for _, h := range hashes {
		if info, ok := index[h]; ok {
			resolved[h] = info
		} else {
			unmatched = append(unmatched, h)
		}
	}

	if len(unmatched) > 0 {
		metas, err := m.hoarder.Metas(unmatched...)
		if err != nil {
			return nil, nil, fmt.Errorf("querying hoarder metas failed with: %w", err)
		}
		known := make(map[string]hoarderIface.InstanceInfo, len(metas))
		for _, info := range metas {
			known[info.Hash] = info
		}
		still := unmatched[:0]
		for _, h := range unmatched {
			if info, ok := known[h]; ok {
				resolved[h] = info
			} else {
				still = append(still, h)
			}
		}
		unmatched = still
	}

	return resolved, unmatched, nil
}

// tnsIndex builds hash → identity for everything TNS can name: exact-match
// database/storage configs across all (project, application) scopes, plus the
// per-project global remap.
func (m *Migrator) tnsIndex(ctx context.Context) (map[string]hoarderIface.InstanceInfo, error) {
	scopes, projects, err := m.tnsScopes()
	if err != nil {
		return nil, fmt.Errorf("listing TNS project scopes failed with: %w", err)
	}

	index := make(map[string]hoarderIface.InstanceInfo)

	for _, pid := range projects {
		// The node-local global kvdb of a project.
		index[mh.Hash("global"+pid)] = hoarderIface.InstanceInfo{
			Hash: mh.Hash("global" + pid),
			Kind: hoarderIface.Global,
			Meta: hoarderIface.MetaData{ProjectId: pid, Match: "global"},
		}
	}

	for _, sc := range scopes {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// A scope listing that fails contributes nothing this pass — its
		// instances stay unresolved and are retried; nothing is classified on
		// an error.
		if dbs, _, branch, err := m.tns.Database().All(sc.project, sc.app, spec.DefaultBranches...).List(); err == nil {
			for id, cfg := range dbs {
				if cfg.Regex {
					continue
				}
				h := mh.Hash(sc.project + sc.app + cfg.Match)
				index[h] = hoarderIface.InstanceInfo{
					Hash: h,
					Kind: hoarderIface.Database,
					Meta: hoarderIface.MetaData{ConfigId: id, ProjectId: sc.project, ApplicationId: sc.app, Match: cfg.Match, Branch: branch},
				}
			}
		}
		if sts, _, branch, err := m.tns.Storage().All(sc.project, sc.app, spec.DefaultBranches...).List(); err == nil {
			for id, cfg := range sts {
				if cfg.Regex {
					continue
				}
				h := mh.Hash(sc.project + sc.app + cfg.Match)
				index[h] = hoarderIface.InstanceInfo{
					Hash: h,
					Kind: hoarderIface.Storage,
					Meta: hoarderIface.MetaData{ConfigId: id, ProjectId: sc.project, ApplicationId: sc.app, Match: cfg.Match, Branch: branch},
				}
			}
		}
	}

	return index, nil
}

type tnsScope struct {
	project string
	app     string
}

// tnsScopes walks the TNS projects index and returns every (project, app)
// scope plus the distinct project ids.
func (m *Migrator) tnsScopes() ([]tnsScope, []string, error) {
	keysIface, err := m.tns.Lookup(tnsIface.Query{Prefix: []string{"projects"}})
	if err != nil {
		return nil, nil, err
	}
	keys, ok := keysIface.([]string)
	if !ok {
		return nil, nil, fmt.Errorf("unexpected lookup result type %T", keysIface)
	}

	seen := make(map[tnsScope]struct{})
	pids := make(map[string]struct{})
	for _, k := range keys {
		parts := strings.Split(strings.TrimPrefix(k, "/"), "/")
		if len(parts) < 2 || parts[0] != "projects" {
			continue
		}
		pid := parts[1]
		pids[pid] = struct{}{}
		seen[tnsScope{project: pid}] = struct{}{}
		if len(parts) >= 4 && parts[2] == "applications" {
			seen[tnsScope{project: pid, app: parts[3]}] = struct{}{}
		}
	}

	scopes := make([]tnsScope, 0, len(seen))
	for sc := range seen {
		scopes = append(scopes, sc)
	}
	projects := make([]string, 0, len(pids))
	for pid := range pids {
		projects = append(projects, pid)
	}
	return scopes, projects, nil
}
