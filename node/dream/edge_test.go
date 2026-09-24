//go:build dreaming

package dream_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	commonIface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/node"
	nodeDream "github.com/taubyte/tau/node/dream"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

// THE WHOLE EDGE PATH, WITH EVERY COMPONENT REAL.
//
// A transformer and a router are each proven twice elsewhere and neither proof
// is this one: against the tier with a real component but no gateway
// (pkg/invoke/wasm), and against the gateway with the worker faked
// (services/v2/gateway). What neither can show is the path BETWEEN them — an
// HTTP request reaching a gateway, the gateway calling a worker over a new RPC,
// placement choosing a node, the pool starting a component in a world that is
// not the handler, and the answer coming back far enough to change what the
// client gets.
//
// So this runs three real components behind one request: shaper transforms,
// picker routes, cacher handles.

// edgeHost is the name the corpus project's gateway answers on: its zone is
// `example.com` and the gateway's host label is `edge`.
const edgeHost = "edge.example.com"

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

// TestARequestPassesThroughARealTransformerAndARealRouter.
func TestTheEdgeChainRunsRealComponents(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	m, err := dream.New(t.Context())
	require.NoError(t, err)
	defer m.Close()

	u, err := m.New(dream.UniverseConfig{Name: t.Name()})
	require.NoError(t, err)
	require.NoError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			nodeDream.Protocol: {Others: nodeDream.Services(
				node.Register, node.Worker, node.Stats, node.Gateway)},
		},
	}))

	pids, err := u.GetServicePids(nodeDream.Protocol)
	require.NoError(t, err)
	require.NotEmpty(t, pids)

	n := nodeOf(t, pids[0])
	store, ok := nodeDream.Assets(pids[0])
	require.True(t, ok)

	handlerCID, err := store.Put(t.Context(), bytes.NewReader(cacherWASM(t)))
	require.NoError(t, err)
	shaperCID, err := store.Put(t.Context(), bytes.NewReader(shaperWASM(t)))
	require.NoError(t, err)
	pickerCID, err := store.Put(t.Context(), bytes.NewReader(pickerWASM(t)))
	require.NoError(t, err)

	// THREE ARTIFACTS, one per world. This and projects/with/authorizer are the
	// only projects that need more than one: everywhere else a single binary
	// runs under several names, and here each of the three is built against a
	// different host interface.
	compiled, _ := project(t, "with/edge", artifacts{
		"": handlerCID, "shaper": shaperCID, "picker": pickerCID,
	})

	_, err = n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1,
		Compiled: compiled,
		Hosts:    []string{edgeHost},
	})
	require.NoError(t, err)

	ask := func(path string, headers ...string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "http://"+edgeHost+path, bytes.NewReader([]byte("body")))
		r.Host = edgeHost
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
