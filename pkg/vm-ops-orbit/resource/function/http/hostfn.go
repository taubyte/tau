package function

import wazy "github.com/samyfodil/wazy"

// RegisterHostFunctions registers this function's host functions on the wasm
// host-module builder.
func (f *FunctionHttp) RegisterHostFunctions(b wazy.HostModuleBuilder) {
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getFunctionHttpName).Export("getFunctionHttpName")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getFunctionHttpNameSize).Export("getFunctionHttpNameSize")
}
