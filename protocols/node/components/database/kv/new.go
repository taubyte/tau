package kv

import (
	"fmt"

	logging "github.com/ipfs/go-log/v2"
	"github.com/taubyte/go-interfaces/p2p/peer"
	iface "github.com/taubyte/go-interfaces/services/substrate/database"
	kvdb "github.com/taubyte/odo/pkgs/kvdb/database"
	db "github.com/taubyte/odo/protocols/node/components/database/common"
)

func New(size uint64, name string, logger logging.StandardLogger, node peer.Node) (iface iface.KV, err error) {
	store, err := kvdb.New(logger, node, name, db.BroadcastInterval)
	if err != nil {
		return nil, fmt.Errorf("Creating new kvdb `%s` failed with: %w", name, err)
	}

	return &kv{name: name, database: store, maxSize: size}, nil
}
