package globals

import (
	iface "github.com/taubyte/go-interfaces/services/substrate/components/database"
	structureSpec "github.com/taubyte/go-specs/structure"
)

type Database struct {
	hash      string
	dbContext iface.Context
	config    *structureSpec.Database
	keystore  iface.KV
}
