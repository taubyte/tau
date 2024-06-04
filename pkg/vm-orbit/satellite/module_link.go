package satellite

import (
	"context"
	"fmt"

	"github.com/taubyte/tau/pkg/vm-orbit/proto"
	"google.golang.org/grpc"
)

func NewModuleLink(ctx context.Context, conn *grpc.ClientConn) Module {
	return &moduleLink{ctx: ctx, client: proto.NewModuleClient(conn)}
}

func (p *moduleLink) MemoryRead(offset uint32, size uint32) ([]byte, error) {
	ret, err := p.client.MemoryRead(p.ctx, &proto.ReadRequest{Offset: offset, Size: size})
	if err != nil {
		return nil, fmt.Errorf("reading from (client) memory failed with: %w", err)
	}

	if ret.Error != proto.IOError_none && ret.Error != proto.IOError_eof {
		return nil, fmt.Errorf("reading from (client) memory failed with: %w", ret.Error.Error())
	}

	return ret.Data, ret.Error.Error()
}

func (p *moduleLink) MemoryWrite(offset uint32, data []byte) (uint32, error) {
	ret, err := p.client.MemoryWrite(p.ctx, &proto.WriteRequest{Offset: offset, Data: data})
	if err != nil {
		return 0, fmt.Errorf("writing to (client) memory failed with: %w", err)
	}

	if ret.Error != proto.IOError_none && ret.Error != proto.IOError_eof {
		return 0, fmt.Errorf("writing to (client) memory failed with: %w", ret.Error.Error())
	}

	return uint32(len(data)), ret.Error.Error()
}
