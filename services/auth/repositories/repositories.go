package repositories

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/services/auth/hooks"
)

var logger = log.Logger("tau.auth.service.api.repositories")

// A repository's rows live under its canonical key:
//
//	/repositories/github.com/acme/pay/key
//	/repositories/github.com/acme/pay/project
//	/repositories/github.com/acme/pay/hooks/<hook uuid>
//
// The key is VARIABLE DEPTH — `host/owner/name` is three segments and a GitLab
// subgroup is four — so nothing here may count segments to find a boundary.
// Everything below either anchors on the fixed suffix or is handed a Key.
const prefix = "/repositories/"

func root(key repokey.Key) string {
	return prefix + key.String()
}

// keyRow is the row that says a repository exists. It is also what tells a
// listing which of the descendants under the prefix are repositories rather
// than the /project and /hooks/<uuid> rows filed beside them.
const keyRow = "/key"

func (r *repository) Serialize() Data {
	return Data{
		"id":       r.key.String(),
		"provider": r.key.Host,
		"project":  r.project,
		"key":      r.deployKey,
	}
}

func (r *repository) Delete(ctx context.Context) (err error) {
	for _, h := range r.Hooks(ctx) {
		if err = h.Delete(ctx); err != nil {
			logger.Errorf("Failed to delete hook %s: %v", h.ID(), err)
		}
	}

	batch, err := r.kv.Batch(ctx)
	if err != nil {
		return fmt.Errorf("failed to create batch: %w", err)
	}

	if err = batch.Delete(root(r.key) + keyRow); err != nil {
		return fmt.Errorf("failed to batch delete repository key: %w", err)
	}

	// The project link goes with the repository. Leaving it behind kept an
	// unregistered repository listed as part of its project, which is the same
	// kind of half-deleted state the hook reverse index below guards against.
	if err = batch.Delete(root(r.key) + "/project"); err != nil {
		return fmt.Errorf("failed to batch delete repository project link: %w", err)
	}

	if err = batch.Commit(); err != nil {
		return fmt.Errorf("failed to commit repository deletion batch: %w", err)
	}

	return nil
}

func (r *repository) Register(ctx context.Context) (err error) {
	batch, err := r.kv.Batch(ctx)
	if err != nil {
		return fmt.Errorf("failed to create batch: %w", err)
	}

	if err = batch.Put(root(r.key)+keyRow, []byte(r.deployKey)); err != nil {
		return fmt.Errorf("failed to batch repository key: %w", err)
	}

	if err = batch.Commit(); err != nil {
		r.Delete(ctx)
		return fmt.Errorf("failed to commit repository registration batch: %w", err)
	}

	return nil
}

var hookID = regexp.MustCompile("/hooks/([^/]+)$")

func (r *repository) Hooks(ctx context.Context) []hooks.Hook {
	keys, err := r.kv.List(ctx, root(r.key)+"/hooks/")
	if err != nil {
		return nil
	}

	hks := make([]hooks.Hook, 0)
	for _, k := range keys {
		m := hookID.FindStringSubmatch(k)
		logger.Debugf("repo.Hooks match:%s", m)
		if len(m) > 1 {
			h, err := hooks.Fetch(ctx, r.kv, m[1])
			if err == nil {
				hks = append(hks, h)
			} else {
				logger.Error(err)
			}
		}
	}
	return hks
}

// Exist reports whether a repository is registered. It takes the key rather
// than probing a list of providers for it: the host is in the key, so there is
// nothing left to guess at.
func Exist(ctx context.Context, kv kvdb.KVDB, key repokey.Key) bool {
	ret, err := kv.Get(ctx, root(key)+keyRow)
	return err == nil && ret != nil
}

// List returns the canonical key of every registered repository.
//
// It matches the /key row rather than listing the prefix, because a bare list
// returns every descendant — the /project row and one row per hook — and a
// caller stripping the prefix off those gets `acme/pay/project` and
// `acme/pay/hooks/<uuid>` back as if they were repositories.
func List(ctx context.Context, kv kvdb.KVDB) ([]string, error) {
	rows, err := kv.ListRegEx(ctx, prefix, regexp.QuoteMeta(keyRow)+"$")
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, strings.TrimSuffix(strings.TrimPrefix(row, prefix), keyRow))
	}
	return ids, nil
}

func Fetch(ctx context.Context, kv kvdb.KVDB, key repokey.Key) (Repository, error) {
	deployKey, err := kv.Get(ctx, root(key)+keyRow)
	if err != nil {
		return nil, fmt.Errorf("repository `%s` is not registered: %w", key, err)
	}

	project, _ := kv.Get(ctx, root(key)+"/project")

	return New(kv, key, string(deployKey), string(project)), nil
}
