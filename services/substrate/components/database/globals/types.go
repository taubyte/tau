package globals

import (
	iface "github.com/taubyte/tau/core/services/substrate/components/database"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

type Database struct {
	hash      string
	dbContext iface.Context
	config    *structureSpec.Database
	keystore  iface.KV
}
