package messaging

import wazy "github.com/samyfodil/wazy"

// RegisterHostFunctions registers this messaging's host functions on the wasm
// host-module builder.
func (f *MessagingPubSub) RegisterHostFunctions(b wazy.HostModuleBuilder) {
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getMessagingPubSubName).Export("getMessagingPubSubName")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getMessagingPubSubNameSize).Export("getMessagingPubSubNameSize")
}
