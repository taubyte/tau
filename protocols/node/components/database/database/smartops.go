package database

import (
	"context"

	"github.com/ipfs/go-cid"
	smartOps "github.com/taubyte/go-interfaces/services/substrate/smartops"
	sdkSmartOpsCommon "github.com/taubyte/go-sdk-smartops/common"
)

var _ smartOps.SmartOpEventCaller = &Database{}

const resourceType = sdkSmartOpsCommon.ResourceTypeDatabase

func (f *Database) Type() uint32 {
	return uint32(resourceType)
}

func (f *Database) Context() context.Context {
	return f.instanceCtx
}

func (f *Database) SmartOps() (uint32, error) {
	return f.srv.SmartOps().Run(f, f.dbContext.Config.SmartOps)
}

func (f *Database) Application() string {
	return f.dbContext.ApplicationId
}

func (f *Database) Project() (cid.Cid, error) {
	return cid.Decode(f.dbContext.ProjectId)
}
