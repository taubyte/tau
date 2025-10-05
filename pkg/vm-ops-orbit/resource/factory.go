package resource

import (
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
	"github.com/taubyte/tau/pkg/vm-ops-orbit/common"

	"github.com/taubyte/tau/pkg/vm-ops-orbit/resource/database"
	functionHttp "github.com/taubyte/tau/pkg/vm-ops-orbit/resource/function/http"
	functionP2P "github.com/taubyte/tau/pkg/vm-ops-orbit/resource/function/p2p"
	functionPubSub "github.com/taubyte/tau/pkg/vm-ops-orbit/resource/function/pubsub"
	messagingPubSub "github.com/taubyte/tau/pkg/vm-ops-orbit/resource/messaging/pubsub"
	messagingWebSocket "github.com/taubyte/tau/pkg/vm-ops-orbit/resource/messaging/websocket"
	"github.com/taubyte/tau/pkg/vm-ops-orbit/resource/service"
	"github.com/taubyte/tau/pkg/vm-ops-orbit/resource/storage"
	"github.com/taubyte/tau/pkg/vm-ops-orbit/resource/website"
)

var _ common.Factory = &Factory{}

func New(i vm.Instance, helper helpers.Methods) *Factory {
	f := &Factory{
		parent:    i,
		ctx:       i.Context().Context(),
		resources: make(map[uint32]*common.Resource),
		Methods:   helper,
	}

	f.resources = map[uint32]*common.Resource{}
	f.Database = database.New(f)
	f.FunctionHttp = functionHttp.New(f)
	f.FunctionP2P = functionP2P.New(f)
	f.FunctionPubSub = functionPubSub.New(f)
	f.MessagingPubSub = messagingPubSub.New(f)
	f.MessagingWebSocket = messagingWebSocket.New(f)
	f.Service = service.New(f)
	f.Storage = storage.New(f)
	f.Website = website.New(f)

	return f
}

func (f *Factory) Name() string {
	return "resource"
}

func (f *Factory) Close() error {
	f.resources = nil
	return nil
}
