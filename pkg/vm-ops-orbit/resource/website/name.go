package website

import (
	"context"

	"github.com/taubyte/tau/core/vm"
)

func (d *Website) getWebsiteName(ctx context.Context, module vm.Module, resourceId uint32, dataPtr uint32) uint32 {
	website, err := d.GetCaller(resourceId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(d.WriteString(module, dataPtr, website.Config().Name))
}

func (d *Website) getWebsiteNameSize(ctx context.Context, module vm.Module, resourceId uint32, sizePtr uint32) uint32 {
	website, err := d.GetCaller(resourceId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(d.WriteStringSize(module, sizePtr, website.Config().Name))
}
