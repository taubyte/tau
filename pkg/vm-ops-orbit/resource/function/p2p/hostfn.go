package function

import wazy "github.com/samyfodil/wazy"

// RegisterHostFunctions registers this function's host functions on the wasm
// host-module builder.
func (f *FunctionP2P) RegisterHostFunctions(b wazy.HostModuleBuilder) {
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getFunctionP2PName).Export("getFunctionP2PName")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getFunctionP2PNameSize).Export("getFunctionP2PNameSize")
}
