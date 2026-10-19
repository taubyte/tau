package node

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	echov1 "github.com/taubyte/tau/p2p/rpc/testdata/gen/echo/v1"
	"github.com/taubyte/tau/p2p/rpc/testdata/gen/echo/v1/echov1connect"
)

const addedName = "added"

var echoProtocol = rpc.Protocol(echov1connect.EchoServiceName)

type echoes struct {
	echov1connect.UnimplementedEchoServiceHandler
}

func (echoes) Unary(_ context.Context, req *connect.Request[echov1.EchoRequest]) (*connect.Response[echov1.EchoResponse], error) {
	return connect.NewResponse(&echov1.EchoResponse{Payload: req.Msg.GetPayload()}), nil
}

type closed struct{ done chan struct{} }

func (c *closed) Close() error { close(c.done); return nil }

// recording is a service that keeps the Host it was started with.
type recording struct {
	host   Host
	closer *closed
}

func (r *recording) service() Service {
	return Service{Name: addedName, Start: func(_ context.Context, h Host) (io.Closer, error) {
		r.host = h
		r.closer = &closed{done: make(chan struct{})}
		return r.closer, nil
	}}
}

func TestWhatWithServiceRefuses(t *testing.T) {
	start := func(context.Context, Host) (io.Closer, error) { return nil, nil }
	for name, opts := range map[string][]Option{
		"no name":    {WithService(Service{Start: start})},
		"no start":   {WithService(Service{Name: addedName})},
		"built-in":   {WithService(Service{Name: Register, Start: start})},
		"twice":      {WithService(Service{Name: addedName, Start: start}), WithService(Service{Name: addedName, Start: start})},
		"misspelled": {WithService(Service{Name: addedName, Start: start})},
	} {
		t.Run(name, func(t *testing.T) {
			mn := mocknet.New()
			t.Cleanup(func() { mn.Close() })
			services := []string{addedName}
			if name == "misspelled" {
				services = []string{"addd"}
			}
			_, err := New(Config{Services: services}, append(opts, WithHost(newHost(t, mn)))...)
			require.Error(t, err)
		})
	}
}

func TestAnEmptyServiceListRunsAddedServicesToo(t *testing.T) {
	r := &recording{}
	n, _, _ := makeNode(t, Config{}, WithService(r.service()))
	require.Contains(t, n.Services(), addedName)
	require.NoError(t, n.Start(t.Context()))
	require.NotNil(t, r.host)
}

func TestAnAddedServiceTheConfigDoesNotNameDoesNotStart(t *testing.T) {
	r := &recording{}
	n, _, _ := makeNode(t, Config{Services: []string{Discovery}}, WithService(r.service()))
	require.NoError(t, n.Start(t.Context()))
	require.Nil(t, r.host)
	require.Nil(t, n.Handler(), "nothing it serves runs here")
}

func TestAnAddedServiceGetsTheNodesResources(t *testing.T) {
	r := &recording{}
	n, h, factory := makeNode(t, Config{Services: []string{Discovery, Secrets, addedName}},
		WithService(r.service()), WithRoot(t.TempDir()))
	require.NoError(t, n.Start(t.Context()))
	host := r.host
	require.NotNil(t, host)

	require.Equal(t, addedName, host.Name())
	require.Equal(t, h, host.Peer())
	require.NotNil(t, host.PubSub())
	require.NotEmpty(t, host.Root())
	require.NotNil(t, host.Logger())

	// ITS DATABASES ARE UNDER ITS NAME, so it cannot open a built-in one.
	db, err := host.Database("state")
	require.NoError(t, err)
	require.NoError(t, db.Put(t.Context(), "/k", []byte("v")))
	_, err = host.Database("")
	require.NoError(t, err)
	opened, _ := factory.counts()
	require.Contains(t, opened, addedName+"/state")
	require.Contains(t, opened, addedName)

	// The vault is the node's own here, and one reach however often asked.
	vault := host.Secrets()
	require.Same(t, vault, host.Secrets())
	require.NoError(t, vault.Set(t.Context(), "p", "e", "name", []byte("value")))
	names, err := vault.List(t.Context(), "p", "e")
	require.NoError(t, err)
	require.Equal(t, []string{"name"}, names)

	// It is reported as running, and discovery finds it.
	require.Contains(t, n.serving(), addedName)
	require.Eventually(t, func() bool { return len(host.Peers(addedName)) == 1 }, 10*time.Second, 20*time.Millisecond)
	require.Equal(t, h.ID(), host.Peers(addedName)[0])

	require.NoError(t, n.Close())
	select {
	case <-r.closer.done:
	default:
		t.Fatal("the node closed without closing what the service returned")
	}
	_, closedCount := factory.counts()
	require.Positive(t, closedCount)
}

