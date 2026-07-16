package dns

import wazy "github.com/samyfodil/wazy"

// RegisterHostFunctions registers this factory's host functions on the wasm
// host-module builder.
func (f *Factory) RegisterHostFunctions(b wazy.HostModuleBuilder) {
	wazy.HostFunc4(b.NewFunctionBuilder(), f.dnsLookupTxTSize).Export("dnsLookupTxTSize")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.dnsLookupTxT).Export("dnsLookupTxT")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.dnsLookupAddressSize).Export("dnsLookupAddressSize")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.dnsLookupAddress).Export("dnsLookupAddress")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.dnsLookupCNAMESize).Export("dnsLookupCNAMESize")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.dnsLookupCNAME).Export("dnsLookupCNAME")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.dnsLookupMXSize).Export("dnsLookupMXSize")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.dnsLookupMX).Export("dnsLookupMX")
	wazy.HostFunc1(b.NewFunctionBuilder(), f.dnsNewResolver).Export("dnsNewResolver")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.dnsRerouteResolver).Export("dnsRerouteResolver")
	wazy.HostFunc1(b.NewFunctionBuilder(), f.dnsResetResolver).Export("dnsResetResolver")
}
