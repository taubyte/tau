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
	wazy.HostFunc2(b.NewFunctionBuilder(), f.databaseBatchNew).Export("databaseBatchNew")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.databaseBatchPut).Export("databaseBatchPut")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.databaseBatchDelete).Export("databaseBatchDelete")
	wazy.HostFunc1(b.NewFunctionBuilder(), f.databaseBatchCommit).Export("databaseBatchCommit")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.databaseListEntries).Export("databaseListEntries")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.databaseListEntriesSize).Export("databaseListEntriesSize")
	wazy.HostFunc6(b.NewFunctionBuilder(), f.databaseListMatch).Export("databaseListMatch")
	wazy.HostFunc6(b.NewFunctionBuilder(), f.databaseListMatchSize).Export("databaseListMatchSize")
	wazy.HostFunc7(b.NewFunctionBuilder(), f.databaseListPage).Export("databaseListPage")
	wazy.HostFunc7(b.NewFunctionBuilder(), f.databaseListPageSize).Export("databaseListPageSize")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.databaseCount).Export("databaseCount")
}
