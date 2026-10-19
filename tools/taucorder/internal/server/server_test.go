package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	authv2 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/build/v2/buildv2connect"
	registerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/register/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/register/v2/registerv2connect"
	"github.com/taubyte/tau/tools/taucorder/internal/fixture"
	"github.com/taubyte/tau/tools/taucorder/internal/policy"
	"github.com/taubyte/tau/tools/taucorder/internal/route"
	"github.com/taubyte/tau/tools/taucorder/internal/server"
)

type all struct{ pid peer.ID }

func (a all) Pick(context.Context, protocol.ID) ([]peer.ID, error) { return []peer.ID{a.pid}, nil }
func (all) Failed(protocol.ID, peer.ID)                            {}

type env struct {
	url   string
	cloud *fixture.Cloud
}

func start(t *testing.T, opts server.Options) env {
	t.Helper()
	client, node := fixture.Host(t), fixture.Host(t)
	fixture.Connect(t, client, node)
	cloud := fixture.NewCloud()
	fixture.Serve(t, node, cloud.Register)
	fixture.Serve(t, node, cloud.Build)
	tr := route.New(client, all{node.ID()})
	t.Cleanup(tr.Close)
	h, err := server.New(tr, opts)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewUnstartedServer(h)
	s.Config.Protocols = server.Protocols()
	s.Start()
	t.Cleanup(s.Close)
	return env{url: s.URL, cloud: cloud}
}

func TestReadIsForwardedInEveryProtocol(t *testing.T) {
	e := start(t, server.Options{})
	for name, opt := range map[string]connect.ClientOption{
		"connect":     connect.WithProtoJSON(),
		"grpc-web":    connect.WithGRPCWeb(),
		"connect-bin": connect.WithSendGzip(),
	} {
		r := registerv2connect.NewRegisterServiceClient(http.DefaultClient, e.url, opt)
		got, err := r.Environments(context.Background(), connect.NewRequest(&registerv2.ProjectRequest{Project: "p1"}))
		if err != nil {
			t.Fatal(name, err)
		}
		if len(got.Msg.GetEnvironments()) != 2 {
			t.Fatal(name, got.Msg)
		}
	}
	// A server stream, over grpc-web on HTTP/1.1.
	j := buildv2connect.NewJobServiceClient(http.DefaultClient, e.url, connect.WithGRPCWeb())
	s, err := j.Follow(context.Background(), connect.NewRequest(&buildv2.FollowRequest{JobId: "j1"}))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for s.Receive() {
		n++
	}
	if n != 3 || s.Err() != nil {
		t.Fatal(n, s.Err())
	}
}

