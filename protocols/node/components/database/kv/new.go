package kv

import (
	"fmt"

	kvdb "bitbucket.org/taubyte/kvdb/database"
	"github.com/taubyte/go-interfaces/moody"
	"github.com/taubyte/go-interfaces/p2p/peer"
	iface "github.com/taubyte/go-interfaces/services/substrate/database"
	db "github.com/taubyte/odo/protocols/node/components/database/common"
)

func New(size uint64, name string, logger moody.Logger, node peer.Node) (iface iface.KV, err error) {
	store, err := kvdb.New(logger.Std(), node, name, db.BroadcastInterval)
	if err != nil {
		return nil, fmt.Errorf("Creating new kvdb `%s` failed with: %w", name, err)
	}

	return &kv{name: name, database: store, maxSize: size}, nil
}
