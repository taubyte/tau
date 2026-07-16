package fifo

import wazy "github.com/samyfodil/wazy"

// RegisterHostFunctions registers this factory's host functions on the wasm
// host-module builder.
func (f *Factory) RegisterHostFunctions(b wazy.HostModuleBuilder) {
	wazy.HostFunc1(b.NewFunctionBuilder(), f.fifoNew).Export("fifoNew")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.fifoPush).Export("fifoPush")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.fifoPop).Export("fifoPop")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.fifoIsCloser).Export("fifoIsCloser")
	wazy.HostProc1(b.NewFunctionBuilder(), f.fifoClose).Export("fifoClose")
}
