package event

import (
	"context"

	wazy "github.com/samyfodil/wazy"
	common "github.com/taubyte/tau/core/vm"
)

// RegisterHostFunctions registers this factory's host functions on the wasm
// host-module builder.
func (f *Factory) RegisterHostFunctions(b wazy.HostModuleBuilder) {
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getHttpEventHostSize).Export("getHttpEventHostSize")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.getHttpEventHost).Export("getHttpEventHost")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getHttpEventUserAgentSize).Export("getHttpEventUserAgentSize")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.getHttpEventUserAgent).Export("getHttpEventUserAgent")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.readHttpEventBody).Export("readHttpEventBody")
	wazy.HostFunc1(b.NewFunctionBuilder(), f.closeHttpEventBody).Export("closeHttpEventBody")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.eventHttpWrite).Export("eventHttpWrite")
	wazy.HostFunc1(b.NewFunctionBuilder(), f.eventHttpFlush).Export("eventHttpFlush")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.eventHttpRetCode).Export("eventHttpRetCode")
	wazy.HostProc2(b.NewFunctionBuilder(), f.getEventType).Export("getEventType")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getHttpEventRequestHeaderKeysSize).Export("getHttpEventRequestHeaderKeysSize")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.getHttpEventRequestHeaderKeys).Export("getHttpEventRequestHeaderKeys")
	// Names go-sdk <= v0.3.9 imports; its keys call has no index argument.
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getHttpEventRequestHeaderKeysSize).Export("getHttpEventHeaderVarNameByIdSize")
	wazy.HostFunc2(b.NewFunctionBuilder(), func(ctx context.Context, m common.Module, id, bufPtr uint32) uint32 {
		return f.getHttpEventRequestHeaderKeys(ctx, m, id, bufPtr, 0)
	}).Export("getHttpEventHeaderVarNameById")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.eventHttpHeaderAdd).Export("eventHttpHeaderAdd")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.eventHttpHeaderSet).Export("eventHttpHeaderSet")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.eventHttpHeaderDel).Export("eventHttpHeaderDel")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.getHttpEventHeadersSize).Export("getHttpEventHeadersSize")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.getHttpEventHeaders).Export("getHttpEventHeaders")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getHttpEventMethodSize).Export("getHttpEventMethodSize")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.getHttpEventMethod).Export("getHttpEventMethod")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getP2PEventCommand).Export("getP2PEventCommand")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getP2PEventCommandSize).Export("getP2PEventCommandSize")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.hostGetP2PEventData).Export("getP2PEventData")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.hostGetP2PEventDataSize).Export("getP2PEventDataSize")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getP2PEventProtocol).Export("getP2PEventProtocol")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getP2PEventProtocolSize).Export("getP2PEventProtocolSize")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getP2PEventTo).Export("getP2PEventTo")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getP2PEventFrom).Export("getP2PEventFrom")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.writeP2PResponse).Export("writeP2PResponse")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getHttpEventPathSize).Export("getHttpEventPathSize")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.getHttpEventPath).Export("getHttpEventPath")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getMessageData).Export("getMessageData")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getMessageDataSize).Export("getMessageDataSize")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getMessageChannel).Export("getMessageChannel")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getMessageChannelSize).Export("getMessageChannelSize")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.getHttpEventQueryValueByNameSize).Export("getHttpEventQueryValueByNameSize")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.getHttpEventQueryValueByName).Export("getHttpEventQueryValueByName")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getHttpEventRequestQueryKeysSize).Export("getHttpEventRequestQueryKeysSize")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.getHttpEventRequestQueryKeys).Export("getHttpEventRequestQueryKeys")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.eventHttpRedirect).Export("eventHttpRedirect")
}
