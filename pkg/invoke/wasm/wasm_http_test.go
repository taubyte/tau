package wasm_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
)

// A component built for wasmtime serve, Spin or wasmCloud runs here unmodified.

// standardWorld is wazy's own wasi:http component from the module cache (building
// one needs upstream WIT not available offline; fixtures/README.md). A wazy upgrade
// that moves it skips this test and says so.
func standardWorld(t *testing.T) []byte {
	t.Helper()

	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/samyfodil/wazy").Output()
	if err != nil {
		t.Skipf("cannot locate wazy in the module cache: %v", err)
	}

	path := filepath.Join(strings.TrimSpace(string(out)),
		"imports", "wasip2", "testdata", "real_http_incoming.component.wasm")

	code, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("wazy no longer ships a standard-world component at %s; see fixtures/README.md", path)
	}
	return code
}

func TestAStandardWorldComponentIsRecognisedAndServed(t *testing.T) {
	instance := start(t, tier(t, standardWorld(t)), invoke.Spec{Name: "shop/prod/function/api"})

	res, err := instance.Serve(t.Context(), &invoke.Request{
		Method:  "GET",
		Path:    "/orders",
		Headers: map[string][]string{"x-trace": {"abc"}},
	})
	require.NoError(t, err)
	require.NotZero(t, res.Status, "the guest answered")

	// It went through the wasi:http path rather than being called by name —
	// the native world's export does not exist on this component, so a
	// by-name call would have failed.
	require.NotEmpty(t, res.Body, "a standard-world guest wrote a body")
}

func TestANonHTTPInvocationReachesAStandardWorldComponent(t *testing.T) {
	instance := start(t, tier(t, standardWorld(t)), invoke.Spec{Name: "fn"})

	res, err := instance.Serve(t.Context(), &invoke.Request{Body: []byte("a delivery")})
	require.NoError(t, err)
	require.NotZero(t, res.Status)
}

func TestTheNativeWorldStillAnswersByName(t *testing.T) {
	instance := start(t, tier(t, handler(t)), invoke.Spec{Name: "fn"})

	res, err := instance.Serve(t.Context(), &invoke.Request{Method: "GET", Path: "/orders"})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status)
	require.Equal(t, []string{"/orders"}, res.Headers["x-path"])
	require.Equal(t, wasm.Name, "wasm")
}
