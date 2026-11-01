//go:build dreaming

package dream_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node"
	nodeDream "github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/p2p/rpc"
	echov1 "github.com/taubyte/tau/p2p/rpc/testdata/gen/echo/v1"
	"github.com/taubyte/tau/p2p/rpc/testdata/gen/echo/v1/echov1connect"
)

type echoes struct {
	echov1connect.UnimplementedEchoServiceHandler
	peer string
}

func (e echoes) Unary(_ context.Context, req *connect.Request[echov1.EchoRequest]) (*connect.Response[echov1.EchoResponse], error) {
	return connect.NewResponse(&echov1.EchoResponse{Payload: req.Msg.GetPayload(), Note: e.peer}), nil
}

func TestAnAddedServiceJoinsAFleet(t *testing.T) {
	const name = "added"
	protocol := rpc.Protocol(echov1connect.EchoServiceName)

	added := node.WithService(node.Service{Name: name, Start: func(_ context.Context, h node.Host) (io.Closer, error) {
		mux := http.NewServeMux()
		mux.Handle(echov1connect.NewEchoServiceHandler(echoes{peer: h.Peer().ID().String()}))
		if err := h.Listen(protocol, mux); err != nil {
			return nil, err
		}
		return nil, h.Handle("/added/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			io.WriteString(w, h.Peer().ID().String())
		}))
	}})

	u, err := nodeDream.Start(t.Context(), t.Name(), nodeDream.Config{
		Copies:   2,
		Services: []string{node.Discovery, name},
		Options:  []node.Option{added},
	})
	require.NoError(t, err)
	defer u.Close()

	pids := u.Pids()
	require.Len(t, pids, 2)
	from, to := nodeOf(t, pids[0]), nodeOf(t, pids[1])

	require.Eventually(t, func() bool {
		return routes(from, name, pids[1])
	}, 30*time.Second, 50*time.Millisecond, "the first node finds the second running the added service")

	target, err := libp2ppeer.Decode(pids[1])
	require.NoError(t, err)
	client, err := rpc.NewClient(u.Peers()[0].Peer(), target, protocol)
	require.NoError(t, err)
	defer client.Close()

	resp, err := echov1connect.NewEchoServiceClient(client.HTTP(), client.URL()).
		Unary(t.Context(), connect.NewRequest(&echov1.EchoRequest{Payload: []byte("across")}))
	require.NoError(t, err)
	require.Equal(t, []byte("across"), resp.Msg.GetPayload())
	require.Equal(t, pids[1], resp.Msg.GetNote(), "the answer came from the node discovery named")

	server := httptest.NewServer(to.Handler())
	defer server.Close()
	res, err := http.Get(server.URL + "/added/x")
	require.NoError(t, err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, pids[1], string(body))
}

func routes(n *node.Node, service, peer string) bool {
	for _, row := range n.Discovery().Route(service, time.Minute) {
		if row.Peer == peer {
			return true
		}
	}
	return false
}
