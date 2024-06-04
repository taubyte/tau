package database

import (
	"context"
	"fmt"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/services/substrate"
	iface "github.com/taubyte/tau/core/services/substrate/components/database"
	"github.com/taubyte/tau/services/substrate/components/database/common"
	kv "github.com/taubyte/tau/services/substrate/components/database/kv"
)

func New(srv substrate.Service, factory kvdb.Factory, dbContext iface.Context) (iface.Database, error) {
	databaseHash, err := common.GetDatabaseHash(dbContext)
	if err != nil {
		return nil, err
	}

	keystore, err := kv.New(dbContext.Config.Size, databaseHash, common.Logger, factory)
	if err != nil {
		return nil, fmt.Errorf("failed creating KV database for %s with error: %v", dbContext.Matcher, err)
	}

	db := &Database{
		srv: srv,

		dbContext: dbContext,
		keystore:  keystore,
	}
	db.instanceCtx, db.instanceCtxC = context.WithCancel(srv.Node().Context())

	val, err := db.SmartOps()
	if err != nil {
		return nil, err
	}
	if val > 0 {
		return nil, fmt.Errorf("exited: %d", val)
	}

	return db, nil
}

func Open(srv substrate.Service, dbContext iface.Context, kv iface.KV) (iface.Database, error) {
	db := &Database{
		srv:       srv,
		dbContext: dbContext,
		keystore:  kv,
	}
	db.instanceCtx, db.instanceCtxC = context.WithCancel(srv.Node().Context())

	val, err := db.SmartOps()
	if err != nil {
		return nil, err
	}
	if val > 0 {
		return nil, fmt.Errorf("exited: %d", val)
	}

	return db, nil
}
