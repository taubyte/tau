package vm

import (
	"context"
	"io"
	"sync"

	"github.com/hashicorp/go-plugin"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-orbit/proto"
)

type pluginInstance struct {
	plugin    *vmPlugin
	instance  vm.Instance
	satellite Satellite
}

type vmPlugin struct {
	proc   *plugin.Client
	client plugin.ClientProtocol

	filename string
	origin   string
	name     string

	lock sync.RWMutex

	instances map[*pluginInstance]interface{}

	ctx  context.Context
	ctxC context.CancelFunc
}

type Satellite interface {
	io.Closer

	Meta(context.Context) (*proto.Metadata, error)
	Symbols(context.Context) ([]vm.FunctionDefinition, error)
	Call(ctx context.Context, module vm.Module, function string, inputs []uint64) ([]uint64, error)
	Close() error
}
