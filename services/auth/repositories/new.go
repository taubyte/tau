package repositories

import (
	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// New builds a repository from its canonical key and the deploy key held for it.
//
// It takes a repokey.Key rather than a string so a caller cannot hand it a
// spelling: a Key only exists because repokey.Parse produced it, which is the
// same grammar the store, the sources index and the tenancy check read. It
// returns no error because there is nothing left to fail on — the parse already
// happened, and that is the point of doing it once.
func New(kv kvdb.KVDB, key repokey.Key, deployKey, project string) Repository {
	return &repository{
		kv:        kv,
		key:       key,
		project:   project,
		deployKey: deployKey,
	}
}
