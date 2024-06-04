package client

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/memory"
)

func (f *Factory) W_setHttpRequestBody(ctx context.Context, module common.Module,
	clientId, requestId,
	bodyPtr, bodySize uint32,
) (err errno.Error) {
	_, req, err := f.getClientAndRequest(clientId, requestId)
	if err != 0 {
		return
	}

	req.Body = memory.New(f.ctx, module.Memory(), bodyPtr, bodySize)

	return 0
}