func TestAnAddedServiceAnswersOverRPCAndHTTP(t *testing.T) {
	var host Host
	svc := Service{Name: addedName, Start: func(_ context.Context, h Host) (io.Closer, error) {
		host = h
		mux := http.NewServeMux()
		mux.Handle(echov1connect.NewEchoServiceHandler(echoes{}))
		if err := h.Listen(echoProtocol, mux); err != nil {
			return nil, err
		}
		return nil, h.Handle("/added/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			io.WriteString(w, "added")
		}))
	}}

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h := newHost(t, mn)
	n, err := New(Config{Services: []string{addedName}}, WithHost(h), WithService(svc))
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })
	require.NoError(t, n.Start(t.Context()))

	caller := newHost(t, mn)
	client, err := rpc.NewClient(caller, h.ID(), echoProtocol)
	require.NoError(t, err)
	defer client.Close()
	echo := echov1connect.NewEchoServiceClient(client.HTTP(), client.URL())
	resp, err := echo.Unary(t.Context(), connect.NewRequest(&echov1.EchoRequest{Payload: []byte("hi")}))
	require.NoError(t, err)
	require.Equal(t, []byte("hi"), resp.Msg.GetPayload())

	handler := n.Handler()
	require.NotNil(t, handler)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://x/added/thing", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "added", w.Body.String())

	// Whatever no added pattern matches is the gateway's, and there is none.
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://x/other", nil))
	require.Equal(t, http.StatusNotFound, w.Code)

	// A conflicting or nil handler is an error rather than a panic.
	require.Error(t, host.Handle("/added/", http.NotFoundHandler()))
	require.Error(t, host.Handle("/nil/", nil))

	// A node without a discovery view knows nobody.
	require.Nil(t, host.Peers(addedName))
	// Nor any database factory.
	_, err = host.Database("x")
	require.ErrorIs(t, err, ErrNoDatabases)
	// A second listener on the same protocol is refused.
	require.Error(t, host.Listen(echoProtocol, http.NotFoundHandler()))
}

func TestAddedPatternsAreServedInFrontOfTheGateway(t *testing.T) {
	svc := Service{Name: addedName, Start: func(_ context.Context, h Host) (io.Closer, error) {
		return nil, h.Handle("mail.example.com/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			io.WriteString(w, "added")
		}))
	}}
	n, _, _ := makeNode(t, Config{}, WithService(svc))
	require.NoError(t, n.Start(t.Context()))

	w := httptest.NewRecorder()
	n.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://mail.example.com/x", nil))
	require.Equal(t, "added", w.Body.String())

	w = httptest.NewRecorder()
	n.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://nobody.example.com/", nil))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.NotEqual(t, "added", w.Body.String())
}

func TestAnAddedServiceThatFailsToStartFailsTheNode(t *testing.T) {
	refused := errors.New("refused")
	svc := Service{Name: addedName, Start: func(context.Context, Host) (io.Closer, error) { return nil, refused }}
	n, _, _ := makeNode(t, Config{Services: []string{addedName}}, WithService(svc))
	err := n.Start(t.Context())
	require.ErrorIs(t, err, refused)
	require.Contains(t, err.Error(), addedName)
}

// TestAnAddedServiceDecodesItsOwnConfig: an added service's entry in
// Config.Added reaches it through Host.Config by its yaml tags, whether the
// entry came from a file (generic values) or from Go (the service's struct),
// and a service with no entry keeps its defaults.
func TestAnAddedServiceDecodesItsOwnConfig(t *testing.T) {
	type settings struct {
		Host  string   `yaml:"host"`
		Hosts []string `yaml:"hosts"`
		Port  int      `yaml:"port"`
	}
	for name, entry := range map[string]any{
		"from a file": map[string]any{"host": "mail.example.com", "hosts": []any{"a", "b"}, "port": 25},
		"from go":     settings{Host: "mail.example.com", Hosts: []string{"a", "b"}, Port: 25},
	} {
		t.Run(name, func(t *testing.T) {
			r := &recording{}
			n, _, _ := makeNode(t, Config{Services: []string{addedName}, Added: map[string]any{addedName: entry}}, WithService(r.service()))
			require.NoError(t, n.Start(t.Context()))
			got := settings{Port: 1}
			require.NoError(t, r.host.Config(&got))
			require.Equal(t, settings{Host: "mail.example.com", Hosts: []string{"a", "b"}, Port: 25}, got)
		})
	}

	t.Run("no entry keeps the defaults", func(t *testing.T) {
		r := &recording{}
		n, _, _ := makeNode(t, Config{Services: []string{addedName}}, WithService(r.service()))
		require.NoError(t, n.Start(t.Context()))
		got := settings{Port: 1}
		require.NoError(t, r.host.Config(&got))
		require.Equal(t, settings{Port: 1}, got)
	})

	t.Run("an entry of the wrong shape is an error", func(t *testing.T) {
		r := &recording{}
		n, _, _ := makeNode(t, Config{Services: []string{addedName}, Added: map[string]any{addedName: map[string]any{"port": "many"}}}, WithService(r.service()))
		require.NoError(t, n.Start(t.Context()))
		var got settings
		require.ErrorContains(t, r.host.Config(&got), addedName)
	})

}

func TestConfigForAServiceNotAddedIsRefused(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	_, err := New(Config{Added: map[string]any{"mail": map[string]any{}}}, WithHost(newHost(t, mn)))
	require.ErrorContains(t, err, `"mail"`)
}
