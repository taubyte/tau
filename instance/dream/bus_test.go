//go:build dreaming

package dream_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	nodeDream "github.com/taubyte/tau/instance/dream"
)

// A browser opens a websocket at a name and receives what a workload publishes,
// with the browser on a node that runs no part of the workload. projects/with/bus
// is compiled.

// busFor is the label `live` in the project's own zone, complete without the
// cloud's fqdn.
const busFor = "live.example.com"

func TestABrowserAttachesAndAGuestReachesIt(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	cloud := deployFleetWith(t, 2, "with/bus", busFor, cacherWASM(t),
		map[string][]byte{"doorman": doormanWASM(t)})
	publishing, edge := cloud[0], cloud[1]

	// The browser attaches to the other node.
	srv := httptest.NewServer(edge.node.Handler())
	defer srv.Close()

	// deployFleet waited for resolution, so the first try should succeed; it retries
	// since resolvable and routable are two facts.
	var conn *websocket.Conn
	require.Eventually(t, func() bool {
		c, res, err := websocket.DefaultDialer.Dial(
			"ws://"+strings.TrimPrefix(srv.URL, "http://")+"/",
			http.Header{"Host": []string{busFor}})
		if err != nil {
			return false
		}
		require.Equal(t, http.StatusSwitchingProtocols, res.StatusCode)
		conn = c
		return true
	}, 60*time.Second, 500*time.Millisecond, "the project never reached the edge node")
	defer conn.Close()

	received := make(chan []byte, 4)
	go func() {
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				return
			}
			received <- message
		}
	}()

	// A gossipsub mesh needs both ends joined, so the publish is retried; 60s is a
	// report bound, not a raft budget. The publish goes through the gateway too: one
	// name carries `/broadcast` to the function and attaches everything else to the
	// bus.
	require.Eventually(t, func() bool {
		res := publishing.askCarrying(t, http.MethodPost, "/broadcast/live/", "deploy finished")
		require.Equal(t, 204, res.Code, res.Body.String())

		select {
		case got := <-received:
			require.Equal(t, "deploy finished", string(got))
			return true
		case <-time.After(250 * time.Millisecond):
			return false
		}
	}, 60*time.Second, 500*time.Millisecond, "a guest's message never reached the browser")
}

// doormanWASM is the route's guard: it denies by header and otherwise grants
// `subscribe` and never `publish`.
func doormanWASM(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile(filepath.Join("..", "..", "pkg", "invoke", "wasm", "fixtures", "doorman.wasm"))
	require.NoError(t, err, "run `cargo component build --release` in pkg/invoke/wasm/fixtures/doorman")
	return code
}

func TestAGuardRefusesAnAttachmentBeforeTheUpgrade(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	r := deployWith(t, "with/bus", busFor, cacherWASM(t),
		map[string][]byte{"doorman": doormanWASM(t)})

	srv := httptest.NewServer(r.node.Handler())
	defer srv.Close()

	// REFUSED, and it is the guard that refused: the header is the fixture's
	// own deny arm, and nothing else on this path reads it.
	_, res, err := websocket.DefaultDialer.Dial(
		"ws://"+strings.TrimPrefix(srv.URL, "http://")+"/",
		http.Header{"Host": []string{busFor}, "X-Deny": []string{"not on the list"}})
	require.Error(t, err, "the handshake must not complete")
	require.Equal(t, http.StatusForbidden, res.StatusCode)

	// AND THE SAME REQUEST WITHOUT IT ATTACHES, which is what makes the refusal
	// above a decision rather than a broken route.
	require.Eventually(t, func() bool {
		conn, res, err := websocket.DefaultDialer.Dial(
			"ws://"+strings.TrimPrefix(srv.URL, "http://")+"/",
			http.Header{"Host": []string{busFor}})
		if err != nil {
			return false
		}
		defer conn.Close()
		return res.StatusCode == http.StatusSwitchingProtocols
	}, 60*time.Second, 500*time.Millisecond, "the guard never admitted anyone")
}

func TestAPlainRequestToABusNameIsToldToUpgrade(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	r := deployWith(t, "with/bus", busFor, cacherWASM(t),
		map[string][]byte{"doorman": doormanWASM(t)})

	got := r.ask(t, http.MethodGet, "/")

	require.Equal(t, http.StatusUpgradeRequired, got.Code)
	require.Contains(t, got.Body.String(), "websocket")
}
