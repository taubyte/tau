package satellite

import (
	"context"

	"github.com/hashicorp/go-plugin"
	"github.com/taubyte/tau/pkg/vm-orbit/proto"
)

type satellite struct {
	plugin.NetRPCUnsupportedPlugin

	name    string
	exports map[string]interface{}
}

type GRPCPluginServer struct {
	broker *plugin.GRPCBroker
	proto.UnimplementedPluginServer

	satellite *satellite
}

type Module interface {
	MemoryRead(offset uint32, size uint32) ([]byte, error)
	MemoryWrite(offset uint32, data []byte) (n uint32, err error)
	helpers
}

type moduleLink struct {
	plugin.NetRPCUnsupportedPlugin
	ctx    context.Context
	client proto.ModuleClient
}

type helpers interface {
	ReadByte(ptr uint32) (byte, error)
	WriteByte(ptr uint32, val byte) (n uint32, err error)

	ReadUint16(ptr uint32) (uint16, error)
	WriteUint16(ptr uint32, val uint16) (n uint32, err error)

	ReadUint32(ptr uint32) (uint32, error)
	WriteUint32(ptr uint32, val uint32) (n uint32, err error)

	ReadUint64(ptr uint32) (uint64, error)
	WriteUint64(ptr uint32, val uint64) (n uint32, err error)

	ReadString(ptr uint32, size uint32) (string, error)
	WriteString(ptr uint32, val string) (n uint32, err error)
	WriteStringSize(sizePtr uint32, val string) (n uint32, err error)

	ReadStringSlice(ptr uint32, size uint32) ([]string, error)
	WriteStringSlice(ptr uint32, val []string) (n uint32, err error)
	WriteStringSliceSize(sizePtr uint32, val []string) (n uint32, err error)

	ReadBytesSlice(ptr uint32, size uint32) ([][]byte, error)
	WriteBytesSlice(ptr uint32, val [][]byte) (n uint32, err error)
	WriteBytesSliceSize(sizePtr uint32, val [][]byte) (n uint32, err error)
}
