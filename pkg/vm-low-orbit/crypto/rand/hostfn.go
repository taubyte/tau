package rand

import (
	wazy "github.com/samyfodil/wazy"
)

// RegisterHostFunctions registers this factory's host functions on the wasm
// host-module builder.
func (f *Factory) RegisterHostFunctions(b wazy.HostModuleBuilder) {
	wazy.HostFunc3(b.NewFunctionBuilder(), f.cryptoRead).Export("cryptoRead")
}
