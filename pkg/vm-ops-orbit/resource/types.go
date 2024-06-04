package resource

import (
	"context"
	"sync"

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

type Factory struct {
	*database.Database
	*functionHttp.FunctionHttp
	*functionP2P.FunctionP2P
	*functionPubSub.FunctionPubSub
	*messagingPubSub.MessagingPubSub
	*messagingWebSocket.MessagingWebSocket
	*service.Service
	*storage.Storage
	*website.Website

	helpers.Methods
	parent vm.Instance
	ctx    context.Context

	resourceLock     sync.RWMutex
	resourceIdToGrab uint32
	resources        map[uint32]*common.Resource
}

var _ vm.Factory = &Factory{}

func (f *Factory) generateResourceId() uint32 {
	f.resourceLock.Lock()
	defer func() {
		f.resourceIdToGrab += 1
		f.resourceLock.Unlock()
	}()
	return f.resourceIdToGrab
}

func (f *Factory) Load(hm vm.HostModule) (err error) {
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
	return nil
}
