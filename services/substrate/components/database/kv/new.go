package kv

import (
	"github.com/taubyte/tau/core/kvdb"
	iface "github.com/taubyte/tau/core/services/substrate/components/database"
)

// New wraps a kvdb.KVDB (now hoarder-backed) with the substrate size-tracking
// layer. The handle is a remote stream client, not a local datastore.
func New(size uint64, name string, store kvdb.KVDB) iface.KV {
	return &kv{name: name, database: store, maxSize: size}
}
