package database

import (
	"context"

	"github.com/taubyte/go-interfaces/services/substrate"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/database"
	structureSpec "github.com/taubyte/go-specs/structure"
)

var _ iface.Database = &Database{}

type Database struct {
	dbContext iface.Context
	config    *structureSpec.Database
	keystore  iface.KV

	srv substrate.Service

	instanceCtx  context.Context
	instanceCtxC context.CancelFunc
}
