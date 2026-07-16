package website

import wazy "github.com/samyfodil/wazy"

// RegisterHostFunctions registers this website's host functions on the wasm
// host-module builder.
func (d *Website) RegisterHostFunctions(b wazy.HostModuleBuilder) {
	wazy.HostFunc2(b.NewFunctionBuilder(), d.getWebsiteName).Export("getWebsiteName")
	wazy.HostFunc2(b.NewFunctionBuilder(), d.getWebsiteNameSize).Export("getWebsiteNameSize")
}
