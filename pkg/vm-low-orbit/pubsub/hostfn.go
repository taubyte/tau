package pubsub

import wazy "github.com/samyfodil/wazy"

// RegisterHostFunctions registers this factory's host functions on the wasm
// host-module builder.
func (f *Factory) RegisterHostFunctions(b wazy.HostModuleBuilder) {
	wazy.HostFunc4(b.NewFunctionBuilder(), f.publishToChannel).Export("publishToChannel")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.setSubscriptionChannel).Export("setSubscriptionChannel")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.getWebSocketURLSize).Export("getWebSocketURLSize")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.getWebSocketURL).Export("getWebSocketURL")
}
