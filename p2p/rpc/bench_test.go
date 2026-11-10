package rpc_test

import (
	"testing"

	"connectrpc.com/connect"

	echov1 "github.com/taubyte/tau/p2p/rpc/testdata/gen/echo/v1"
)

// A unary call to another node over the client a service holds: what each call
// from one service to another costs, allocations included.
func BenchmarkAUnaryCall(b *testing.B) {
	h := newHarness(b)
	payload := []byte("ping")
	b.ReportAllocs()
	for b.Loop() {
		if _, err := h.client.Unary(b.Context(), connect.NewRequest(&echov1.EchoRequest{Payload: payload})); err != nil {
			b.Fatal(err)
		}
	}
}
