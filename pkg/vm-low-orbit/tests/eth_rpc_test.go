//go:build web3

package tests

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ethRPCHandler is a tiny JSON-RPC 2.0 node: it answers the read calls the eth
// read path makes (eth_blockNumber, eth_chainId) with fixed values the guest
// asserts. Anything else returns null, which is enough for the dial round-trip.
func ethRPCHandler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode rpc request: %v", err)
			return
		}
		result := "null"
		switch req.Method {
		case "eth_blockNumber":
			result = `"0x10"` // 16
		case "eth_chainId":
			result = `"0x539"` // 1337
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(req.ID) + `,"result":` + result + `}`))
	}
}

func TestEthRPC(t *testing.T) {
	// Fixed port: the guest wasm dials a compile-time URL.
	l, err := net.Listen("tcp", "127.0.0.1:18546")
	if err != nil {
		t.Fatalf("listen on fixed rpc port: %v", err)
	}
	srv := &http.Server{Handler: ethRPCHandler(t)}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })

	req := httptest.NewRequest("GET", "/eth-rpc", nil)
	w, code := guestCall(t, context.Background(), "eth_rpc", "ethRpcTest", req, testCtxOpts()...)
	if code != 0 {
		t.Fatalf("guest returned %d: %s", code, w.Body.String())
	}
	if got := w.Body.String(); got != `{"ping": "pong"}` {
		t.Fatalf("body = %q, want success marker", got)
	}
}
