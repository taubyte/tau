package client

import wazy "github.com/samyfodil/wazy"

// RegisterHostFunctions registers this factory's host functions on the wasm
// host-module builder.
func (f *Factory) RegisterHostFunctions(b wazy.HostModuleBuilder) {
	wazy.HostFunc4(b.NewFunctionBuilder(), f.databaseGet).Export("databaseGet")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.databaseGetSize).Export("databaseGetSize")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.databasePut).Export("databasePut")
	wazy.HostFunc1(b.NewFunctionBuilder(), f.databaseClose).Export("databaseClose")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.databaseDelete).Export("databaseDelete")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.databaseList).Export("databaseList")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.databaseListSize).Export("databaseListSize")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.newDatabase).Export("newDatabase")
}
