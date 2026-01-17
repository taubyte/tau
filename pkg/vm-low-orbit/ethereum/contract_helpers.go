//go:build web3
// +build web3

package ethereum

import (
	"crypto/ecdsa"
	"reflect"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (c *Contract) generateTransactionId() uint32 {
	c.transactionsLock.Lock()
	c.transactionsToGrab += 1
	c.transactionsLock.Unlock()

	return c.transactionsToGrab
}

func (c *Client) generateContractId() uint32 {
	c.contractsLock.Lock()
	defer func() {
		c.contractsIdToGrab += 1
		c.contractsLock.Unlock()
	}()

	return c.contractsIdToGrab
}

func (c *Client) getContract(contractId uint32) (*Contract, errno.Error) {
	c.contractsLock.RLock()
	defer c.contractsLock.RUnlock()
	if contract, ok := c.contracts[contractId]; ok {
		return contract, errno.ErrorNone
	}

	return nil, errno.ErrorEthereumContractNotFound
}

func (f *Factory) handleBoundContractSize(module common.Module, client *Client, contract *bind.BoundContract, _abi abi.ABI, contractIdPtr, methodsSizePtr, eventSizePtr uint32) (*Contract, errno.Error) {
	c := &Contract{
		BoundContract: contract,
		client:        client,
		Id:            client.generateContractId(),
		abi:           &_abi,
		methods:       make(map[string]*contractMethod),
		transactions:  make(map[uint32]*Transaction),
		events:        make(map[string]*contractEvent),
	}

	var methodList []string
	for _, method := range _abi.Methods {
		methodList = append(methodList, method.Name)

		var inputs []string
		var outputs []string
		for _, input := range method.Inputs {
			inputs = append(inputs, input.Type.GetType().String())
		}

		for _, output := range method.Outputs {
			outputs = append(outputs, output.Type.GetType().String())
		}

		c.methods[method.Name] = &contractMethod{
			inputs:   inputs,
			outputs:  outputs,
			constant: method.IsConstant(), // if false then needs to call transaction

		}
	}

	c.eventsLock.Lock()
	var events []string
	for _, event := range _abi.Events {
		inputs := event.Inputs
		eventInputStructFields := make([]reflect.StructField, len(inputs))
		for idx, inputArg := range inputs {
			eventInputStructFields[idx] = reflect.StructField{
				Name: capitalize(inputArg.Name),
				Type: inputArg.Type.GetType(),
			}
		}

		c.events[event.Name] = &contractEvent{
			parent:     c,
			event:      event,
			structType: reflect.StructOf(eventInputStructFields),
		}

		events = append(events, event.Name)
	}

	c.eventsLock.Unlock()

	if err := f.WriteStringSliceSize(module, methodsSizePtr, methodList); err != 0 {
		return nil, err
	}

	if err := f.WriteStringSliceSize(module, eventSizePtr, events); err != 0 {
		return nil, err
	}

	if err := f.WriteUint32Le(module, contractIdPtr, c.Id); err != 0 {
		return nil, err
	}

	client.contractsLock.Lock()
	client.contracts[c.Id] = c
	client.contractsLock.Unlock()

	return c, 0
}

func (f *Factory) toEcdsa(module common.Module, privKeyPtr, privKeySize uint32) (*ecdsa.PrivateKey, errno.Error) {
	pkBytes, err0 := f.ReadBytes(module, privKeyPtr, privKeySize)
	if err0 != 0 {
		return nil, err0
	}

	privateKey, err := crypto.ToECDSA(pkBytes)
	if err != nil {
		return nil, errno.ErrorEthereumInvalidPrivateKey
	}

	return privateKey, 0
}
