package p2p

import wazy "github.com/samyfodil/wazy"

// RegisterHostFunctions registers this factory's host functions on the wasm
// host-module builder.
func (f *Factory) RegisterHostFunctions(b wazy.HostModuleBuilder) {
	wazy.HostFunc3(b.NewFunctionBuilder(), f.readCommandResponse).Export("readCommandResponse")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.discoverPeersSize).Export("discoverPeersSize")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.discoverPeers).Export("discoverPeers")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.sendCommand).Export("sendCommand")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.sendCommandTo).Export("sendCommandTo")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.newCommand).Export("newCommand")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.listenToProtocolSize).Export("listenToProtocolSize")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.listenToProtocol).Export("listenToProtocol")
}
