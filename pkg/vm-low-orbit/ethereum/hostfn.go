//go:build web3

package ethereum

import (
	"context"

	wazy "github.com/samyfodil/wazy"
	wazyapi "github.com/samyfodil/wazy/api"
)

// RegisterHostFunctions registers this factory's host functions on the wasm
// host-module builder.
func (f *Factory) RegisterHostFunctions(b wazy.HostModuleBuilder) {
	// Block methods
	wazy.HostFunc4(b.NewFunctionBuilder(), f.ethBlockByNumber).Export("ethBlockByNumber")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.ethCurrentBlockNumber).Export("ethCurrentBlockNumber")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.ethBlockNumberFromIdSize).Export("ethBlockNumberFromIdSize")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.ethBlockNumberFromId).Export("ethBlockNumberFromId")

	// Chain methods
	wazy.HostFunc2(b.NewFunctionBuilder(), f.ethCurrentChainIdSize).Export("ethCurrentChainIdSize")
	wazy.HostFunc2(b.NewFunctionBuilder(), f.ethCurrentChainId).Export("ethCurrentChainId")

	// Client methods
	wazy.HostFunc5(b.NewFunctionBuilder(), f.ethNew).Export("ethNew")
	wazy.HostFunc1(b.NewFunctionBuilder(), f.ethCloseClient).Export("ethCloseClient")

	// Contract methods
	// ethDeployContract has 14 params, needs GoModuleFunc
	b.NewFunctionBuilder().WithGoModuleFunction(wazyapi.GoModuleFunc(func(ctx context.Context, m wazyapi.Module, s []uint64) {
		s[0] = uint64(f.ethDeployContract(ctx, m, uint32(s[0]), uint32(s[1]), uint32(s[2]), uint32(s[3]), uint32(s[4]), uint32(s[5]), uint32(s[6]), uint32(s[7]), uint32(s[8]), uint32(s[9]), uint32(s[10]), uint32(s[11]), uint32(s[12]), uint32(s[13])))
	}), []wazyapi.ValueType{wazyapi.ValueTypeI32, wazyapi.ValueTypeI32, wazyapi.ValueTypeI32, wazyapi.ValueTypeI32, wazyapi.ValueTypeI32, wazyapi.ValueTypeI32, wazyapi.ValueTypeI32, wazyapi.ValueTypeI32, wazyapi.ValueTypeI32, wazyapi.ValueTypeI32, wazyapi.ValueTypeI32, wazyapi.ValueTypeI32, wazyapi.ValueTypeI32, wazyapi.ValueTypeI32}, []wazyapi.ValueType{wazyapi.ValueTypeI32}).Export("ethDeployContract")

	wazy.HostFunc8(b.NewFunctionBuilder(), f.ethNewContractSize).Export("ethNewContractSize")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.ethNewContract).Export("ethNewContract")

	// Contract event methods
	wazy.HostFunc7(b.NewFunctionBuilder(), f.ethSubscribeContractEvent).Export("ethSubscribeContractEvent")

	// Contract method methods
	wazy.HostFunc6(b.NewFunctionBuilder(), f.ethGetContractMethodSize).Export("ethGetContractMethodSize")
	// ethTransactContract has 12 params, needs GoModuleFunc
	b.NewFunctionBuilder().WithGoModuleFunction(wazyapi.GoModuleFunc(func(ctx context.Context, m wazyapi.Module, s []uint64) {
		s[0] = uint64(f.ethTransactContract(ctx, m, uint32(s[0]), uint32(s[1]), uint32(s[2]), uint32(s[3]), uint32(s[4]), uint32(s[5]), uint32(s[6]), uint32(s[7]), uint32(s[8]), uint32(s[9]), uint32(s[10]), uint32(s[11])))
	}), []wazyapi.ValueType{wazyapi.ValueTypeI32, wazyapi.ValueTypeI32, wazyapi.ValueTypeI32, wazyapi.ValueTypeI32, wazyapi.ValueTypeI32, wazyapi.ValueTypeI32, wazyapi.ValueTypeI32, wazyapi.ValueTypeI32, wazyapi.ValueTypeI32, wazyapi.ValueTypeI32, wazyapi.ValueTypeI32, wazyapi.ValueTypeI32}, []wazyapi.ValueType{wazyapi.ValueTypeI32}).Export("ethTransactContract")

	wazy.HostFunc8(b.NewFunctionBuilder(), f.ethCallContractSize).Export("ethCallContractSize")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.ethCallContract).Export("ethCallContract")
	wazy.HostFunc6(b.NewFunctionBuilder(), f.ethGetContractMethod).Export("ethGetContractMethod")

	// ECDSA methods
	wazy.HostFunc5(b.NewFunctionBuilder(), f.ethPubKeyFromSignedMessage).Export("ethPubKeyFromSignedMessage")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.ethHexToECDSA).Export("ethHexToECDSA")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.ethPubFromPriv).Export("ethPubFromPriv")

	// Sign methods
	wazy.HostFunc5(b.NewFunctionBuilder(), f.ethSignMessage).Export("ethSignMessage")
	wazy.HostFunc6(b.NewFunctionBuilder(), f.ethVerifySignature).Export("ethVerifySignature")

	// Transaction methods
	wazy.HostFunc4(b.NewFunctionBuilder(), f.ethGetTransactionFromBlockByHash).Export("ethGetTransactionFromBlockByHash")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.ethGetTransactionsFromBlockSize).Export("ethGetTransactionsFromBlockSize")
	wazy.HostFunc3(b.NewFunctionBuilder(), f.ethGetTransactionsFromBlock).Export("ethGetTransactionsFromBlock")
	wazy.HostFunc7(b.NewFunctionBuilder(), f.ethGetTransactionMethodSize).Export("ethGetTransactionMethodSize")
	wazy.HostFunc7(b.NewFunctionBuilder(), f.ethGetTransactionMethodBytes).Export("ethGetTransactionMethodBytes")
	wazy.HostFunc7(b.NewFunctionBuilder(), f.ethGetTransactionMethodUint64).Export("ethGetTransactionMethodUint64")
	wazy.HostFunc7(b.NewFunctionBuilder(), f.ethTransactionRawSignaturesSize).Export("ethTransactionRawSignaturesSize")
	wazy.HostFunc7(b.NewFunctionBuilder(), f.ethTransactionRawSignatures).Export("ethTransactionRawSignatures")
	wazy.HostFunc4(b.NewFunctionBuilder(), f.ethSendTransaction).Export("ethSendTransaction")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.ethJsonSize).Export("ethJsonSize")
	wazy.HostFunc5(b.NewFunctionBuilder(), f.ethJson).Export("ethJson")
}
