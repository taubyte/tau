package ethereum

import (
	"context"
	"reflect"

	ethCommon "github.com/ethereum/go-ethereum/common"
	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/go-sdk/ethereum/client/reflection"
	common "github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

func (b *Block) getTransaction(transactionId uint32) (*Transaction, errno.Error) {
	b.transactionsLock.RLock()
	defer b.transactionsLock.RUnlock()
	if transaction, ok := b.transactions[transactionId]; ok {
		return transaction, 0
	}

	return nil, errno.ErrorEthereumTransactionNotFound
}

func (c *Contract) getTransaction(transactionId uint32) (*Transaction, errno.Error) {
	c.transactionsLock.RLock()
	defer c.transactionsLock.RUnlock()
	if transaction, ok := c.transactions[transactionId]; ok {
		return transaction, 0
	}

	return nil, errno.ErrorEthereumTransactionNotFound
}

func validateBytesMethod(method string) (reflection.MethodDetail, errno.Error) {
	methodDetail, err := reflection.ReflectiveTransaction(method)
	if err != nil || !methodDetail.IsBytesMethod() {
		return methodDetail, errno.ErrorEthereumMethodNotSupported
	}

	return methodDetail, 0
}

func validateUint64Method(method string) (reflection.MethodDetail, errno.Error) {

	methodDetail, err := reflection.ReflectiveTransaction(method)
	if err != nil || !methodDetail.IsUint64Method() {
		return nil, errno.ErrorEthereumMethodNotSupported
	}

	return methodDetail, 0
}

type transactionValidatorMethod func(string) (reflection.MethodDetail, errno.Error)

func (t *Transaction) callTransactionMethod(method string, validator transactionValidatorMethod) (reflection.MethodDetail, interface{}, errno.Error) {
	methodDetail, err := validator(method)
	if err != 0 {
		return methodDetail, nil, err
	}

	rt := reflect.ValueOf(t)
	rm := rt.MethodByName(method)
	values := rm.Call(nil)
	if len(values) != 1 {
		if !values[1].IsNil() {
			return methodDetail, nil, methodDetail.Error()
		}
	}

	return methodDetail, values[0].Interface(), 0
}

func (f *Factory) getTransaction(module common.Module, clientId, blockIdPtr, contractId, transactionId uint32) (*Transaction, errno.Error) {
	client, err := f.getClient(clientId)
	if err != 0 {
		return nil, err
	}

	if contractId != 0 {
		contract, err := client.getContract(contractId)
		if err != 0 {
			return nil, err
		}

		return contract.getTransaction(transactionId)
	}

	if blockIdPtr != 0 {
		blockId, err := f.ReadUint64Le(module, blockIdPtr)
		if err != 0 {
			return nil, err
		}
		block, err := f.getBlock(clientId, blockId)
		if err != 0 {
			return nil, err
		}

		transaction, err := block.getTransaction(transactionId)
		if err != 0 {
			return nil, err
		}

		return transaction, 0
	}

	return nil, errno.ErrorEthereumTransactionNotFound
}

func (f *Factory) W_ethGetTransactionFromBlockByHash(
	ctx context.Context,
	module common.Module,
	clientId,
	blockIdPtr,
	idPtr,
	hashPtr uint32,
) errno.Error {
	blockId, err := f.ReadUint64Le(module, blockIdPtr)
	if err != 0 {
		return err
	}

	block, err := f.getBlock(clientId, blockId)
	if err != 0 {
		return err
	}

	// Hash always 32 bytes
	hashBytes, err0 := f.ReadBytes(module, hashPtr, ethCommon.HashLength)
	if err0 != 0 {
		return err0
	}

	transaction := block.Transaction(ethCommon.BytesToHash(hashBytes))
	id := uint32(transaction.Hash().Big().Uint64())

	block.transactionsLock.Lock()
	if _, ok := block.transactions[id]; !ok {
		t := &Transaction{
			Transaction: transaction,
			Id:          id,
		}
		block.transactions[t.Id] = t
	}
	block.transactionsLock.Unlock()

	return f.WriteUint32Le(module, idPtr, id)
}

func (f *Factory) W_ethGetTransactionsFromBlockSize(
	ctx context.Context,
	module common.Module,
	clientId,
	blockIdPtr,
	sizePtr, arrSizePtr uint32,
) errno.Error {
	blockId, err := f.ReadUint64Le(module, blockIdPtr)
	if err != 0 {
		return err
	}

	block, err := f.getBlock(clientId, blockId)
	if err != 0 {
		return err
	}

	var hashList []uint32
	block.transactionsLock.Lock()
	defer block.transactionsLock.Unlock()
	for _, transaction := range block.Transactions() {
		t := &Transaction{
			Transaction: transaction,
			Id:          uint32(transaction.Hash().Big().Uint64()),
		}
		block.transactions[t.Id] = t
		hashList = append(hashList, t.Id)
	}

	err = f.WriteUint32Le(module, arrSizePtr, uint32(len(hashList)))
	if err != 0 {
		return err
	}

	return f.WriteUint32SliceSize(module, sizePtr, hashList)
}

func (f *Factory) W_ethGetTransactionsFromBlock(
	ctx context.Context,
	module common.Module,
	clientId,
	blockIdPtr,
	bufPtr uint32,
) errno.Error {
	blockId, err := f.ReadUint64Le(module, blockIdPtr)
	if err != 0 {
		return err
	}

	block, err := f.getBlock(clientId, blockId)
	if err != 0 {
		return err
	}

	var hashList []uint32
	for hash := range block.transactions {
		hashList = append(hashList, hash)
	}

	return f.WriteUint32Slice(module, bufPtr, hashList)
}

func (f *Factory) W_ethGetTransactionMethodSize(
	ctx context.Context,
	module common.Module,
	clientId,
	blockId,
	contractId,
	transactionId,
	methodPtr,
	methodLen,
	sizePtr uint32,
) errno.Error {
	method, err0 := f.ReadString(module, methodPtr, methodLen)
	if err0 != 0 {
		return err0
	}

	transaction, err0 := f.getTransaction(module, clientId, blockId, contractId, transactionId)
	if err0 != 0 {
		return err0
	}

	methodDetail, valueIface, err0 := transaction.callTransactionMethod(method, validateBytesMethod)
	if err0 != 0 {
		return err0
	}

	switch methodDetail.Type() {
	case reflection.ByteConvertibleMethod, reflection.BigIntMethod:
		return f.WriteBytesConvertibleInterfaceSize(module, sizePtr, valueIface)
	case reflection.BytesMethod:
		return f.WriteBytesInterfaceSize(module, sizePtr, valueIface)
	}

	return 0
}

func (f *Factory) W_ethGetTransactionMethodBytes(
	ctx context.Context,
	module common.Module,
	clientId,
	blockId,
	contractId,
	transactionId,
	methodPtr,
	methodLen,
	bufPtr uint32,
) errno.Error {
	method, err0 := f.ReadString(module, methodPtr, methodLen)
	if err0 != 0 {
		return err0
	}

	transaction, err0 := f.getTransaction(module, clientId, blockId, contractId, transactionId)
	if err0 != 0 {
		return err0
	}

	methodDetail, valueIface, err0 := transaction.callTransactionMethod(method, validateBytesMethod)
	if err0 != 0 {
		return err0
	}

	switch methodDetail.Type() {
	case reflection.ByteConvertibleMethod, reflection.BigIntMethod:
		return f.WriteBytesConvertibleInterface(module, bufPtr, valueIface)
	case reflection.BytesMethod:
		return f.WriteBytesInterface(module, bufPtr, valueIface)
	}

	return 0
}

func (f *Factory) W_ethGetTransactionMethodUint64(
	ctx context.Context,
	module common.Module,
	clientId,
	blockId,
	contractId,
	transactionId,
	methodPtr,
	methodLen,
	numPtr uint32,
) errno.Error {
	method, err0 := f.ReadString(module, methodPtr, methodLen)
	if err0 != 0 {
		return err0
	}

	transaction, err0 := f.getTransaction(module, clientId, blockId, contractId, transactionId)
	if err0 != 0 {
		return err0
	}

	_, valueIface, err0 := transaction.callTransactionMethod(method, validateUint64Method)
	if err0 != 0 {
		return err0
	}

	return f.WriteUint64LeInterface(module, numPtr, valueIface)

}

func (f *Factory) W_ethTransactionRawSignaturesSize(
	ctx context.Context,
	module common.Module,
	clientId,
	blockId,
	contractId,
	transactionId,
	vSigSizePtr,
	rSigSizePtr,
	sSigSizePtr uint32,
) errno.Error {
	transaction, err := f.getTransaction(module, clientId, blockId, contractId, transactionId)
	if err != 0 {
		return err
	}

	return f.WriteBytesConvertibleMultiSize(
		module,
		[]uint32{vSigSizePtr, rSigSizePtr, sSigSizePtr},
		helpers.BytesConvertibleMultiHelper(transaction.RawSignatureValues())...,
	)
}

func (f *Factory) W_ethTransactionRawSignatures(
	ctx context.Context,
	module common.Module,
	clientId,
	blockId,
	contractId,
	transactionId,
	vSigBufPtr,
	rSigBufPtr,
	sSigBufPtr uint32,
) errno.Error {
	transaction, err := f.getTransaction(module, clientId, blockId, contractId, transactionId)
	if err != 0 {
		return err
	}

	return f.WriteBytesConvertibleMulti(
		module,
		[]uint32{vSigBufPtr, rSigBufPtr, sSigBufPtr},
		helpers.BytesConvertibleMultiHelper(transaction.RawSignatureValues())...,
	)
}

func (f *Factory) W_ethSendTransaction(
	ctx context.Context,
	module common.Module,
	clientId,
	blockId,
	contractId,
	transactionId uint32,
) errno.Error {
	client, err0 := f.getClient(clientId)
	if err0 != 0 {
		return err0
	}

	transaction, err0 := f.getTransaction(module, clientId, blockId, contractId, transactionId)
	if err0 != 0 {
		return err0
	}

	err := client.SendTransaction(f.ctx, transaction.Transaction)
	if err != nil {
		return errno.ErrorEthereumSendTransactionFailed
	}

	return 0
}

func (f *Factory) W_ethJsonSize(
	ctx context.Context,
	module common.Module,
	clientId,
	blockId,
	contractId,
	transactionId,
	sizePtr uint32,
) errno.Error {
	transaction, err0 := f.getTransaction(module, clientId, blockId, contractId, transactionId)
	if err0 != 0 {
		return err0
	}

	buf, err := transaction.MarshalJSON()
	if err != nil {
		return errno.ErrorEthereumMarshalJSON
	}

	return f.WriteBytesSize(module, sizePtr, buf)
}

func (f *Factory) W_ethJson(
	ctx context.Context,
	module common.Module,
	clientId,
	blockId,
	contractId,
	transactionId,
	bufPtr uint32,
) errno.Error {
	transaction, err0 := f.getTransaction(module, clientId, blockId, contractId, transactionId)
	if err0 != 0 {
		return err0
	}

	buf, err := transaction.MarshalJSON()
	if err != nil {
		return errno.ErrorEthereumMarshalJSON
	}

	return f.WriteBytes(module, bufPtr, buf)
}
