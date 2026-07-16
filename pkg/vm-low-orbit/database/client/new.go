package client

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	databaseIface "github.com/taubyte/tau/core/services/substrate/components/database"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) newDatabase(ctx context.Context,
	module common.Module,
	databaseMatchPtr, databaseMatchSize,
	idPtr uint32,
) uint32 {

	databaseMatch, err := f.ReadString(module, databaseMatchPtr, databaseMatchSize)
	if err != 0 {
		return uint32(err)
	}

	_ctx := f.parent.Context()
	databaseContext := databaseIface.Context{
		ProjectId:     _ctx.Project(),
		ApplicationId: _ctx.Application(),
		Matcher:       databaseMatch,
	}

	_database, err0 := f.databaseNode.Database(databaseContext)
	if err0 != nil {
		return uint32(errno.ErrorDatabaseCreateFailed)
	}

	database := f.createDatabasePointer(_database)

	return uint32(f.WriteUint32Le(module, idPtr, uint32(database.Id)))
}
