package worker

import (
	"testing"

	"connectrpc.com/connect"

	workerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/worker/v2"
)

func BenchmarkResolve(b *testing.B) {
	h := newHarness(b)
	for _, t := range []Target{
		{Project: "shop", Environment: "prod", Kind: "application", Name: "api"},
		{Project: "shop", Environment: "prod", Kind: "function", Name: "checkout"},
	} {
		if _, err := h.Worker.Resolve(b.Context(), t); err != nil {
			b.Fatal(err)
		}
		b.Run(t.Kind+"/"+t.Name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := h.Worker.Resolve(b.Context(), t); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkInvokeOverRPC(b *testing.B) {
	h := newHarness(b)
	req := &workerv2.InvokeRequest{Target: target("application", "api"), Method: "GET", Path: "/orders"}
	if _, err := h.Client.Invoke(b.Context(), connect.NewRequest(req)); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := h.Client.Invoke(b.Context(), connect.NewRequest(req)); err != nil {
			b.Fatal(err)
		}
	}
}
