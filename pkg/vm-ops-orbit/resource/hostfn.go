package resource

import (
	wazy "github.com/samyfodil/wazy"
)

// RegisterHostFunctions registers this factory's own host functions plus those
// of every embedded resource sub-factory.
func (f *Factory) RegisterHostFunctions(b wazy.HostModuleBuilder) {
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getEventId).Export("getEventId")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getResourceType).Export("getResourceType")

	f.Database.RegisterHostFunctions(b)
	f.Storage.RegisterHostFunctions(b)
	f.Website.RegisterHostFunctions(b)
	f.Service.RegisterHostFunctions(b)
	f.FunctionHttp.RegisterHostFunctions(b)
	f.FunctionP2P.RegisterHostFunctions(b)
	f.FunctionPubSub.RegisterHostFunctions(b)
	f.MessagingPubSub.RegisterHostFunctions(b)
	f.MessagingWebSocket.RegisterHostFunctions(b)
}
