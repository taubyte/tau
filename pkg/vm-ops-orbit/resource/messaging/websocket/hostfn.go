package messaging

import wazy "github.com/samyfodil/wazy"

// RegisterHostFunctions registers this messaging's host functions on the wasm
// host-module builder.
func (f *MessagingWebSocket) RegisterHostFunctions(b wazy.HostModuleBuilder) {
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getMessagingWebSocketName).Export("getMessagingWebSocketName")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getMessagingWebSocketNameSize).Export("getMessagingWebSocketNameSize")
}
