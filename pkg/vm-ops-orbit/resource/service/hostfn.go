package service

import wazy "github.com/samyfodil/wazy"

// RegisterHostFunctions registers this service's host functions on the wasm
// host-module builder.
func (f *Service) RegisterHostFunctions(b wazy.HostModuleBuilder) {
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getServiceName).Export("getServiceName")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getServiceNameSize).Export("getServiceNameSize")
}
