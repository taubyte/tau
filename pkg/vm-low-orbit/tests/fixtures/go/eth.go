//go:build eth

package main

//lint:file-ignore U1000 compiled file

import (
	"bytes"
	"fmt"

	"math/big"

	ethereum "github.com/taubyte/go-sdk/ethereum/client"
	"github.com/taubyte/go-sdk/event"
)

// ethtest drives the ethereum read path against whatever node the host dials.
// The test injects an in-memory node seeded with one block holding one dynamic-
// fee transfer (nonce 0, to 0x11..11, gas 21000, tip 1 gwei, feecap 2 gwei,
// chain 1337). The guest reads it all back through the host ABI and checks the
// values match — proving dial -> block/tx decode round-trips.
var recipient = []byte{
	0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11,
	0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11,
}

//export ethtest
func ethtest(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}
	fail := func(msg string) uint32 {
		h.Write([]byte(msg))
		return 1
	}

	client, err := ethereum.New("http://sim.invalid")
	if err != nil {
		return fail("dial: " + err.Error())
	}
	defer client.Close()

	chainID, err := client.CurrentChainID()
	if err != nil {
		return fail("chain id: " + err.Error())
	}
	if chainID.Int64() != 1337 {
		return fail(fmt.Sprintf("chain id = %d, want 1337", chainID.Int64()))
	}

	blockNumber, err := client.CurrentBlockNumber()
	if err != nil {
		return fail("block number: " + err.Error())
	}
	if blockNumber != 1 {
		return fail(fmt.Sprintf("block number = %d, want 1", blockNumber))
	}

	block, err := client.BlockByNumber(big.NewInt(1))
	if err != nil {
		return fail("block by number: " + err.Error())
	}
	num, err := block.Number()
	if err != nil {
		return fail("block.Number: " + err.Error())
	}
	if num.Int64() != 1 {
		return fail(fmt.Sprintf("block.Number = %d, want 1", num.Int64()))
	}

	txs, err := block.Transactions()
	if err != nil {
		return fail("transactions: " + err.Error())
	}
	if len(txs) != 1 {
		return fail(fmt.Sprintf("tx count = %d, want 1", len(txs)))
	}
	tx := txs[0]

	if err := checkTx(tx); err != nil {
		return fail(err.Error())
	}

	h.Write([]byte(`{"ping": "pong"}`))
	return 0
}

func checkTx(tx *ethereum.Transaction) error {
	nonce, err := tx.Nonce()
	if err != nil {
		return fmt.Errorf("nonce: %w", err)
	}
	if nonce != 0 {
		return fmt.Errorf("nonce = %d, want 0", nonce)
	}

	to, err := tx.ToAddress()
	if err != nil {
		return fmt.Errorf("to: %w", err)
	}
	if !bytes.Equal(to, recipient) {
		return fmt.Errorf("to = %x, want %x", to, recipient)
	}

	gas, err := tx.Gas()
	if err != nil {
		return fmt.Errorf("gas: %w", err)
	}
	if gas != 21000 {
		return fmt.Errorf("gas = %d, want 21000", gas)
	}

	chain, err := tx.Chain()
	if err != nil {
		return fmt.Errorf("chain: %w", err)
	}
	if chain.Int64() != 1337 {
		return fmt.Errorf("tx chain = %d, want 1337", chain.Int64())
	}

	tip, err := tx.GasTipCap()
	if err != nil {
		return fmt.Errorf("tip: %w", err)
	}
	if tip.Int64() != 1_000_000_000 {
		return fmt.Errorf("tip = %d, want 1e9", tip.Int64())
	}

	feeCap, err := tx.GasFeeCap()
	if err != nil {
		return fmt.Errorf("feecap: %w", err)
	}
	if feeCap.Int64() != 2_000_000_000 {
		return fmt.Errorf("feecap = %d, want 2e9", feeCap.Int64())
	}

	hash, err := tx.Hash()
	if err != nil {
		return fmt.Errorf("hash: %w", err)
	}
	if len(hash) != 32 {
		return fmt.Errorf("hash len = %d, want 32", len(hash))
	}

	return nil
}
