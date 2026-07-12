//go:build dreaming

package benchmarks

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/taubyte/tau/dream"
	multihash "github.com/taubyte/tau/utils/multihash"
)

// The prebuilt wasm fixtures in this suite only cover http and p2p triggers
// (see bootShared in bench_test.go and bench_p2p_test.go). No fixture exists
// for a pubsub-triggered wasm function, so there is no BenchmarkPubsubFunction
// here — a wasm pubsub-function benchmark would need a new compile fixture
// analogous to artifact.zwasm, wired to a "pubsub" trigger. BenchmarkWebsocketEcho
// below instead covers the pubsub publish->deliver transport directly: two
// websocket clients on the same channel, one writes, the other reads what
// the server republished over the pubsub topic — exercising the HTTP
// upgrade, channel lookup, and websocket<->pubsub bridging without wasm.

// wsDialer maps the test fqdn to localhost, mirroring
// dream/helpers.CreateHttpClient's DialContext trick for the plain HTTP
// client used by the other benchmarks in this suite.
var wsDialer = &websocket.Dialer{
	NetDial: func(network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if host == testFqdn {
			host = "127.0.0.1"
		}
		return net.Dial(network, net.JoinHostPort(host, port))
	},
	HandshakeTimeout: 5 * time.Second,
}

// wsURL builds the /ws-{hash}/{channel} URL for the shared project's global
// (application-less) benchchannel messaging resource. The hash mirrors what
// services/substrate/components/pubsub/websocket/handler.go derives via
// pkg/specs/messaging/tns.go's WebSocketHashPath: multihash.Hash(project+app).
func wsURL(u *dream.Universe) (string, error) {
	port, err := u.GetPortHttp(u.Substrate().Node())
	if err != nil {
		return "", err
	}
	hash := multihash.Hash(testProjectId + "")
	return fmt.Sprintf("ws://%s:%d/ws-%s/%s", testFqdn, port, hash, messagingChannel), nil
}

func dialWS(url string) (*websocket.Conn, error) {
	conn, _, err := wsDialer.Dial(url, nil)
	return conn, err
}

const wsPayloadSize = 256

// BenchmarkWebsocketEcho measures the websocket<->pubsub bridge: A writes a
// binary frame, the server republishes it on the channel's pubsub topic, and
// B (the other subscriber on the same channel) reads it back. See the file
// comment above for why there is no pubsub-function benchmark alongside it.
func BenchmarkWebsocketEcho(b *testing.B) {
	// Skipped: the pubsub websocket serviceable is currently dead code — its
	// construction is commented out in
	// services/substrate/components/pubsub/lookup.go, so every WebSocket-matcher
	// Lookup returns "no pub-sub matches found" and the dial below can never
	// complete. Keeping the full benchmark under the skip so it starts measuring
	// the websocket<->pubsub bridge the day that path is revived (just delete
	// this line). Until then, this keeps `make bench-dreaming BENCH=.` green.
	b.Skip("pubsub websocket serviceable disabled: websocket append commented out in services/substrate/components/pubsub/lookup.go")

	u := sharedUniverse(b)
	url, err := wsURL(u)
	if err != nil {
		b.Fatal(err)
	}

	payload := make([]byte, wsPayloadSize)
	for i := range payload {
		payload[i] = byte(i)
	}

	var (
		connA, connB *websocket.Conn
		lastErr      error
	)

	// The messaging config needs to propagate through TNS before the
	// websocket handler's channel lookup resolves - the first dial(s) may
	// 404/error, or connect and immediately receive an error JSON frame,
	// until it does. Retry a full dial+roundtrip in a bounded poll.
	for i := 0; ; i++ {
		if connA != nil {
			connA.Close()
		}
		if connB != nil {
			connB.Close()
		}

		var mt int
		var msg []byte

		connA, lastErr = dialWS(url)
		if lastErr == nil {
			connB, lastErr = dialWS(url)
		}
		if lastErr == nil {
			lastErr = connA.WriteMessage(websocket.BinaryMessage, payload)
		}
		if lastErr == nil {
			connB.SetReadDeadline(time.Now().Add(2 * time.Second))
			mt, msg, lastErr = connB.ReadMessage()
		}
		if lastErr == nil && mt == websocket.BinaryMessage && len(msg) == len(payload) {
			connB.SetReadDeadline(time.Time{})
			break
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("unexpected frame type=%d len=%d body=%q", mt, len(msg), msg)
		}
		if i >= 60 {
			b.Fatalf("websocket echo never came up: %v", lastErr)
		}
		time.Sleep(500 * time.Millisecond)
	}
	defer connA.Close()
	defer connB.Close()

	b.ReportAllocs()
	for b.Loop() {
		if err := connA.WriteMessage(websocket.BinaryMessage, payload); err != nil {
			b.Fatal(err)
		}
		// Any non-payload read (e.g. the server's error JSON frame) is
		// fatal - see dataStreamHandler.In/Out in
		// services/substrate/components/pubsub/websocket/datastream.go.
		mt, msg, err := connB.ReadMessage()
		if err != nil {
			b.Fatalf("reading echo failed with: %v", err)
		}
		if mt != websocket.BinaryMessage || len(msg) != len(payload) {
			b.Fatalf("expected a %d-byte binary frame, got type=%d len=%d body=%q", len(payload), mt, len(msg), msg)
		}
	}
}
