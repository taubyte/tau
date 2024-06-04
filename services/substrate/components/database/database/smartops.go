package database

import (
	"context"

	sdkSmartOpsCommon "github.com/taubyte/go-sdk-smartops/common"
	"github.com/taubyte/tau/core/services/substrate/smartops"
)

var _ smartops.EventCaller = &Database{}

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

func (f *Database) Project() string {
	return f.dbContext.ProjectId
}
