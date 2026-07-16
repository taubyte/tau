//go:build web3

package tests

import (
	"context"
	"net/http/httptest"
	"testing"
)

// eth_sign exercises the ethereum plugin's local ECDSA host functions (hex->key,
// sign, recover pubkey, address-from-pubkey, verify, parse a metamask signature)
// with a fixed key, so the results are deterministic — no chain, no RPC. The
// guest returns 205 on success and writes the failing step otherwise.
func TestEthSign(t *testing.T) {
	req := httptest.NewRequest("GET", "/eth-sign", nil)
	w, _ := guestCall(t, context.Background(), "eth_sign", "signTest", req, testCtxOpts()...)
	if w.Code != 205 {
		t.Fatalf("guest status = %d (want 205): %s", w.Code, w.Body.String())
	}
}
