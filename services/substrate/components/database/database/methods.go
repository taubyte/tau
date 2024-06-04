package database

import (
	iface "github.com/taubyte/tau/core/services/substrate/components/database"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func (db *Database) DBContext() iface.Context {
	return db.dbContext
}

func (db *Database) SetConfig(config *structureSpec.Database) {
	db.dbContext.Config = config
}

func (db *Database) Config() *structureSpec.Database {
	return db.config
}

func (db *Database) KV() iface.KV {
	return db.keystore
}

func (db *Database) Close() {
	if db.keystore != nil {
		db.keystore.Close()
	}

	db.instanceCtxC()
}
