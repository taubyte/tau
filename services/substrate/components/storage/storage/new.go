package storage

import (
	"context"
	"fmt"

	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	storageIface "github.com/taubyte/tau/core/services/substrate/components/storage"
	common "github.com/taubyte/tau/services/substrate/components/storage/common"
)

func storageError(ctx storageIface.Context) string {
	if len(ctx.ApplicationId) > 0 {
		return fmt.Sprintf("Storage(%s/%s/%s :: %s)", ctx.ProjectId, ctx.ApplicationId, ctx.Config.Id, ctx.Matcher)
	}
	return fmt.Sprintf("Storage(%s/%s :: %s)", ctx.ProjectId, ctx.Config.Id, ctx.Matcher)
}

// New opens a storage instance: metadata (file cids/versions/sizes) lives in a
// remote hoarder-hosted kvdb; file bytes are stashed to hoarders (see AddFile).
func New(srv storageIface.Service, hoarderClient hoarderIface.Client, storageContext storageIface.Context, branch string) (storageIface.Storage, error) {
	storageHash, err := common.GetStorageHash(storageContext)
	if err != nil {
		return nil, fmt.Errorf("getting hash for `%s` failed with: %s", storageError(storageContext), err)
	}

	store, err := hoarderClient.KVDB(hoarderIface.Storage, storageContext.ProjectId, storageContext.ApplicationId, storageContext.Matcher, branch)
	if err != nil {
		return nil, fmt.Errorf("opening remote kvdb for `%s` failed with: %s", storageError(storageContext), err)
	}

	_store := &Store{
		KVDB:          store,
		srv:           srv,
		hoarderClient: hoarderClient,
		context:       storageContext,
		id:            storageHash,
	}
	_store.instanceCtx, _store.instanceCtxC = context.WithCancel(srv.Node().Context())

	val, err := _store.SmartOps()
	if err != nil || val > 0 {
		if err != nil {
			return nil, fmt.Errorf("running smartops for `%s` failed with: %s", storageError(storageContext), err)
		}
		return nil, fmt.Errorf("exited: %d", val)
	}

	return _store, nil
}
