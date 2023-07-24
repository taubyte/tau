package storage

import (
	"context"
	"fmt"
	"regexp"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/go-interfaces/kvdb"
	storageIface "github.com/taubyte/go-interfaces/services/substrate/components/storage"
	kvd "github.com/taubyte/odo/pkgs/kvdb/database"
	common "github.com/taubyte/odo/protocols/node/components/storage/common"
)

func storageError(ctx storageIface.Context) string {
	if len(ctx.ApplicationId) > 0 {
		return fmt.Sprintf("Storage(%s/%s/%s :: %s)", ctx.ProjectId, ctx.ApplicationId, ctx.Config.Id, ctx.Matcher)
	} else {
		return fmt.Sprintf("Storage(%s/%s :: %s)", ctx.ProjectId, ctx.Config.Id, ctx.Matcher)
	}
}

func New(srv storageIface.Service, storageContext storageIface.Context, logger log.StandardLogger, matches map[string]kvdb.KVDB) (storageIface.Storage, error) {
	storageHash, err := common.GetStorageHash(storageContext)
	if err != nil {
		return nil, fmt.Errorf("getting hash for `%s` failed with: %s", storageError(storageContext), err)
	}
	_store := &Store{
		srv:     srv,
		context: storageContext,
		id:      storageHash,
	}
	_store.instanceCtx, _store.instanceCtxC = context.WithCancel(srv.Node().Context())

	// TODO: Implement matching score
	if storageContext.Config.Match != "" {
		for name, storage := range matches {
			if name == storageContext.Config.Match {
				if !storageContext.Config.Regex {
					_store.KVDB = storage
					val, err := _store.SmartOps()
					if err != nil || val > 0 {
						if err != nil {
							return nil, fmt.Errorf("running smartops for `%s` failed with: %s", storageError(storageContext), err)
						}
						return nil, fmt.Errorf("exited: %d", val)
					}

					return _store, nil
				} else {
					match, err := regexp.Match(storageContext.Config.Match, []byte(name))
					if err != nil {
						return nil, fmt.Errorf("regexp match for storage `%s` failed with: %s", storageContext.Matcher, err)
					}

					if match {
						_store.KVDB = storage
						val, err := _store.SmartOps()
						if err != nil || val > 0 {
							if err != nil {
								return nil, fmt.Errorf("running smartops for `%s` failed with: %s", storageError(storageContext), err)
							}
							return nil, fmt.Errorf("exited: %d", val)
						}

						return _store, nil
					}
				}
			}
		}
	}

	val, err := _store.SmartOps()
	if err != nil || val > 0 {
		if err != nil {
			return nil, fmt.Errorf("running smartops for `%s` failed with: %s", storageError(storageContext), err)
		}
		return nil, fmt.Errorf("exited: %d", val)
	}

	_storage, err := kvd.New(logger, srv.Node(), storageHash, common.BroadcastInterval)
	if err != nil {
		return nil, fmt.Errorf("creating new kvdb for `%s` failed with: %s", storageError(storageContext), err)
	}

	_store.KVDB = _storage
	return _store, nil
}
