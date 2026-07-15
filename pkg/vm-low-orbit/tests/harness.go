// Package tests runs the guest wasm fixtures against the real vm-low-orbit
// plugin with mocked backends — no dream. Each fixture (see fixtures/guest,
// compiled by `make vm-fixtures`) is a reactor module that imports the
// "taubyte/sdk" host functions the plugin exposes.
package tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path"
	"runtime"
	"testing"

	"github.com/taubyte/tau/core/vm"
	vmWaz "github.com/taubyte/tau/pkg/vm"
	plugins "github.com/taubyte/tau/pkg/vm-low-orbit"
	"github.com/taubyte/tau/pkg/vm/backend/file"
	vmContext "github.com/taubyte/tau/pkg/vm/context"
	loader "github.com/taubyte/tau/pkg/vm/loaders/wazero"
	fileRes "github.com/taubyte/tau/pkg/vm/resolvers/file"
	source "github.com/taubyte/tau/pkg/vm/sources/taubyte"
)

// fixtureWasm returns the absolute path to a compiled guest fixture.
func fixtureWasm(name string) string {
	_, self, _, _ := runtime.Caller(0)
	return path.Join(path.Dir(self), "fixtures", "wasm", name+".wasm")
}

// guestCall loads the given fixture wasm, attaches the vm-low-orbit plugin
// (already Initialized by the caller), synthesizes an HTTP event from req, and
// invokes the named export. It returns the recorded HTTP response and the
// guest's return code (0 == success).
func guestCall(t *testing.T, ctx context.Context, wasm, export string, req *http.Request, ctxOpts ...vmContext.Option) (*httptest.ResponseRecorder, uint64) {
	t.Helper()

	resolver := fileRes.New(fixtureWasm(wasm))
	svc := vmWaz.New(ctx, source.New(loader.New(resolver, file.New())))

	vmCtx, err := vmContext.New(ctx, ctxOpts...)
	if err != nil {
		t.Fatalf("vm context: %v", err)
	}

	inst, err := svc.New(vmCtx, vm.Config{})
	if err != nil {
		t.Fatalf("vm instance: %v", err)
	}
	t.Cleanup(func() { inst.Close() })

	rt, err := inst.Runtime()
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	t.Cleanup(func() { rt.Close() })

	pi, _, err := rt.Attach(plugins.Plugin())
	if err != nil {
		t.Fatalf("attach plugin: %v", err)
	}
	t.Cleanup(func() { pi.Close() })

	sdk, err := plugins.With(pi)
	if err != nil {
		t.Fatalf("plugin instance: %v", err)
	}

	w := httptest.NewRecorder()
	ev := sdk.CreateHttpEvent(w, req)

	module, err := rt.Module(wasm)
	if err != nil {
		t.Fatalf("load module %q: %v", wasm, err)
	}
	fn, err := module.Function(export)
	if err != nil {
		t.Fatalf("get export %q: %v", export, err)
	}

	ret, err := fn.RawCall(ctx, uint64(ev.Id))
	if err != nil {
		t.Fatalf("calling %q: %v", export, err)
	}
	// void exports (e.g. Rust reactor functions) return nothing; treat as 0.
	var code uint64
	if len(ret) > 0 {
		code = ret[0]
	}
	return w, code
}

// testCtxOpts is the default vm context (project/app/resource/commit/branch)
// most guests run under.
func testCtxOpts() []vmContext.Option {
	return []vmContext.Option{
		vmContext.Project("proj-123"),
		vmContext.Application("app-456"),
		vmContext.Resource("res-789"),
		vmContext.Commit("commit-abc"),
		vmContext.Branch("master"),
	}
}
