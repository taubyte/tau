package resource

import (
	"github.com/taubyte/tau/core/services/substrate/smartops"
	"github.com/taubyte/tau/pkg/vm-ops-orbit/common"
)

func (f *Factory) CreateSmartOp(caller smartops.EventCaller) *common.Resource {
	r := &common.Resource{
		Id:     f.generateResourceId(),
		Caller: caller,
	}

	f.resourceLock.Lock()
	defer f.resourceLock.Unlock()
	f.resources[r.Id] = r
	return r
}
