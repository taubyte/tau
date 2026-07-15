package storage

import wazy "github.com/samyfodil/wazy"

// RegisterHostFunctions registers this factory's host functions on the wasm
// host-module builder.
func (f *Factory) RegisterHostFunctions(b wazy.HostModuleBuilder) {
	// storage management
	wazy.HostFunc3(b.NewFunctionBuilder(), f.storageNew).Export("storageNew")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.storageGet).Export("storageGet")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.storageListFilesSize).Export("storageListFilesSize")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.storageListFiles).Export("storageListFiles")

	// content operations
	wazy.HostFunc1(b.NewFunctionBuilder(), f.storageNewContent).Export("storageNewContent")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.storageOpenCid).Export("storageOpenCid")
	wazy.HostFunc1(b.NewFunctionBuilder(), f.contentCloseFile).Export("contentCloseFile")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.contentFileCid).Export("contentFileCid")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.contentWriteFile).Export("contentWriteFile")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.contentReadFile).Export("contentReadFile")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.contentPushFile).Export("contentPushFile")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.contentSeekFile).Export("contentSeekFile")

	// file operations
	wazy.HostFunc7(b.NewFunctionBuilder(), f.storageAddFile).Export("storageAddFile")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.storageGetFile).Export("storageGetFile")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.storageReadFile).Export("storageReadFile")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.storageCloseFile).Export("storageCloseFile")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.storageDeleteFile).Export("storageDeleteFile")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.storageListVersionsSize).Export("storageListVersionsSize")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.storageListVersions).Export("storageListVersions")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.storageCid).Export("storageCid")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.storageCurrentVersion).Export("storageCurrentVersion")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.storageCurrentVersionSize).Export("storageCurrentVersionSize")

	// capacity and usage
	wazy.HostFunc2(b.NewFunctionBuilder(), f.storageUsedSize).Export("storageUsedSize")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.storageUsed).Export("storageUsed")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.storageCapacitySize).Export("storageCapacitySize")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.storageCapacity).Export("storageCapacity")
}
