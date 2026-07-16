package globals

import wazy "github.com/samyfodil/wazy"

// RegisterHostFunctions registers this factory's host functions on the wasm
// host-module builder.
func (f *Factory) RegisterHostFunctions(b wazy.HostModuleBuilder) {
	wazy.HostFunc5(b.NewFunctionBuilder(), f.getGlobalValueSize).Export("getGlobalValueSize")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.getGlobalValue).Export("getGlobalValue")
	wazy.HostFunc7(b.NewFunctionBuilder(), f.putGlobalValue).Export("putGlobalValue")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.getOrCreateGlobalValueSize).Export("getOrCreateGlobalValueSize")
}
