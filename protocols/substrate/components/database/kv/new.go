package kv

import (
	"fmt"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/go-interfaces/kvdb"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/database"

	// kvdb "github.com/taubyte/odo/pkgs/kvdb"
	db "github.com/taubyte/odo/protocols/substrate/components/database/common"
)

func New(size uint64, name string, logger log.StandardLogger, factory kvdb.Factory) (iface iface.KV, err error) {
	store, err := factory.New(logger, name, db.BroadcastInterval)
	if err != nil {
		return nil, fmt.Errorf("creating new kvdb `%s` failed with: %w", name, err)
	}

	return &kv{name: name, database: store, maxSize: size}, nil
}
