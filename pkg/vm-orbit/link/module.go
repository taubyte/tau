package link

import (
	"context"
	"errors"

	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-orbit/proto"
)

func NewModule(mod vm.Module) proto.ModuleServer {
	return &module{module: mod}
}

func (m *module) MemoryRead(ctx context.Context, req *proto.ReadRequest) (*proto.ReadReturn, error) {
	data, ok := m.module.Memory().Read(req.Offset, req.Size)
	if !ok {
		return nil, errors.New("reading (vm) memory failed")
	}

	return &proto.ReadReturn{Data: data}, nil
}

func (m *module) MemoryWrite(ctx context.Context, req *proto.WriteRequest) (*proto.WriteReturn, error) {
	ok := m.module.Memory().Write(req.Offset, req.Data)
	if !ok {
		return nil, errors.New("writing to (vm) memory failed")
	}

	return &proto.WriteReturn{Written: uint32(len(req.Data))}, nil
}
