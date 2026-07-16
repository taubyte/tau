package database

import wazy "github.com/samyfodil/wazy"

// RegisterHostFunctions registers this database's host functions on the wasm
// host-module builder.
func (d *Database) RegisterHostFunctions(b wazy.HostModuleBuilder) {
	wazy.HostFunc2(b.NewFunctionBuilder(), d.getDatabaseName).Export("getDatabaseName")
	wazy.HostFunc2(b.NewFunctionBuilder(), d.getDatabaseNameSize).Export("getDatabaseNameSize")
}
