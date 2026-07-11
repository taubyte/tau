package database

import (
	"context"
	"fmt"

	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	"github.com/taubyte/tau/core/services/substrate"
	iface "github.com/taubyte/tau/core/services/substrate/components/database"
	kv "github.com/taubyte/tau/services/substrate/components/database/kv"
)

// New opens a database instance backed by a remote hoarder-hosted kvdb. The
// first op first-touches the instance on a hoarder; substrate holds no data.
func New(srv substrate.Service, hoarderClient hoarderIface.Client, dbContext iface.Context, branch string) (iface.Database, error) {
	store, err := hoarderClient.KVDB(hoarderIface.Database, dbContext.ProjectId, dbContext.ApplicationId, dbContext.Matcher, branch)
	if err != nil {
		return nil, fmt.Errorf("opening remote kvdb for %s failed with: %w", dbContext.Matcher, err)
	}

	db := &Database{
		srv:       srv,
		dbContext: dbContext,
		keystore:  kv.New(dbContext.Config.Size, dbContext.Matcher, store),
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

// Open wraps an already-resolved KV (used by the global path).
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
