package client

import (
	"context"
	"fmt"
	"strings"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

// The guest only gets an error code: without this the cause is lost.
var logger = log.Logger("tau.substrate.database")

func (d *Database) Get(ctx context.Context, key string) (data []byte, err errno.Error) {
	var err0 error
	data, err0 = d.KV().Get(ctx, key)
	if err0 != nil {
		if strings.Contains(err0.Error(), keyNotFound) {
			err = errno.ErrorDatabaseKeyNotFound
		} else {
			logger.Errorf("get failed: %s", err0)
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

	if rc, ok := f.WriteReported(module, getKey(databaseId, key), dataPtr); ok {
		return uint32(rc)
	}

	data, err := database.Get(ctx, key)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteBytesFor(module, getKey(databaseId, key), dataPtr, data))
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

	return uint32(f.WriteBytesSizeFor(module, getKey(keystoreId, key), sizePtr, data))
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
		logger.Errorf("put failed: %s", _err)
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

	f.releaseDatabase(database)
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

	if rc, ok := f.WriteReported(module, listKey(databaseId, _key), dataPtr); ok {
		return uint32(rc)
	}

	keys, err0 := database.KV().List(ctx, _key)
	if err0 != nil {
		return uint32(errno.ErrorDatabaseListFailed)
	}

	return uint32(f.WriteStringSliceFor(module, listKey(databaseId, _key), dataPtr, keys))
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

	return uint32(f.WriteStringSliceSizeFor(module, listKey(databaseId, _key), sizePtr, keys))
}

// Keys naming a value across its size and data steps; see helpers/sized.go.
func getKey(id uint32, key string) string  { return fmt.Sprintf("database.get/%d/%s", id, key) }
func listKey(id uint32, key string) string { return fmt.Sprintf("database.list/%d/%s", id, key) }

// keyNotFound ends the error of a missing key from the local kvdb
// (datastore.ErrNotFound) and from the hoarder client and the kvdb mock, whose
// errors are only "key not found".
const keyNotFound = "key not found"
