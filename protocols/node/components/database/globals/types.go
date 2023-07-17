package globals

import (
	p2p "github.com/taubyte/go-interfaces/p2p/peer"
	iface "github.com/taubyte/go-interfaces/services/substrate/database"
	structureSpec "github.com/taubyte/go-specs/structure"
)

type Database struct {
	hash      string
	node      p2p.Node
	dbContext iface.Context
	config    *structureSpec.Database
	keystore  iface.KV
}
