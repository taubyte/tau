package globals

import (
	"fmt"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/core/kvdb"
	iface "github.com/taubyte/tau/core/services/substrate/components/database"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	kv "github.com/taubyte/tau/services/substrate/components/database/kv"
)

// TODO, get from project?
var DefaultGlobalConfig = &structureSpec.Database{
	Size: 1000000,
}

func New(hash string, logger log.StandardLogger, factory kvdb.Factory) (iface.Database, error) {
	c := iface.Context{
		Config: DefaultGlobalConfig,
	}

	newKv, err := kv.New(c.Config.Size, hash, logger, factory)
	if err != nil {
		return nil, fmt.Errorf("creating global kv failed with error: %v", err)
	}

	return &Database{hash: hash, dbContext: c, keystore: newKv}, nil
}
