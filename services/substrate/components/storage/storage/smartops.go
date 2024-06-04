package storage

import (
	"context"

	sdkSmartOpsCommon "github.com/taubyte/go-sdk-smartops/common"
	"github.com/taubyte/tau/core/services/substrate/smartops"
)

var _ smartops.EventCaller = &Store{}

const resourceType = sdkSmartOpsCommon.ResourceTypeStorage

func (s *Store) Type() uint32 {
	return uint32(resourceType)
}

func (s *Store) Context() context.Context {
	return s.instanceCtx
}

func (s *Store) Event() interface{} {
	return nil
}

func (s *Store) SmartOps() (uint32, error) {
	return s.srv.SmartOps().Run(s, s.context.Config.SmartOps)
}

func (s *Store) Application() string {
	return s.context.ApplicationId
}

func (s *Store) Project() string {
	return s.context.ProjectId
}
