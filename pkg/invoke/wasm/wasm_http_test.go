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

// A COMPONENT BUILT FOR wasmtime serve, Spin OR wasmCloud RUNS HERE
// UNMODIFIED, and this is what says so. It is the market-standards half of
// having a wasm tier at all: the native world carries what tau's own
// invocations need, and this one carries what the ecosystem already builds.

// standardWorld is a real component exporting wasi:http/incoming-handler.
//
// IT IS NOT OURS, and the reason is in fixtures/README.md: building one needs
// the upstream wasi WIT packages, and the copy available offline is trimmed
// and does not merge. This is wazy's own, out of the module cache — a real
// component from a real toolchain, which is the only thing that can show the
// adapter works. A wazy upgrade that moves it skips this test rather than
// failing it, and says so.
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

// THE WORLD IS READ OFF THE ARTIFACT, not off the configuration: a component
// built for wasmtime serve carries the answer with it, and an author should not
// have to restate what the binary already says.
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

// A NON-HTTP INVOCATION STILL REACHES IT. The standard world has no way to
// express an invocation that is not an HTTP exchange, so a delivery with no
// method and no path arrives as the closest thing the world can say — which is
// better than refusing to deliver it at all. An author who wants the difference
// exports the native world, which carries it exactly.
func TestANonHTTPInvocationReachesAStandardWorldComponent(t *testing.T) {
	instance := start(t, tier(t, standardWorld(t)), invoke.Spec{Name: "fn"})

	res, err := instance.Serve(t.Context(), &invoke.Request{Body: []byte("a delivery")})
	require.NoError(t, err)
	require.NotZero(t, res.Status)
}

// AND THE NATIVE WORLD IS UNAFFECTED. Registering wasi:http for every component
// is what lets the world be read off the artifact, and it must not change what
// a component in the other world does.
func TestTheNativeWorldStillAnswersByName(t *testing.T) {
	instance := start(t, tier(t, handler(t)), invoke.Spec{Name: "fn"})

	res, err := instance.Serve(t.Context(), &invoke.Request{Method: "GET", Path: "/orders"})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status)
	require.Equal(t, []string{"/orders"}, res.Headers["x-path"])
	require.Equal(t, wasm.Name, "wasm")
}
