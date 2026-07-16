//go:build eth_rpc

package main

//lint:file-ignore U1000 compiled file

import (
	"fmt"

	ethereum "github.com/taubyte/go-sdk/ethereum/client"
	"github.com/taubyte/go-sdk/event"
)

// ethRpcTest dials a local JSON-RPC endpoint (served by the test) and reads the
// current block number + chain id through the ethereum host ABI. It asserts the
// values the test's fake node returns, proving the dial -> RPC -> decode round
// trip. The endpoint is a fixed localhost URL the test listens on.
//
//export ethRpcTest
func ethRpcTest(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}
	fail := func(msg string) uint32 {
		h.Write([]byte(msg))
		return 1
	}

	client, err := ethereum.New("http://127.0.0.1:18546")
	if err != nil {
		return fail("dial: " + err.Error())
	}
	defer client.Close()

	blockNumber, err := client.CurrentBlockNumber()
	if err != nil {
		return fail("block number: " + err.Error())
	}
	if blockNumber != 16 {
		return fail(fmt.Sprintf("block number = %d, want 16", blockNumber))
	}

	chainID, err := client.CurrentChainID()
	if err != nil {
		return fail("chain id: " + err.Error())
	}
	if chainID.Int64() != 1337 {
		return fail(fmt.Sprintf("chain id = %d, want 1337", chainID.Int64()))
	}

	h.Write([]byte(`{"ping": "pong"}`))
	return 0
}
