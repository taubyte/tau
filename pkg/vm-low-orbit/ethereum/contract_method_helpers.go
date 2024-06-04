package ethereum

import (
	"encoding/json"
	"reflect"

	"github.com/ethereum/go-ethereum/accounts/abi"
	ethCommon "github.com/ethereum/go-ethereum/common"
	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/go-sdk/ethereum/client/codec"
)

func verifyInputs(inputsBytes [][]byte, method *contractMethod) ([]interface{}, errno.Error) {
	if len(inputsBytes) != len(method.inputs) {
		return nil, errno.ErrorEthereumInvalidContractMethodInput
	}

	var inputs []interface{}
	for idx, input := range inputsBytes {
		inputType := method.inputs[idx]
		if inputType == "common.Address" {
			inputs = append(inputs, ethCommon.BytesToAddress(input))
			continue
		}

		decoder, err := codec.Converter(inputType).Decoder()
		if err != nil {
			return nil, errno.ErrorEthereumUnsupportedDataType
		}

		val, err := decoder(input)
		if err != nil {
			return nil, errno.ErrorEthereumParseInputTypeFailed
		}

		inputs = append(inputs, val)
	}

	return inputs, 0
}

type inputGetType uint32

const (
	eventInputs inputGetType = iota
	methodInputs
)

func (c *Contract) inputsFromJSON(data []byte, name string, inputGetType inputGetType) ([]interface{}, errno.Error) {
	rawMessages := make([]json.RawMessage, 0)

	if err := json.Unmarshal(data, &rawMessages); err != nil {
		return nil, errno.ErrorJSONUnmarshalRawMessagesFailed
	}

	var args abi.Arguments
	switch inputGetType {
	case eventInputs:
		method, ok := c.abi.Methods[name]
		if !ok {
			return nil, errno.ErrorEthereumContractMethodNotFound
		}

		args = method.Inputs
	case methodInputs:
		event, ok := c.abi.Events[name]
		if !ok {
			return nil, errno.ErrorEthereumEventNotFound
		}

		args = event.Inputs
	}

	if len(args) != len(rawMessages) {
		return nil, errno.ErrorEthereumInvalidParamsLength
	}

	inputs := make([]interface{}, len(args))
	for idx, arg := range args {
		argType := arg.Type.GetType()
		input := reflect.New(argType).Interface()

		if err := json.Unmarshal(rawMessages[idx], &input); err != nil {
			return nil, errno.ErrorEthereumInvalidInputJSON
		}

		inputs[idx] = input
	}

	return inputs, 0
}
