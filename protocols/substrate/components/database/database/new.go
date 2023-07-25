package database

import (
	"context"
	"fmt"

	iface "github.com/taubyte/go-interfaces/services/substrate/components/database"
	"github.com/taubyte/odo/protocols/substrate/components/database/common"
	kv "github.com/taubyte/odo/protocols/substrate/components/database/kv"
)

func New(srv iface.Service, dbContext iface.Context) (iface.Database, error) {
	databaseHash, err := common.GetDatabaseHash(dbContext)
	if err != nil {
		return nil, err
	}

	keystore, err := kv.New(dbContext.Config.Size, databaseHash, common.Logger, srv.Node())
	if err != nil {
		return nil, fmt.Errorf("failed creating KV database for %s with error: %v", dbContext.Matcher, err)
	}

	db := &Database{
		srv:       srv,
		node:      srv.Node(),
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

func Open(srv iface.Service, dbContext iface.Context, kv iface.KV) (iface.Database, error) {
	db := &Database{
		srv:       srv,
		node:      srv.Node(),
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
