package globals

import (
	iface "github.com/taubyte/go-interfaces/services/substrate/database"
	structureSpec "github.com/taubyte/go-specs/structure"
	"github.com/taubyte/p2p/peer"
)

type Database struct {
	hash      string
	node      *peer.Node
	dbContext iface.Context
	config    *structureSpec.Database
	keystore  iface.KV
}
