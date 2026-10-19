package route_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/build/v2/buildv2connect"
	registerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/register/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/register/v2/registerv2connect"
	"github.com/taubyte/tau/tools/taucorder/internal/fixture"
	"github.com/taubyte/tau/tools/taucorder/internal/route"
)

type picker struct {
	mu     sync.Mutex
	peers  map[protocol.ID][]peer.ID
	err    error
	failed []peer.ID
}

func (p *picker) Pick(_ context.Context, id protocol.ID) ([]peer.ID, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.peers[id], p.err
}

func (p *picker) Failed(_ protocol.ID, pid peer.ID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failed = append(p.failed, pid)
}

func TestForwardsUnaryAndStreams(t *testing.T) {
	client, node := fixture.Host(t), fixture.Host(t)
	fixture.Connect(t, client, node)
	cloud := fixture.NewCloud()
	reg := fixture.Serve(t, node, cloud.Register)
	bld := fixture.Serve(t, node, cloud.Build)

	tr := route.New(client, &picker{peers: map[protocol.ID][]peer.ID{reg: {node.ID()}, bld: {node.ID()}}})
	defer tr.Close()

	r := registerv2connect.NewRegisterServiceClient(tr.Client(), route.BaseURL)
	got, err := r.Environments(context.Background(), connect.NewRequest(&registerv2.ProjectRequest{Project: "p1"}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Msg.GetEnvironments(), ",") != "main,staging" {
		t.Fatal(got.Msg)
	}
	// Twice: the second call reuses the held stream.
	if _, err := r.Environments(context.Background(), connect.NewRequest(&registerv2.ProjectRequest{Project: "p1"})); err != nil {
		t.Fatal(err)
	}

	j := buildv2connect.NewJobServiceClient(tr.Client(), route.BaseURL, connect.WithGRPC())
	s, err := j.Follow(context.Background(), connect.NewRequest(&buildv2.FollowRequest{JobId: "j1"}))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for s.Receive() {
		n++
	}
	if s.Err() != nil || n != 3 {
		t.Fatal(n, s.Err())
	}
}

func proto(t *testing.T, path string) protocol.ID {
	id, err := route.Protocol(path)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestNoPeer(t *testing.T) {
	tr := route.New(fixture.Host(t), &picker{})
	req, _ := http.NewRequest(http.MethodPost, route.BaseURL+"/tau.register.v2.RegisterService/Resolve", strings.NewReader("x"))
	if _, err := tr.RoundTrip(req); !errors.Is(err, route.ErrNoPeer) {
		t.Fatal(err)
	}
}

func TestPickError(t *testing.T) {
	boom := errors.New("boom")
	tr := route.New(fixture.Host(t), &picker{err: boom})
	req, _ := http.NewRequest(http.MethodPost, route.BaseURL+"/tau.register.v2.RegisterService/Resolve", nil)
	if _, err := tr.RoundTrip(req); !errors.Is(err, boom) {
		t.Fatal(err)
	}
}

func TestBadPath(t *testing.T) {
	tr := route.New(fixture.Host(t), &picker{})
	req, _ := http.NewRequest(http.MethodPost, route.BaseURL+"/nope", io.NopCloser(strings.NewReader("")))
	if _, err := tr.RoundTrip(req); err == nil {
		t.Fatal("routed a path with no method")
	}
}

func TestInvalidPeer(t *testing.T) {
	id := proto(t, "/tau.register.v2.RegisterService/Resolve")
	tr := route.New(fixture.Host(t), &picker{peers: map[protocol.ID][]peer.ID{id: {peer.ID("")}}})
	req, _ := http.NewRequest(http.MethodPost, route.BaseURL+"/tau.register.v2.RegisterService/Resolve", nil)
	if _, err := tr.RoundTrip(req); err == nil {
		t.Fatal("dialled an empty peer id")
	}
}

func TestUnreachablePeerIsReported(t *testing.T) {
	client := fixture.Host(t)
	gone := fixture.Host(t) // never connected, no addresses known to client
	id := proto(t, "/tau.register.v2.RegisterService/Resolve")
	p := &picker{peers: map[protocol.ID][]peer.ID{id: {gone.ID()}}}
	tr := route.New(client, p)
	defer tr.Close()
	r := registerv2connect.NewRegisterServiceClient(tr.Client(), route.BaseURL)
	ctx, cancel := context.WithTimeout(context.Background(), 2e9)
	defer cancel()
	if _, err := r.Resolve(ctx, connect.NewRequest(&registerv2.ResolveRequest{})); err == nil {
		t.Fatal("reached a peer with no address")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.failed) != 1 || p.failed[0] != gone.ID() {
		t.Fatal(p.failed)
	}
}
