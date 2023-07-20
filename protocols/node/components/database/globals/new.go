package globals

import (
	"fmt"

	"github.com/taubyte/go-interfaces/moody"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/database"
	structureSpec "github.com/taubyte/go-specs/structure"
	kv "github.com/taubyte/odo/protocols/node/components/database/kv"
	"github.com/taubyte/p2p/peer"
)

// TODO, get from project?
var DefaultGlobalConfig = &structureSpec.Database{
	Size: 1000000,
}

func New(hash string, logger moody.Logger, node peer.Node) (iface.Database, error) {
	c := iface.Context{
		Config: DefaultGlobalConfig,
	}

	newKv, err := kv.New(c.Config.Size, hash, logger, node)
	if err != nil {
		return nil, fmt.Errorf("creating global kv failed with error: %v", err)
	}

	return &Database{hash: hash, node: node, dbContext: c, keystore: newKv}, nil
}
