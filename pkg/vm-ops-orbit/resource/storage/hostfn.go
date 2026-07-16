package storage

import wazy "github.com/samyfodil/wazy"

// RegisterHostFunctions registers this storage's host functions on the wasm
// host-module builder.
func (d *Storage) RegisterHostFunctions(b wazy.HostModuleBuilder) {
	wazy.HostFunc2(b.NewFunctionBuilder(), d.getStorageName).Export("getStorageName")
	wazy.HostFunc2(b.NewFunctionBuilder(), d.getStorageNameSize).Export("getStorageNameSize")
}
