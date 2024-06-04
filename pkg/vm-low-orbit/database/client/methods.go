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

func (f *Factory) W_databaseGet(ctx context.Context, module common.Module,
	databaseId,
	keyPtr, keyLen,
	dataPtr uint32,
) errno.Error {

	database, err := f.getDatabase(databaseId)
	if err != 0 {
		return err
	}

	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return err
	}

	data, err := database.Get(ctx, key)
	if err != 0 {
		return err
	}

	return f.WriteBytes(module, dataPtr, data)
}

func (f *Factory) W_databaseGetSize(ctx context.Context, module common.Module,
	keystoreId,
	keyPtr, keyLen, // key string
	sizePtr uint32,
) errno.Error {

	database, err := f.getDatabase(keystoreId)
	if err != 0 {
		return err
	}

	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return err
	}

	data, err := database.Get(ctx, key)
	if err != 0 {
		return err
	}

	return f.WriteBytesSize(module, sizePtr, data)
}

func (f *Factory) W_databasePut(ctx context.Context, module common.Module,
	databaseId,
	keyPtr, keyLen,
	bufPtr, bufSize uint32,
) errno.Error {

	database, err := f.getDatabase(databaseId)
	if err != 0 {
		return err
	}

	_key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return err
	}

	data, err := f.ReadBytes(module, bufPtr, bufSize)
	if err != 0 {
		return err
	}

	_err := database.KV().Put(ctx, _key, data)
	if _err != nil {
		return errno.ErrorDatabasePutFailed
	}

	return 0
}

func (f *Factory) W_databaseClose(ctx context.Context, module common.Module,
	databaseId uint32,
) errno.Error {
	database, err := f.getDatabase(databaseId)
	if err != 0 {
		return err
	}

	f.databaseLock.Lock()
	defer f.databaseLock.Unlock()
	delete(f.database, databaseId)

	database.Close()
	return 0
}

func (f *Factory) W_databaseDelete(ctx context.Context, module common.Module,
	databaseId,
	keyPtr, keyLen uint32,
) (err errno.Error) {

	database, err := f.getDatabase(databaseId)
	if err != 0 {
		return err
	}

	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return
	}

	_err := database.KV().Delete(ctx, key)
	if _err != nil {
		return errno.ErrorDatabaseDeleteFailed
	}

	return 0
}

func (f *Factory) W_databaseList(ctx context.Context, module common.Module,
	databaseId,
	keyPtr, keyLen,
	dataPtr uint32,
) errno.Error {

	database, err := f.getDatabase(databaseId)
	if err != 0 {
		return err
	}

	_key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return err
	}

	keys, err0 := database.KV().List(ctx, _key)
	if err0 != nil {
		return errno.ErrorDatabaseListFailed
	}

	return f.WriteStringSlice(module, dataPtr, keys)
}

func (f *Factory) W_databaseListSize(ctx context.Context, module common.Module,
	databaseId uint32,
	keyPtr, keyLen,
	sizePtr uint32,
) errno.Error {

	database, err := f.getDatabase(databaseId)
	if err != 0 {
		return err
	}

	_key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return err
	}

	keys, err0 := database.KV().List(ctx, _key)
	if err0 != nil {
		return errno.ErrorDatabaseListFailed
	}

	return f.WriteStringSliceSize(module, sizePtr, keys)
}
