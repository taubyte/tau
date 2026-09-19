//go:build dreaming

package dream_test

import (
	"bytes"
	"context"
	"encoding/json"
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
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// project_test runs AUTHORED PROJECTS, compiled by tcc, in a universe.
//
// # Why these are yaml and not compiled objects
//
// Most fixtures in this package are the compiled object, written by hand. That
// is the right shape when what is under test is the runtime's reading of one
// field. It is the wrong shape for asking whether the PLATFORM works, because a
// hand-written object is a statement of what I believe the compiler produces —
// it keeps passing on the day the compiler produces something else, which is
// the day it matters.
//
// So these start where an author starts: a directory of yaml. tcc compiles it,
// the register publishes it, and the gateway is asked a question over HTTP. What
// a green test here says is that the whole path holds, from a file somebody
// wrote to bytes on a socket.
//
// # They start small on purpose
//
// `onefunction` is the smallest project that answers anything: one function, one
// zone, one route. Everything that goes wrong in it goes wrong for every project
// after it, so it is the one to read first when the corpus turns red.
//
// # The one substitution
//
// Every function here names a source repository nobody built, because building
// it needs a git server and a toolchain. So each is pointed at a component this
// tree already ships. THAT IS A DEPLOYMENT FACT AND NOT A SHAPE ONE: nothing
// about the project's structure is touched, and the artifact a real deploy would
// have produced is the one thing a universe cannot make for itself.

// project compiles one of the authored projects under projects/ and points every
// function at the component the caller names.
func project(t *testing.T, name, componentCID string) []byte {
	t.Helper()

	c, err := schema.New(
		schema.WithLocal(filepath.Join("projects", name)),
		schema.WithBranch("main"))
	require.NoError(t, err)

	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err)

	flat := obj.Flat()
	root, ok := flat["object"].(map[string]any)
	require.True(t, ok, "%s compiled to something with no object in it", name)

	var swapped int
	for _, group := range root {
		instances, ok := group.(map[string]any)
		if !ok {
			continue
		}
		for _, v := range instances {
			res, ok := v.(map[string]any)
			if !ok {
				continue
			}
			if _, isSource := res["source"]; !isSource {
				continue
			}
			res["source"] = map[string]any{"kind": "component", "component": componentCID}
			swapped++
		}
	}
	require.NotZero(t, swapped, "%s has nothing to run", name)

	raw, err := json.Marshal(flat)
	require.NoError(t, err)
	return raw
}

// running is a universe holding one project, and a way to ask it something.
type running struct {
	node *node.Node
	host string
}

// ask puts a request to the gateway the way the internet would.
func (r *running) ask(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, "http://"+r.host+path, bytes.NewReader([]byte("body")))
	req.Host = r.host
	w := httptest.NewRecorder()
	r.node.Handler().ServeHTTP(w, req)
	return w
}

// deploy starts a universe, publishes the named project into it, and waits for
// the node to be holding it.
func deploy(t *testing.T, name, host string, wasm []byte) *running {
	t.Helper()

	m, err := dream.New(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { m.Close() })

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

	cid, err := store.Put(t.Context(), bytes.NewReader(wasm))
	require.NoError(t, err)

	_, err = n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1,
		Compiled: project(t, name, cid), Hosts: []string{host},
	})
	require.NoError(t, err)

	return &running{node: n, host: host}
}

func handlerWASM(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile(filepath.Join("..", "..", "pkg", "invoke", "wasm", "fixtures", "handler.wasm"))
	require.NoError(t, err, "run `cargo component build --release` in pkg/invoke/wasm/fixtures/handler")
	return code
}

// TestTheSmallestProjectAnswers. One function, one zone, one route — authored as
// yaml, compiled by tcc, published, and asked a question over HTTP.
//
// THE STATUS IS THE PROOF AND NOT THE BODY. The component answers 200 to a GET
// and 201 to a POST, so a passing assertion here cannot be a constant somewhere
// in the path: the method the gateway parsed reached the guest and came back.
func TestTheSmallestProjectAnswers(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	r := deploy(t, "onefunction", "hello.example.com", handlerWASM(t))

	got := r.ask(t, http.MethodGet, "/")
	require.Equal(t, 200, got.Code, "a GET reached the function")
	require.Equal(t, "/", got.Header().Get("x-path"), "and the path it was asked for came back")

	posted := r.ask(t, http.MethodPost, "/orders")
	require.Equal(t, 201, posted.Code, "and a POST is a different answer from the same component")
	require.Equal(t, "/orders", posted.Header().Get("x-path"))
}
