//go:build web3
// +build web3

package ethereum

import (
	"context"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	ethCommon "github.com/ethereum/go-ethereum/common"
	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/go-sdk/ethereum/client/codec"
	"github.com/taubyte/go-sdk/utils/booleans"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) ethGetContractMethodSize(
	ctx context.Context,
	module common.Module,
	clientId,
	contractId,
	methodPtr,
	methodSize,
	inputSizePtr,
	outputSizePtr uint32,
) uint32 {
	client, err := f.getClient(clientId)
	if err != 0 {
		return uint32(err)
	}

	contract, err := client.getContract(contractId)
	if err != 0 {
		return uint32(err)
	}

	method, err := f.ReadString(module, methodPtr, methodSize)
	if err != 0 {
		return uint32(err)
	}

	contractMethod, ok := contract.methods[method]
	if !ok {
		return uint32(errno.ErrorEthereumContractMethodNotFound)
	}

	if err := f.WriteStringSliceSize(module, inputSizePtr, contractMethod.inputs); err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteStringSliceSize(module, outputSizePtr, contractMethod.outputs))
}

func (f *Factory) ethTransactContract(
	ctx context.Context,
	module common.Module,
	clientId,
	contractId,
	chainIdPtr,
	chainIdSize,
	methodPtr,
	methodLen,
	privKeyPtr,
	privKeySize,
	inputsPtr,
	inputsSize,
	isJSON,
	transactionIdPtr uint32,
) uint32 {
	client, err0 := f.getClient(clientId)
	if err0 != 0 {
		return uint32(err0)
	}

	chainId, err0 := f.ReadBigInt(module, chainIdPtr, chainIdSize)
	if err0 != 0 {
		return uint32(err0)
	}

	contract, err0 := client.getContract(contractId)
	if err0 != 0 {
		return uint32(err0)
	}

	methodName, err0 := f.ReadString(module, methodPtr, methodLen)
	if err0 != 0 {
		return uint32(err0)
	}

	method, ok := contract.methods[methodName]
	if !ok {
		return uint32(errno.ErrorEthereumContractMethodNotFound)
	}
	if method.constant {
		return uint32(errno.ErrorEthereumCannotTransactFreeMethod)
	}

	var inputs []interface{}
	if booleans.ToBool(isJSON) {
		inputsJSON, err0 := f.ReadBytes(module, inputsPtr, inputsSize)
		if err0 != 0 {
			return uint32(err0)
		}

		inputs, err0 = contract.inputsFromJSON(inputsJSON, methodName, methodInputs)
		if err0 != 0 {
			return uint32(err0)
		}
	} else {
		inputsBytes, err0 := f.ReadBytesSlice(module, inputsPtr, inputsSize)
		if err0 != 0 {
			return uint32(err0)
		}

		inputs, err0 = verifyInputs(inputsBytes, method)
		if err0 != 0 {
			return uint32(err0)
		}
	}

	privateKey, err0 := f.toEcdsa(module, privKeyPtr, privKeySize)
	if err0 != 0 {
		return uint32(err0)
	}

	auth, err := bind.NewKeyedTransactorWithChainID(privateKey, chainId)
	if err != nil {
		return uint32(errno.ErrorEthereumBindTransactorFailed)
	}

	transaction, err := contract.Transact(auth, methodName, inputs...)
	if err != nil {
		return uint32(errno.ErrorEthereumTransactMethodFailed)
	}

	tx := &Transaction{
		Transaction: transaction,
		Id:          contract.generateTransactionId(),
	}

	if err0 := f.WriteUint32Le(module, transactionIdPtr, tx.Id); err0 != 0 {
		return uint32(err0)
	}

	contract.transactionsLock.Lock()
	contract.transactions[tx.Id] = tx
	contract.transactionsLock.Unlock()

	return 0
}

