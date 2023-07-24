package kv

import (
	"fmt"

	"github.com/ipfs/go-log/v2"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/database"
	kvdb "github.com/taubyte/odo/pkgs/kvdb/database"
	db "github.com/taubyte/odo/protocols/node/components/database/common"
	"github.com/taubyte/p2p/peer"
)

func New(size uint64, name string, logger log.StandardLogger, node peer.Node) (iface iface.KV, err error) {
	store, err := kvdb.New(logger, node, name, db.BroadcastInterval)
	if err != nil {
		return nil, fmt.Errorf("creating new kvdb `%s` failed with: %w", name, err)
	}

	return &kv{name: name, database: store, maxSize: size}, nil
}
