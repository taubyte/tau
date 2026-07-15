package memoryView

import wazy "github.com/samyfodil/wazy"

// RegisterHostFunctions registers this factory's host functions on the wasm
// host-module builder.
func (f *Factory) RegisterHostFunctions(b wazy.HostModuleBuilder) {
	wazy.HostFunc4(b.NewFunctionBuilder(), f.memoryViewNew).Export("memoryViewNew")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.memoryViewOpen).Export("memoryViewOpen")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.memoryViewRead).Export("memoryViewRead")
	wazy.HostProc1(b.NewFunctionBuilder(), f.memoryViewClose).Export("memoryViewClose")
}