func (f *Factory) ethCallContractSize(
	ctx context.Context,
	module common.Module,
	clientId,
	contractId,
	methodPtr,
	methodSize,
	inputsPtr,
	inputsSize,
	isJSON,
	outPutSizePtr uint32,
) uint32 {
	client, err0 := f.getClient(clientId)
	if err0 != 0 {
		return uint32(err0)
	}

	contract, err0 := client.getContract(contractId)
	if err0 != 0 {
		return uint32(err0)
	}

	methodName, err0 := f.ReadString(module, methodPtr, methodSize)
	if err0 != 0 {
		return uint32(err0)
	}

	method, ok := contract.methods[methodName]
	if !ok {
		return uint32(errno.ErrorEthereumContractMethodNotFound)
	}

	if !method.constant {
		return uint32(errno.ErrorEthereumCannotCallPaidMutatorTransaction)
	}

	var inputs []interface{}
	if booleans.ToBool(isJSON) {
		inputsJSON, err0 := f.ReadBytes(module, inputsPtr, inputsSize)
		if err0 != 0 {
			return uint32(err0)
		}

		inputs, err0 = contract.inputsFromJSON(inputsJSON, methodName, methodInputs)
		if err0 != 0 {
			return uint32(err0)
		}
	} else {
		inputsBytes, err0 := f.ReadBytesSlice(module, inputsPtr, inputsSize)
		if err0 != 0 {
			return uint32(err0)
		}

		inputs, err0 = verifyInputs(inputsBytes, method)
		if err0 != 0 {
			return uint32(err0)
		}
	}

	results := make([]interface{}, 0)
	err := contract.Call(nil, &results, methodName, inputs...)
	if err != nil {
		return uint32(errno.ErrorEthereumCallContractFailed)
	}

	if len(results) != len(method.outputs) {
		return uint32(errno.ErrorEthereumInvalidContractMethodOutput)
	}

	var outputs [][]byte
	for idx, output := range results {
		outputType := method.outputs[idx]
		if outputType == "common.Address" {
			outputs = append(outputs, output.(ethCommon.Address).Bytes())
			continue
		}

		encoder, err := codec.Converter(outputType).Encoder()
		if err != nil {
			return uint32(errno.ErrorEthereumUnsupportedDataType)
		}

		value, err := encoder(output)
		if err != nil {
			return uint32(errno.ErrorEthereumParseOutputTypeFailed)
		}

		if len(value) == 0 {
			value = []byte{0}
		}

		outputs = append(outputs, value)
	}

	method.data = outputs

	return uint32(f.WriteBytesSliceSize(module, outPutSizePtr, outputs))
}

func (f *Factory) ethCallContract(
	ctx context.Context,
	module common.Module,
	clientId,
	contractId,
	methodPtr,
	methodSize,
	outputPtr uint32,
) uint32 {
	client, err0 := f.getClient(clientId)
	if err0 != 0 {
		return uint32(err0)
	}

	contract, err0 := client.getContract(contractId)
	if err0 != 0 {
		return uint32(err0)
	}

	methodString, err0 := f.ReadString(module, methodPtr, methodSize)
	if err0 != 0 {
		return uint32(err0)
	}

	method, ok := contract.methods[methodString]
	if !ok {
		return uint32(errno.ErrorEthereumContractMethodNotFound)
	}

	return uint32(f.WriteBytesSlice(module, outputPtr, method.data))
}

func (f *Factory) ethGetContractMethod(
	ctx context.Context,
	module common.Module,
	clientId,
	contractId,
	methodPtr,
	methodSize,
	inputPtr,
	outputPtr uint32,
) uint32 {
	client, err := f.getClient(clientId)
	if err != 0 {
		return uint32(err)
	}

	contract, err := client.getContract(contractId)
	if err != 0 {
		return uint32(err)
	}

	method, err := f.ReadString(module, methodPtr, methodSize)
	if err != 0 {
		return uint32(err)
	}

	contractMethod, ok := contract.methods[method]
	if !ok {
		return uint32(errno.ErrorEthereumContractMethodNotFound)
	}

	err = f.WriteStringSlice(module, inputPtr, contractMethod.inputs)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteStringSlice(module, outputPtr, contractMethod.outputs))
}
