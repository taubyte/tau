package client

import (
	"context"
	"strings"

	"github.com/ipfs/go-datastore"
	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (d *Database) Get(ctx context.Context, key string) (data []byte, err errno.Error) {
	var err0 error
	data, err0 = d.KV().Get(ctx, key)
	if err0 != nil {
		if strings.Contains(err0.Error(), datastore.ErrNotFound.Error()) {
			err = errno.ErrorDatabaseKeyNotFound
		} else {
			err = errno.ErrorDatabaseGetFailed
		}
	}

	return
}

func (f *Factory) databaseGet(ctx context.Context, module common.Module,
	databaseId,
	keyPtr, keyLen,
	dataPtr uint32,
) uint32 {

	database, err := f.getDatabase(databaseId)
	if err != 0 {
		return uint32(err)
	}

	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return uint32(err)
	}

	data, err := database.Get(ctx, key)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteBytes(module, dataPtr, data))
}

func (f *Factory) databaseGetSize(ctx context.Context, module common.Module,
	keystoreId,
	keyPtr, keyLen, // key string
	sizePtr uint32,
) uint32 {

	database, err := f.getDatabase(keystoreId)
	if err != 0 {
		return uint32(err)
	}

	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return uint32(err)
	}

	data, err := database.Get(ctx, key)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteBytesSize(module, sizePtr, data))
}

func (f *Factory) databasePut(ctx context.Context, module common.Module,
	databaseId,
	keyPtr, keyLen,
	bufPtr, bufSize uint32,
) uint32 {

	database, err := f.getDatabase(databaseId)
	if err != 0 {
		return uint32(err)
	}

	_key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return uint32(err)
	}

	data, err := f.ReadBytes(module, bufPtr, bufSize)
	if err != 0 {
		return uint32(err)
	}

	_err := database.KV().Put(ctx, _key, data)
	if _err != nil {
		return uint32(errno.ErrorDatabasePutFailed)
	}

	return 0
}

func (f *Factory) databaseClose(ctx context.Context, module common.Module,
	databaseId uint32,
) uint32 {
	database, err := f.getDatabase(databaseId)
	if err != 0 {
		return uint32(err)
	}

	f.databaseLock.Lock()
	defer f.databaseLock.Unlock()
	delete(f.database, databaseId)

	database.Close()
	return 0
}

func (f *Factory) databaseDelete(ctx context.Context, module common.Module,
	databaseId,
	keyPtr, keyLen uint32,
) uint32 {

	database, err := f.getDatabase(databaseId)
	if err != 0 {
		return uint32(err)
	}

	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return uint32(err)
	}

	_err := database.KV().Delete(ctx, key)
	if _err != nil {
		return uint32(errno.ErrorDatabaseDeleteFailed)
	}

	return 0
}

func (f *Factory) databaseList(ctx context.Context, module common.Module,
	databaseId,
	keyPtr, keyLen,
	dataPtr uint32,
) uint32 {

	database, err := f.getDatabase(databaseId)
	if err != 0 {
		return uint32(err)
	}

	_key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return uint32(err)
	}

	keys, err0 := database.KV().List(ctx, _key)
	if err0 != nil {
		return uint32(errno.ErrorDatabaseListFailed)
	}

	return uint32(f.WriteStringSlice(module, dataPtr, keys))
}

func (f *Factory) databaseListSize(ctx context.Context, module common.Module,
	databaseId uint32,
	keyPtr, keyLen,
	sizePtr uint32,
) uint32 {

	database, err := f.getDatabase(databaseId)
	if err != 0 {
		return uint32(err)
	}

	_key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return uint32(err)
	}

	keys, err0 := database.KV().List(ctx, _key)
	if err0 != nil {
		return uint32(errno.ErrorDatabaseListFailed)
	}

	return uint32(f.WriteStringSliceSize(module, sizePtr, keys))
}
