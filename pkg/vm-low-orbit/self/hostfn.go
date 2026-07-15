package self

import wazy "github.com/samyfodil/wazy"

// RegisterHostFunctions registers this factory's host functions on the wasm
// host-module builder.
func (f *Factory) RegisterHostFunctions(b wazy.HostModuleBuilder) {
	wazy.HostFunc1(b.NewFunctionBuilder(), f.selfApplicationSize).Export("selfApplicationSize")
	wazy.HostFunc1(b.NewFunctionBuilder(), f.selfApplication).Export("selfApplication")
	wazy.HostFunc1(b.NewFunctionBuilder(), f.selfProjectSize).Export("selfProjectSize")
	wazy.HostFunc1(b.NewFunctionBuilder(), f.selfProject).Export("selfProject")
	wazy.HostFunc1(b.NewFunctionBuilder(), f.selfIdSize).Export("selfIdSize")
	wazy.HostFunc1(b.NewFunctionBuilder(), f.selfId).Export("selfId")
	wazy.HostFunc1(b.NewFunctionBuilder(), f.selfBranchesSize).Export("selfBranchesSize")
	wazy.HostFunc1(b.NewFunctionBuilder(), f.selfBranches).Export("selfBranches")
	wazy.HostFunc1(b.NewFunctionBuilder(), f.selfCommitSize).Export("selfCommitSize")
	wazy.HostFunc1(b.NewFunctionBuilder(), f.selfCommit).Export("selfCommit")
}
