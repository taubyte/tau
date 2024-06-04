package website

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/tau/core/vm"
)

func (d *Website) W_getWebsiteName(ctx context.Context, module vm.Module, resourceId uint32, dataPtr uint32) errno.Error {
	website, err := d.GetCaller(resourceId)
	if err != 0 {
		return err
	}

	return d.WriteString(module, dataPtr, website.Config().Name)
}

func (d *Website) W_getWebsiteNameSize(ctx context.Context, module vm.Module, resourceId uint32, sizePtr uint32) errno.Error {
	website, err := d.GetCaller(resourceId)
	if err != 0 {
		return err
	}

	return d.WriteStringSize(module, sizePtr, website.Config().Name)
}
