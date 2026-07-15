package function

import wazy "github.com/samyfodil/wazy"

// RegisterHostFunctions registers this function's host functions on the wasm
// host-module builder.
func (f *FunctionPubSub) RegisterHostFunctions(b wazy.HostModuleBuilder) {
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getFunctionPubSubName).Export("getFunctionPubSubName")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getFunctionPubSubNameSize).Export("getFunctionPubSubNameSize")
}
