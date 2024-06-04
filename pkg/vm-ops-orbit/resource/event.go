package resource

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/event"
	plCommon "github.com/taubyte/tau/pkg/vm-ops-orbit/common"
)

func (f *Factory) GetResource(resourceId uint32) (*plCommon.Resource, errno.Error) {
	f.resourceLock.RLock()
	defer f.resourceLock.RUnlock()
	if e, exists := f.resources[resourceId]; exists {
		return e, 0
	}
	return nil, errno.SmartOpErrorResourceNotFound
}

// TODO: FIXME this breaks smartops
func (f *Factory) GetEvent(resourceId uint32) (*event.Event, errno.Error) {
	// resource, err := f.GetResource(resourceId)
	// if err != 0 {
	// 	return nil, err
	// }

	// e, ok := resource.Caller.Event().(*event.Event)
	// if !ok {
	return nil, errno.ErrorEventNotFound
	// }

	// return e, 0
}

func (f *Factory) W_getEventId(ctx context.Context, module vm.Module, resourceId uint32, eventIdPtr uint32) errno.Error {
	event, err := f.GetEvent(resourceId)
	if err != 0 {
		return err
	}

	return f.WriteUint32Le(module, eventIdPtr, event.Id)
}

func (f *Factory) W_getResourceType(ctx context.Context, module vm.Module, resourceId uint32, typePtr uint32) errno.Error {
	resource, err := f.GetResource(resourceId)
	if err != 0 {
		return err
	}

	return f.WriteUint32Le(module, typePtr, resource.Caller.Type())
}
