package client

import (
	"context"

	common "github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/memory"
)

func (f *Factory) setHttpRequestBody(ctx context.Context, module common.Module,
	clientId, requestId,
	bodyPtr, bodySize uint32,
) uint32 {
	_, req, err := f.getClientAndRequest(clientId, requestId)
	if err != 0 {
		return uint32(err)
	}

	req.Body = memory.New(f.ctx, module.Memory(), bodyPtr, bodySize)

	return uint32(0)
}
