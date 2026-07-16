package client

import wazy "github.com/samyfodil/wazy"

// RegisterHostFunctions registers this factory's host functions on the wasm
// host-module builder.
func (f *Factory) RegisterHostFunctions(b wazy.HostModuleBuilder) {
	wazy.HostFunc1(b.NewFunctionBuilder(), f.newHttpClient).Export("newHttpClient")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.setHttpRequestBody).Export("setHttpRequestBody")
	wazy.HostFunc6(b.NewFunctionBuilder(), f.setHttpRequestHeader).Export("setHttpRequestHeader")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.deleteHttpRequestHeader).Export("deleteHttpRequestHeader")
	wazy.HostFunc6(b.NewFunctionBuilder(), f.addHttpRequestHeader).Export("addHttpRequestHeader")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.getHttpRequestHeaderSize).Export("getHttpRequestHeaderSize")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.getHttpRequestHeader).Export("getHttpRequestHeader")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.getHttpRequestHeaderKeysSize).Export("getHttpRequestHeaderKeysSize")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.getHttpRequestHeaderKeys).Export("getHttpRequestHeaderKeys")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.newHttpRequest).Export("newHttpRequest")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.setHttpRequestURL).Export("setHttpRequestURL")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.doHttpRequest).Export("doHttpRequest")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.readHttpResponseBody).Export("readHttpResponseBody")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.closeHttpResponseBody).Export("closeHttpResponseBody")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.getHttpResponseHeaderSize).Export("getHttpResponseHeaderSize")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.getHttpResponseHeader).Export("getHttpResponseHeader")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.getHttpResponseHeaderKeysSize).Export("getHttpResponseHeaderKeysSize")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.getHttpResponseHeaderKeys).Export("getHttpResponseHeaderKeys")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.setHttpRequestMethod).Export("setHttpRequestMethod")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.getHttpRequestMethod).Export("getHttpRequestMethod")
}