func TestUpstreamErrorKeepsItsCode(t *testing.T) {
	e := start(t, server.Options{})
	r := registerv2connect.NewRegisterServiceClient(http.DefaultClient, e.url)
	_, err := r.Environments(context.Background(), connect.NewRequest(&registerv2.ProjectRequest{Project: "nope"}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatal(err)
	}
}

func TestWritesNeedTheFlag(t *testing.T) {
	e := start(t, server.Options{})
	r := registerv2connect.NewRegisterServiceClient(http.DefaultClient, e.url)
	_, err := r.Publish(context.Background(), connect.NewRequest(&registerv2.PublishRequest{Project: "p1"}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal(err)
	}
	if e.cloud.Count("RegisterService/Publish") != 0 {
		t.Fatal("a refused write reached the node")
	}

	e = start(t, server.Options{Allow: policy.Allow{Writes: true}})
	r = registerv2connect.NewRegisterServiceClient(http.DefaultClient, e.url)
	if _, err := r.Publish(context.Background(), connect.NewRequest(&registerv2.PublishRequest{Project: "p1"})); err != nil {
		t.Fatal(err)
	}
	if e.cloud.Count("RegisterService/Publish") != 1 {
		t.Fatal("write not forwarded")
	}
}

func TestSensitiveNeedsItsOwnFlag(t *testing.T) {
	e := start(t, server.Options{Allow: policy.Allow{Writes: true}})
	r := authv2connect.NewRepositoryServiceClient(http.DefaultClient, e.url)
	_, err := r.Get(context.Background(), connect.NewRequest(&authv2.RepositoryRef{}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal(err)
	}
	e = start(t, server.Options{Allow: policy.Allow{Sensitive: true}})
	r = authv2connect.NewRepositoryServiceClient(http.DefaultClient, e.url)
	_, err = r.Get(context.Background(), connect.NewRequest(&authv2.RepositoryRef{}))
	// Allowed through; nobody serves auth in this fixture.
	if connect.CodeOf(err) != connect.CodeUnavailable && connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatal(err)
	}
}

func TestUnknownMethod(t *testing.T) {
	e := start(t, server.Options{})
	resp := post(t, e.url+"/tau.register.v2.RegisterService/Nope", "", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatal(resp.Status)
	}
	resp = post(t, e.url+"/notapath", "", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatal(resp.Status)
	}
	resp = post(t, e.url+"/tau.register.v2.RegisterService/Nope", "", "application/json")
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatal(resp.Status)
	}
}

func post(t *testing.T, url, origin, ctype string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader("{}"))
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

const envPath = "/tau.register.v2.RegisterService/Environments"

func TestOrigins(t *testing.T) {
	e := start(t, server.Options{Origins: []string{"http://localhost:5173"}})

	if r := post(t, e.url+envPath, "http://evil.example", ""); r.StatusCode != http.StatusForbidden {
		t.Fatal("foreign origin:", r.Status)
	}
	if r := post(t, e.url+envPath, "http://evil.example", "application/json"); r.StatusCode != http.StatusForbidden {
		t.Fatal("foreign origin, connect:", r.Status)
	}

	req, _ := http.NewRequest(http.MethodOptions, e.url+envPath, nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || resp.Header.Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Fatal(resp.Status, resp.Header)
	}
	if !strings.Contains(resp.Header.Get("Access-Control-Allow-Headers"), "Connect-Protocol-Version") {
		t.Fatal(resp.Header)
	}

	r := post(t, e.url+envPath, "http://localhost:5173", "application/json")
	// Forwarded (the fake answers not_found for an empty project), with CORS.
	if r.StatusCode == http.StatusForbidden || r.Header.Get("Access-Control-Allow-Origin") == "" {
		t.Fatal(r.Status, r.Header)
	}
}

func TestWildcardOriginIsRefused(t *testing.T) {
	if _, err := server.New(http.DefaultTransport, server.Options{Origins: []string{"*"}}); err == nil {
		t.Fatal("accepted *")
	}
}

func TestHostHeader(t *testing.T) {
	e := start(t, server.Options{Hosts: []string{"console.lan"}})
	for host, want := range map[string]int{
		"attacker.example":      http.StatusForbidden,
		"attacker.example:8080": http.StatusForbidden,
		"localhost:1":           http.StatusOK,
		"[::1]:1":               http.StatusOK,
		"console.lan":           http.StatusOK,
		"console.lan:9":         http.StatusOK,
	} {
		req, _ := http.NewRequest(http.MethodPost, e.url+envPath, strings.NewReader(`{"project":"p1"}`))
		req.Host = host
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Error(host, resp.Status)
		}
	}
}

type failing struct{ err error }

func (f failing) RoundTrip(r *http.Request) (*http.Response, error) { return nil, f.err }

func TestUnavailableAndCanceled(t *testing.T) {
	h, err := server.New(failing{route.ErrNoPeer}, server.Options{})
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(h)
	defer s.Close()
	r := registerv2connect.NewRegisterServiceClient(http.DefaultClient, s.URL)
	_, err = r.Resolve(context.Background(), connect.NewRequest(&registerv2.ResolveRequest{}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, envPath, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "127.0.0.1:1"
	h2, _ := server.New(failing{context.Canceled}, server.Options{})
	h2.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "canceled") {
		t.Fatal(rec.Body.String())
	}
}
