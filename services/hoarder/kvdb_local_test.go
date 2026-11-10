package hoarder

import (
	"testing"

	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	"github.com/taubyte/tau/p2p/streams/command"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
	"github.com/taubyte/tau/utils/maps"
)

// A client in this process calls the handler directly (hoarderClient.ServeLocal):
// no stream connection, and the body as the client built it, not decoded off the
// wire.
func TestKvdbHandler_CalledInProcess(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	body := func(op, key string, value []byte) command.Body {
		b := command.Body{
			hoarderSpecs.BodyKind:    int(hoarderIface.Global),
			hoarderSpecs.BodyProject: "p",
			hoarderSpecs.BodyMatch:   "m",
			hoarderSpecs.BodyKVOp:    op,
			hoarderSpecs.BodyKey:     key,
		}
		if value != nil {
			b[hoarderSpecs.BodyValue] = value
		}
		return b
	}

	if _, err := srv.kvdbHandler(ctx, nil, body(hoarderSpecs.KVPut, "k", []byte("v"))); err != nil {
		t.Fatalf("put: %v", err)
	}
	resp, err := srv.kvdbHandler(ctx, nil, body(hoarderSpecs.KVGet, "k", nil))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if v, _ := maps.ByteArray(resp, hoarderSpecs.BodyValue); string(v) != "v" {
		t.Fatalf("get = %q", v)
	}
	if got := maps.TryString(resp, hoarderSpecs.BodyServedBy); got != srv.node.ID().String() {
		t.Fatalf("served by %q", got)
	}

	resp, err = srv.kvdbHandler(ctx, nil, body(hoarderSpecs.KVGet, "missing", nil))
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	if code := maps.TryString(resp, hoarderSpecs.BodyCode); code != hoarderSpecs.CodeNotFound {
		t.Fatalf("missing key code = %q", code)
	}
}
