package database

import (
	"context"

	p2p "github.com/taubyte/go-interfaces/p2p/peer"
	iface "github.com/taubyte/go-interfaces/services/substrate/database"
	structureSpec "github.com/taubyte/go-specs/structure"
)

var _ iface.Database = &Database{}

type Database struct {
	node      p2p.Node
	dbContext iface.Context
	config    *structureSpec.Database
	keystore  iface.KV

	srv iface.Service

	instanceCtx  context.Context
	instanceCtxC context.CancelFunc
}
