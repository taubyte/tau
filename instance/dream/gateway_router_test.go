//go:build dreaming

package dream_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	storageService "github.com/taubyte/tau/services/storage"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
	nodeDream "github.com/taubyte/tau/instance/dream"
)

// The whole edge path with every component real: an HTTP request reaching a
// gateway, the gateway calling a worker, placement choosing a node, and the pool
// starting components in non-handler worlds. shaper transforms, picker routes,
// cacher handles.

// routedHost is the name the corpus project's gateway answers on: its zone is
// `example.com` and the gateway's host label is `routed`.
const routedHost = "routed.example.com"

func shaperWASM(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile(filepath.Join("..", "..", "pkg", "invoke", "wasm", "fixtures", "shaper.wasm"))
	require.NoError(t, err, "run `cargo component build --release` in pkg/invoke/wasm/fixtures/shaper")
	return code
}

func pickerWASM(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile(filepath.Join("..", "..", "pkg", "invoke", "wasm", "fixtures", "picker.wasm"))
	require.NoError(t, err, "run `cargo component build --release` in pkg/invoke/wasm/fixtures/picker")
	return code
}

func TestAGatewayRoutedByAFunctionRunsRealComponents(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Services: []string{instance.Register, instance.Worker, instance.Discovery, instance.Gateway, storageService.Descriptor.Name, instance.Runner},
	})
	require.NoError(t, err)
	defer u.Close()

	pids := u.Pids()
	require.NotEmpty(t, pids)

	n := nodeOf(t, pids[0])

	// Three artifacts, one per world.
	publish(t, u, pids[:1], deployed{name: "with/gateway/router", host: routedHost}, artifacts{
		"": cacherWASM(t), "shaper": shaperWASM(t), "picker": pickerWASM(t),
	})

	ask := func(path string, headers ...string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "http://"+routedHost+path, bytes.NewReader([]byte("body")))
		r.Host = routedHost
		for i := 0; i+1 < len(headers); i += 2 {
			r.Header.Set(headers[i], headers[i+1])
		}
		w := httptest.NewRecorder()
		n.Handler().ServeHTTP(w, r)
		return w
	}

	// THE WHOLE CHAIN. The transformer forwards, the router is handed what it
	// produced and answers with a destination named after the PATH, and the
	// handler at that name runs — so a 200 here means three components in three
	// worlds ran behind one request.
	res := ask("/whoami")
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	require.Contains(t, res.Body.String(), "shop/prod/function/saw_whoami",
		"the handler the ROUTER named is the one that ran")

	// THE TRANSFORMER CAN STOP IT, and then neither the router nor the handler
	// runs — which is what makes a fail-closed filter worth declaring.
	res = ask("/whoami", "X-Shape", "reject")
	require.Equal(t, http.StatusTeapot, res.Code, res.Body.String())
	require.Equal(t, "the chain stopped here", res.Body.String())

	// THE ROUTER CAN ANSWER ITSELF, with no workload behind it at all.
	res = ask("/answer")
	require.Equal(t, http.StatusFound, res.Code, res.Body.String())
	require.Equal(t, "/elsewhere", res.Header().Get("Location"))

	// AND `none` IS A 404 rather than a failure: the router ran and said nothing
	// here handles it.
	res = ask("/nothing")
	require.Equal(t, http.StatusNotFound, res.Code, res.Body.String())
}
