//go:build web3
// +build web3

package client

import wazy "github.com/samyfodil/wazy"

// RegisterHostFunctions registers this factory's host functions on the wasm
// host-module builder.
func (f *Factory) RegisterHostFunctions(b wazy.HostModuleBuilder) {
	wazy.HostFunc2(b.NewFunctionBuilder(), f.ipfsNewContent).Export("ipfsNewContent")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.ipfsOpenFile).Export("ipfsOpenFile")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.ipfsCloseFile).Export("ipfsCloseFile")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.ipfsFileCid).Export("ipfsFileCid")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.ipfsWriteFile).Export("ipfsWriteFile")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.ipfsReadFile).Export("ipfsReadFile")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.ipfsPushFile).Export("ipfsPushFile")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.ipfsSeekFile).Export("ipfsSeekFile")
	wazy.HostFunc1(b.NewFunctionBuilder(), f.newIpfsClient).Export("newIpfsClient")
}
