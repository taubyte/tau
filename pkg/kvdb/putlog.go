package kvdb

import (
	"fmt"

	ds "github.com/ipfs/go-datastore"
)

// putLogLine describes a write without its value: the databases hold deploy
// keys, hook secrets and certificates, and a log is read far more widely than
// the store.
func putLogLine(k ds.Key, v []byte) string {
	return fmt.Sprintf("Added: [%s] (%d bytes)", k, len(v))
}
