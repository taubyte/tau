package kv

import (
	"fmt"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/core/kvdb"
	iface "github.com/taubyte/tau/core/services/substrate/components/database"

	db "github.com/taubyte/tau/services/substrate/components/database/common"
)

func New(size uint64, name string, logger log.StandardLogger, factory kvdb.Factory) (iface iface.KV, err error) {
	store, err := factory.New(logger, name, db.BroadcastInterval)
	if err != nil {
		return nil, fmt.Errorf("creating new kvdb `%s` failed with: %w", name, err)
	}

	return &kv{name: name, database: store, maxSize: size}, nil
}
