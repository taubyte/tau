//go:build eth

package main

//lint:file-ignore U1000 compiled file
//lint:file-ignore SA4006 is used
//lint:file-ignore SA4017 is used

import (
	"bytes"
	_ "embed"
	"fmt"
	"math/big"
	"time"

	ethereum "github.com/taubyte/go-sdk/ethereum/client"
	"github.com/taubyte/go-sdk/event"
	http "github.com/taubyte/go-sdk/http/client"
)

//export _sleep
func sleep(dur time.Duration)

//export ethtest
func ethtest(e event.Event) (err0 uint32) {
	var testBlockNumber int64 = 7557740
	var testChainId int64 = 5
	testAddress := []byte{204, 123, 178, 210, 25, 160, 252, 8, 3, 62, 19, 6, 41, 194, 184, 84, 183, 186, 145, 149}
	var testNonce uint64 = 38
	testHash := []byte{42, 86, 105, 124, 14, 217, 250, 133, 248, 227, 19, 102, 117, 248, 70, 120, 110, 67, 239, 173, 14, 211, 221, 18, 38, 121, 26, 174, 222, 120, 151, 201}
	var testGasTipCap int64 = 1500000000
	var testGaspPrice int64 = 1500000015
	var testFeeCap int64 = 1500000015
	var testGas uint64 = 46918
	testDataLen := 68

	h, err := e.HTTP()
	if err != nil {
		panic(err)
	}

	errReturn := func(msg string) {
		h.Write([]byte(msg))
		h.Return(404)
	}

	client, err := ethereum.New("https://goerli.infura.io/v3/9aa3d95b3bc440fa88ea12eaa4456161")
	if err != nil {
		errReturn("Unable to connect to rpc client")
		return 1
	}

	blockNumber, err := client.CurrentBlockNumber()
	if err != nil {
		errReturn("Unable to get current block number")
		return 1
	}

	chainId, err := client.CurrentChainID()
	if err != nil {
		errReturn("Unable to get chain ID")
		return
	}

	block, err := client.BlockByNumber(big.NewInt(int64(testBlockNumber)))
	if err != nil {
		errReturn(fmt.Sprintf("Unable to get Block with block number `%d`", blockNumber))
		return 1
	}

	bigNumber, err := block.Number()
	if err != nil {
		errReturn("Unable to get this block's block number")
		return 1
	}

	if bigNumber.Int64() != testBlockNumber {
		errReturn(fmt.Sprintf("Expected block number `%d` got `%d`", testBlockNumber, bigNumber.Int64()))
		return 1
	}

	transactions, err := block.Transactions()
	if err != nil {
		errReturn("unable to list transactions")
		return 1
	}

	transaction := transactions[0]

	chain, err := transaction.Chain()
	if err != nil {
		errReturn("Unable to get chain id with: " + err.Error())
		return 1
	}

	if chain.Int64() != testChainId {
		errReturn(fmt.Sprintf("Expected chainId `%d` got `%d`", chain, testChainId))
		return 1
	}

	address, err := transaction.ToAddress()
	if err != nil {
		errReturn("Unable to get address")
		return 1
	}

	if !bytes.Equal(address, testAddress) {
		errReturn(fmt.Sprintf("Expected address `%s` got `%s`", address, testAddress))
		return 1
	}

	nonce, err := transaction.Nonce()
	if err != nil {
		errReturn("Unable to get nonce with: " + err.Error())
		return 1
	}

	if nonce != testNonce {
		errReturn(fmt.Sprintf("Expected nonce `%d` got `%d`", nonce, testNonce))
		return 1
	}

	hash, err := transaction.Hash()
	if err != nil {
		errReturn("Unable to get hash")
		return 1
	}

	if !bytes.Equal(hash, testHash) {
		errReturn(fmt.Sprintf("Expected hash `%s` got `%s`", hash, testHash))
		return 1
	}

	gasTip, err := transaction.GasTipCap()
	if err != nil {
		errReturn("Unable to get gas tip cap")
		return 1
	}

	if gasTip.Int64() != testGasTipCap {
		errReturn(fmt.Sprintf("Expected gas tip cap `%d` got `%d`", gasTip, testGasTipCap))
		return 1
	}

	gasPrice, err := transaction.GasPrice()
	if err != nil {
		errReturn("Unable to get gasPrice")
		return 1
	}

	if gasPrice.Int64() != testGaspPrice {
		errReturn(fmt.Sprintf("Expected gas price `%d` got `%d`", gasPrice, testGaspPrice))
		return 1
	}

	gasFee, err := transaction.GasFeeCap()
	if err != nil {
		errReturn("Unable to get GasFeeCap")
		return 1
	}

	if gasFee.Int64() != testFeeCap {
		errReturn(fmt.Sprintf("Expected gas fee cap `%d` got `%d`", gasFee, testFeeCap))
		return 1
	}

	gas, err := transaction.Gas()
	if err != nil {
		errReturn("Unable to get gas")
		return 1
	}

	if gas != testGas {
		errReturn(fmt.Sprintf("Expected gas `%d` got `%d`", gas, testGas))
		return 1
	}

	data, err := transaction.Data()
	if err != nil {
		errReturn("Unable to get gas")
		return 1
	}

	if len(data) != testDataLen {
		errReturn(fmt.Sprintf("Expected data len `%d` got `%d`", len(data), testDataLen))
		return 1
	}

	httpClient, err := http.New()
	if err != nil {
		errReturn("Unable to get http client")
		return 1
	}

	contractAddress := "0xB36046FfB1587e0Cde7FD79Db09a5E0410697368"
	binString := "608060405234801561001057600080fd5b50610b94806100206000396000f3fe608060405234801561001057600080fd5b50600436106100885760003560e01c80636057361d1161005b5780636057361d1461010457806375e45dc0146101205780639f901e7c14610151578063a270e6801461018157610088565b80630dd587051461008d578063200d2ed2146100be57806320873d32146100dc5780632e64cec1146100e6575b600080fd5b6100a760048036038101906100a291906105a8565b6101b1565b6040516100b5929190610692565b60405180910390f35b6100c66101c2565b6040516100d39190610739565b60405180910390f35b6100e46101d5565b005b6100ee6101d7565b6040516100fb919061076d565b60405180910390f35b61011e600480360381019061011991906107b4565b6101e0565b005b61013a600480360381019061013591906108bb565b6101ea565b60405161014892919061097b565b60405180910390f35b61016b600480360381019061016691906109d0565b61025d565b6040516101789190610739565b60405180910390f35b61019b600480360381019061019691906109fd565b61036c565b6040516101a89190610a3d565b60405180910390f35b606060008284915091509250929050565b600160009054906101000a900460ff1681565b565b60008054905090565b8060008190555050565b600060606001836101fb9190610a87565b846040518060400160405280600881526020017f5f74617562797465000000000000000000000000000000000000000000000000815250604051602001610243929190610af8565b604051602081830303815290604052915091509250929050565b6000806004811115610272576102716106c2565b5b826004811115610285576102846106c2565b5b036102935760019050610367565b600160048111156102a7576102a66106c2565b5b8260048111156102ba576102b96106c2565b5b036102c85760029050610367565b600260048111156102dc576102db6106c2565b5b8260048111156102ef576102ee6106c2565b5b036102fd5760039050610367565b60036004811115610311576103106106c2565b5b826004811115610324576103236106c2565b5b036103325760049050610367565b600480811115610345576103446106c2565b5b826004811115610358576103576106c2565b5b036103665760009050610367565b5b919050565b60008173ffffffffffffffffffffffffffffffffffffffff16636352211e846040518263ffffffff1660e01b81526004016103a7919061076d565b602060405180830381865afa1580156103c4573d6000803e3d6000fd5b505050506040513d601f19601f820116820180604052508101906103e89190610b31565b905092915050565b6000604051905090565b600080fd5b600080fd5b600073ffffffffffffffffffffffffffffffffffffffff82169050919050565b600061042f82610404565b9050919050565b61043f81610424565b811461044a57600080fd5b50565b60008135905061045c81610436565b92915050565b600080fd5b600080fd5b6000601f19601f8301169050919050565b7f4e487b7100000000000000000000000000000000000000000000000000000000600052604160045260246000fd5b6104b58261046c565b810181811067ffffffffffffffff821117156104d4576104d361047d565b5b80604052505050565b60006104e76103f0565b90506104f382826104ac565b919050565b600067ffffffffffffffff8211156105135761051261047d565b5b61051c8261046c565b9050602081019050919050565b82818337600083830152505050565b600061054b610546846104f8565b6104dd565b90508281526020810184848401111561056757610566610467565b5b610572848285610529565b509392505050565b600082601f83011261058f5761058e610462565b5b813561059f848260208601610538565b91505092915050565b600080604083850312156105bf576105be6103fa565b5b60006105cd8582860161044d565b925050602083013567ffffffffffffffff8111156105ee576105ed6103ff565b5b6105fa8582860161057a565b9150509250929050565b600081519050919050565b600082825260208201905092915050565b60005b8381101561063e578082015181840152602081019050610623565b60008484015250505050565b600061065582610604565b61065f818561060f565b935061066f818560208601610620565b6106788161046c565b840191505092915050565b61068c81610424565b82525050565b600060408201905081810360008301526106ac818561064a565b90506106bb6020830184610683565b9392505050565b7f4e487b7100000000000000000000000000000000000000000000000000000000600052602160045260246000fd5b60058110610702576107016106c2565b5b50565b6000819050610713826106f1565b919050565b600061072382610705565b9050919050565b61073381610718565b82525050565b600060208201905061074e600083018461072a565b92915050565b6000819050919050565b61076781610754565b82525050565b6000602082019050610782600083018461075e565b92915050565b61079181610754565b811461079c57600080fd5b50565b6000813590506107ae81610788565b92915050565b6000602082840312156107ca576107c96103fa565b5b60006107d88482850161079f565b91505092915050565b600067ffffffffffffffff8211156107fc576107fb61047d565b5b6108058261046c565b9050602081019050919050565b6000610825610820846107e1565b6104dd565b90508281526020810184848401111561084157610840610467565b5b61084c848285610529565b509392505050565b600082601f83011261086957610868610462565b5b8135610879848260208601610812565b91505092915050565b600060ff82169050919050565b61089881610882565b81146108a357600080fd5b50565b6000813590506108b58161088f565b92915050565b600080604083850312156108d2576108d16103fa565b5b600083013567ffffffffffffffff8111156108f0576108ef6103ff565b5b6108fc85828601610854565b925050602061090d858286016108a6565b9150509250929050565b61092081610882565b82525050565b600081519050919050565b600082825260208201905092915050565b600061094d82610926565b6109578185610931565b9350610967818560208601610620565b6109708161046c565b840191505092915050565b60006040820190506109906000830185610917565b81810360208301526109a28184610942565b90509392505050565b600581106109b857600080fd5b50565b6000813590506109ca816109ab565b92915050565b6000602082840312156109e6576109e56103fa565b5b60006109f4848285016109bb565b91505092915050565b60008060408385031215610a1457610a136103fa565b5b6000610a228582860161079f565b9250506020610a338582860161044d565b9150509250929050565b6000602082019050610a526000830184610683565b92915050565b7f4e487b7100000000000000000000000000000000000000000000000000000000600052601160045260246000fd5b6000610a9282610882565b9150610a9d83610882565b9250828201905060ff811115610ab657610ab5610a58565b5b92915050565b600081905092915050565b6000610ad282610604565b610adc8185610abc565b9350610aec818560208601610620565b80840191505092915050565b6000610b048285610ac7565b9150610b108284610ac7565b91508190509392505050565b600081519050610b2b81610436565b92915050565b600060208284031215610b4757610b466103fa565b5b6000610b5584828501610b1c565b9150509291505056fea26469706673582212209ace2a4a4ac22b505b15598bf5ffce9cbe8e96b27569d33ca1b3b97031e0ef5964736f6c63430008110033"
	privKeyHex := "d95da681814cba888f4d5258d38cb73cffe10baeadbdf04b7ace76de3a9b9ca7"
	privKey, err := ethereum.HexToECDSABytes(privKeyHex)
	if err != nil {
		errReturn("failed to get private key")
		return 1
	}

	bin := bytes.NewReader([]byte(binString))

	req, err := httpClient.Request(fmt.Sprintf("https://api-goerli.etherscan.io/api?module=contract&action=getabi&format=raw&address=%s&apikey=GJ9AZ69URIFNGXCUGXN2GSSRSPB16HI3M7", contractAddress))
	if err != nil {
		errReturn("Unable to get http request")
		return 1
	}

	res, err := req.Do()
	if err != nil {
		errReturn("Unable to get http client")
		return 1
	}

	deployedContract, _, err := client.DeployContract(res.Body(), bin, chainId, privKey)
	if err != nil {
		errReturn("Deploying contract failed with: " + err.Error())
		return 1
	}

	req, err = httpClient.Request(fmt.Sprintf("https://api-goerli.etherscan.io/api?module=contract&action=getabi&format=raw&address=%s&apikey=GJ9AZ69URIFNGXCUGXN2GSSRSPB16HI3M7", contractAddress))
	if err != nil {
		errReturn("Unable to get http request")
		return 1
	}

	res, err = req.Do()
	if err != nil {
		errReturn("Unable to get http client")
		return 1
	}

	contract, err := client.NewBoundContract(res.Body(), contractAddress)
	if err != nil {
		errReturn("Unable to create bound contract")
		return
	}

	for _, method := range contract.Methods() {
		var found bool
		for _, _method := range deployedContract.Methods() {
			if method.Name() == _method.Name() {
				found = true
				break
			}
		}
		if !found {
			errReturn(fmt.Sprintf("Method `%s`, not found in deployed contract", method.Name()))
			return
		}
	}

	addUp, err := contract.Method("addup")
	if err != nil {
		errReturn(fmt.Sprintf("Unable to find method addup with: %s", err))
		return
	}

	addUpStringInput := "taf"
	addUpUint8Input := uint8(5)
	outputs, err := addUp.Call(addUpStringInput, addUpUint8Input)
	if err != nil {
		errReturn("Unable to call addup with " + err.Error())
		return
	}

	if len(outputs) != 2 {
		errReturn("Expected only 2 outputs for method addup")
		return
	}

	stringVal, ok := outputs[1].(string)
	if !ok {
		errReturn(fmt.Sprintf("Expected type string output for `addup` call, got %t", outputs[1]))
		return
	}

	expectedString := addUpStringInput + "_taubyte"
	if stringVal != expectedString {
		errReturn(fmt.Sprintf("Expected `%s` got `%s`", expectedString, stringVal))
		return
	}

	uint8val, ok := outputs[0].(uint8)
	if !ok {
		errReturn("Expected value to be uint8")
		return
	}

	expectedUint8 := addUpUint8Input + 1
	if uint8val != expectedUint8 {
		errReturn(fmt.Sprintf("Expected `%d` got `%d`", uint8val, expectedUint8))
		return
	}

	retrieve, err := contract.Method("retrieve")
	if err != nil {
		errReturn("Unable to get method retrieve with " + err.Error())
		return
	}

	outputs, err = retrieve.Call()
	if err != nil {
		errReturn("Unable to call retrieve with " + err.Error())
		return
	}

	if len(outputs) != 1 {
		errReturn("Expected only one output for retrieve call")
		return
	}

	if _, ok := outputs[0].(*big.Int); !ok {
		errReturn(fmt.Sprintf("Expected value to be of type `*big.Int` got type `%t`", outputs[0]))
		return
	}

	none, err := contract.Method("noparamsnoout")
	if err != nil {
		errReturn("Unable to get method noparamsnoout with " + err.Error())
		return
	}

	_, err = none.Call()
	if err == nil {
		errReturn("Should not be able to call method noparamsnoout as it is a transaction function")
		return
	}

	_, err = none.Transact(chainId, privKey)
	if err != nil {
		errReturn("Cannot transact noparamsnoout with " + err.Error())
	}

	testBytes, err := contract.Method("testbytes")
	if err != nil {
		errReturn("Unable to get method testbytes with " + err.Error())
		return
	}

	testbytes := []byte{248, 125, 51, 180, 69, 115, 172, 156, 143, 253, 212, 120, 220, 180, 135, 69, 160, 248, 12, 184, 20, 20}

	outputs, err = testBytes.Call(address, testbytes)
	if err != nil {
		errReturn("Unable to call method testbytes with " + err.Error())
		return
	}

	if len(outputs) != 2 {
		errReturn(fmt.Sprintf("Expected 2 outputs got %d", len(outputs)))
		return
	}

	bytesOutput, ok := outputs[0].([]byte)
	if !ok {
		errReturn(fmt.Sprintf("Expected testBytes call output to be []byte got %t", outputs[0]))
		return
	}

	addressOutput, ok := outputs[1].([]byte)
	if !ok {
		errReturn(fmt.Sprintf("Expected testBytes call output to be []byte got %t", outputs[0]))
		return
	}

	if !bytes.Equal(addressOutput, address) {
		errReturn("Address input and return are not the same")
		return
	}

	if !bytes.Equal(bytesOutput, testbytes) {
		errReturn("Address input and return are not the same")
		return
	}

	testEnum, err := contract.Method("testEnum")
	if err != nil {
		errReturn("Unable to get method testenums with " + err.Error())
		return
	}

	uint8val = uint8(1)
	switch uint8val {
	case 0:
		expectedUint8 = 1
	case 1:
		expectedUint8 = 2
	case 2:
		expectedUint8 = 3
	case 3:
		expectedUint8 = 4
	case 4:
		expectedUint8 = 0
	}

	outputs, err = testEnum.Call(uint8val)
	if err != nil {
		errReturn("Unable to call method testenums with " + err.Error())
		return
	}

	if len(outputs) != 1 {
		errReturn(fmt.Sprintf("Expected 1 output for method testEnum got %d", len(outputs)))
		return
	}

	if val, ok := outputs[0].(uint8); !ok {
		errReturn(fmt.Sprintf("Expected uint8 value return from testEnums got %t", outputs[0]))
		return
	} else {
		if val != expectedUint8 {
			errReturn(fmt.Sprintf("Expected `%d` got `%d` from testenums", expectedUint8, val))
		}
	}

	store, err := contract.Method("store")
	if err != nil {
		errReturn("Unable to get method store")
		return
	}

	newInt := big.NewInt(420)
	_, err = store.Transact(chainId, privKey, newInt)
	if err != nil {
		errReturn(fmt.Sprintf("Unable to transact store with: %s", err))
		return
	}

	// takes some time to reflect
	var transactErr error
	sleep(30 * time.Second)
	for i := 0; i < 5; i++ {
		outputs, err = retrieve.Call()
		if err != nil {
			errReturn("Unable to call retrieve with " + err.Error())
			return
		}

		if len(outputs) != 1 {
			errReturn("Expected only one output for retrieve call")
			return
		}

		val, ok := outputs[0].(*big.Int)
		if !ok {
			errReturn(fmt.Sprintf("Expected value to be of type `*big.Int` got type `%t`", outputs[0]))
			return
		}

		if val.Cmp(newInt) != 0 {
			time.Sleep(1 * time.Second)
			transactErr = fmt.Errorf("expected value to be `%d` got `%d`", newInt, val)
			return
		}
	}

	if transactErr != nil {
		errReturn(transactErr.Error())
		return
	}

	newInt = big.NewInt(359)
	_, err = store.Transact(chainId, privKey, newInt)
	if err != nil {
		errReturn(fmt.Sprintf("Unable to transact store with: %s", err))
		return
	}

	sleep(30 * time.Second)

	for i := 0; i < 5; i++ {
		outputs, err = retrieve.Call()
		if err != nil {
			errReturn("Unable to call retrieve with " + err.Error())
			return
		}

		if len(outputs) != 1 {
			errReturn("Expected only one output for retrieve call")
			return
		}

		val, ok := outputs[0].(*big.Int)
		if !ok {
			errReturn(fmt.Sprintf("Expected value to be of type `*big.Int` got type `%t`", outputs[0]))
			return
		}

		if val.Cmp(newInt) != 0 {
			time.Sleep(1 * time.Second)
			transactErr = fmt.Errorf("expected value to be `%d` got `%d`", newInt, val)
			return
		}
	}

	if transactErr != nil {
		errReturn(transactErr.Error())
		return
	}

	client.Close()

	_, err = client.CurrentChainID()
	if err == nil {
		errReturn("Expected error to not be nil")
		return
	}

	h.Return(205)
	h.Write([]byte("All green"))

	return 1
}
