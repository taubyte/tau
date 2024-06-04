package ethereum

import (
	"bytes"
	"context"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	ethCommon "github.com/ethereum/go-ethereum/common"
	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) W_ethDeployContract(
	ctx context.Context,
	module common.Module,
	clientId,
	chainIdPtr, chainIdSize,
	binPtr, binLen,
	abiPtr, abiSize,
	privKeyPtr, privKeySize,
	addressPtr,
	methodsSizePtr,
	eventsSizePtr,
	contractIdPtr,
	transactionIdPtr uint32,
) errno.Error {
	client, err0 := f.getClient(clientId)
	if err0 != 0 {
		return err0
	}

	abiJson, err0 := f.ReadBytes(module, abiPtr, abiSize)
	if err0 != 0 {
		return err0
	}

	chainId, err0 := f.ReadBigInt(module, chainIdPtr, chainIdSize)
	if err0 != 0 {
		return err0
	}

	parsedAbi, err := abi.JSON(bytes.NewReader(abiJson))
	if err != nil {
		return errno.ErrorEthereumParsingAbiFailed
	}

	privateKey, err0 := f.toEcdsa(module, privKeyPtr, privKeySize)
	if err0 != 0 {
		return err0
	}

	bin, err0 := f.ReadString(module, binPtr, binLen)
	if err0 != 0 {
		return err0
	}

	auth, err := bind.NewKeyedTransactorWithChainID(privateKey, chainId)
	if err != nil {
		return errno.ErrorEthereumBindTransactorFailed
	}

	address, transaction, boundContract, err := bind.DeployContract(auth, parsedAbi, ethCommon.FromHex(bin), client)
	if err != nil {
		return errno.ErrorEthereumDeployFailed
	}

	contract, err0 := f.handleBoundContractSize(module, client, boundContract, parsedAbi, contractIdPtr, methodsSizePtr, eventsSizePtr)
	if err0 != 0 {
		return err0
	}

	tx := &Transaction{
		Transaction: transaction,
		Id:          contract.generateTransactionId(),
	}

	if err0 := f.WriteBytes(module, addressPtr, address[:]); err0 != 0 {
		return err0
	}

	if err0 := f.WriteUint32Le(module, transactionIdPtr, tx.Id); err0 != 0 {
		return err0
	}

	contract.transactionsLock.Lock()
	contract.transactions[tx.Id] = tx
	contract.transactionsLock.Unlock()

	return 0
}

func (f *Factory) W_ethNewContractSize(
	ctx context.Context,
	module common.Module,
	clientId,
	abiPtr,
	abiSize,
	addressPtr,
	addressLen,
	methodsSizePtr,
	eventsSizePtr,
	contractPtr uint32,
) errno.Error {
	client, err0 := f.getClient(clientId)
	if err0 != 0 {
		return err0
	}

	abiJson, err0 := f.ReadBytes(module, abiPtr, abiSize)
	if err0 != 0 {
		return err0
	}

	parsedAbi, err := abi.JSON(bytes.NewReader(abiJson))
	if err != nil {
		return errno.ErrorEthereumParsingAbiFailed
	}

	var contractAddress ethCommon.Address
	address, err0 := f.ReadString(module, addressPtr, addressLen)
	if err0 == 0 || len(address) != 0 {
		contractAddress = ethCommon.HexToAddress(address)
	}

	contract := bind.NewBoundContract(contractAddress, parsedAbi, client, client, client)
	_, err0 = f.handleBoundContractSize(module, client, contract, parsedAbi, contractPtr, methodsSizePtr, eventsSizePtr)

	return err0
}

func (f *Factory) W_ethNewContract(
	ctx context.Context,
	module common.Module,
	clientId,
	contractId,
	methodsPtr,
	eventsPtr uint32,
) errno.Error {
	client, err0 := f.getClient(clientId)
	if err0 != 0 {
		return err0
	}

	contract, err0 := client.getContract(contractId)
	if err0 != 0 {
		return err0
	}

	var methodList []string
	for method := range contract.methods {
		methodList = append(methodList, method)
	}

	var events []string
	contract.eventsLock.RLock()
	for event := range contract.events {
		events = append(events, event)
	}
	contract.eventsLock.RUnlock()

	if err0 := f.WriteStringSlice(module, eventsPtr, events); err0 != 0 {
		return err0
	}

	return f.WriteStringSlice(module, methodsPtr, methodList)
}
