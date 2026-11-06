package invoke

import (
	"context"
	"testing"
)

func BenchmarkPoolDoWarm(b *testing.B) {
	e := newFakeRuntime("fake")
	reply := &Response{Status: 200, Body: []byte("ok")}
	e.serve = func(context.Context, *Request) (*Response, error) { return reply, nil }
	p, _ := newPool(b, e, nil)
	s := spec("fn", "r1")
	req := &Request{Method: "GET", Path: "/"}
	ctx := context.Background()
	if _, err := p.Do(ctx, s, req); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := p.Do(ctx, s, req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPoolDoWarmParallel(b *testing.B) {
	e := newFakeRuntime("fake")
	reply := &Response{Status: 200, Body: []byte("ok")}
	e.serve = func(context.Context, *Request) (*Response, error) { return reply, nil }
	p, _ := newPool(b, e, nil)
	s := spec("fn", "r1")
	s.Concurrency = 64
	ctx := context.Background()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		req := &Request{Method: "GET", Path: "/"}
		for pb.Next() {
			if _, err := p.Do(ctx, s, req); err != nil {
				b.Error(err)
				return
			}
		}
	})
}

// BenchmarkPoolDoWarmParallelUnderADeadline is a call whose caller already set a
// deadline sooner than the call's timeout, as the gateway does for every request:
// the call's own timeout then needs no timer. Each caller has its own context, as
// each request does.
func BenchmarkPoolDoWarmParallelUnderADeadline(b *testing.B) {
	e := newFakeRuntime("fake")
	reply := &Response{Status: 200, Body: []byte("ok")}
	e.serve = func(context.Context, *Request) (*Response, error) { return reply, nil }
	p, _ := newPool(b, e, nil)
	s := spec("fn", "r1")
	s.Concurrency = 64
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout/3)
		defer cancel()
		req := &Request{Method: "GET", Path: "/"}
		for pb.Next() {
			if _, err := p.Do(ctx, s, req); err != nil {
				b.Error(err)
				return
			}
		}
	})
}
