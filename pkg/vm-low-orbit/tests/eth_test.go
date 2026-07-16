//go:build web3

package tests

import (
	"context"
	"math/big"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
	ethplugin "github.com/taubyte/tau/pkg/vm-low-orbit/ethereum"
)

// mockEthBackend implements the plugin's Backend interface for the read path.
// The contract methods (bind.ContractBackend) come from the embedded nil
// interface — the read-path fixture never calls them, so a real EVM isn't
// needed (which also dodges go-ethereum v1.12's pebble driver, incompatible
// with the pebble tau uses).
type mockEthBackend struct {
	bind.ContractBackend
	block   *types.Block
	chainID *big.Int
}

func (m *mockEthBackend) BlockByNumber(context.Context, *big.Int) (*types.Block, error) {
	return m.block, nil
}
func (m *mockEthBackend) BlockNumber(context.Context) (uint64, error) {
	return m.block.NumberU64(), nil
}
func (m *mockEthBackend) ChainID(context.Context) (*big.Int, error) { return m.chainID, nil }

// TestEth drives the ethereum read path against an in-memory backend injected
// through the plugin's dial seam — no live RPC, no fixed port. The backend
// serves one block holding a known dynamic-fee transfer, and the guest reads it
// all back through the host ABI.
func TestEth(t *testing.T) {
	chainID := big.NewInt(1337)

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	recipient := common.BytesToAddress([]byte{
		0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11,
		0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11,
	})
	tx, err := types.SignNewTx(key, types.LatestSignerForChainID(chainID), &types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     0,
		GasTipCap: big.NewInt(1_000_000_000),
		GasFeeCap: big.NewInt(2_000_000_000),
		Gas:       21000,
		To:        &recipient,
		Value:     big.NewInt(1_000_000_000_000_000_000),
	})
	if err != nil {
		t.Fatal(err)
	}

	// block 1 holding the one transfer (no trie computation needed for reads)
	block := types.NewBlockWithHeader(&types.Header{Number: big.NewInt(1)}).
		WithBody([]*types.Transaction{tx}, nil)

	orig := ethplugin.NewBackend
	ethplugin.NewBackend = func(context.Context, string, ...rpc.ClientOption) (ethplugin.Backend, func(), error) {
		return &mockEthBackend{block: block, chainID: chainID}, func() {}, nil
	}
	t.Cleanup(func() { ethplugin.NewBackend = orig })

	req := httptest.NewRequest("GET", "/eth", nil)
	w, code := guestCall(t, context.Background(), "eth", "ethtest", req, testCtxOpts()...)
	if code != 0 {
		t.Fatalf("guest returned %d: %s", code, w.Body.String())
	}
	if got := w.Body.String(); got != `{"ping": "pong"}` {
		t.Fatalf("body = %q, want success marker", got)
	}
}
