package instance

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/rpc"
	echov1 "github.com/taubyte/tau/p2p/rpc/testdata/gen/echo/v1"
	"github.com/taubyte/tau/p2p/rpc/testdata/gen/echo/v1/echov1connect"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/discovery"
	"github.com/taubyte/tau/services/storage"
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

func (r *recording) service() Option {
	return withService(addedName, started{
		start: func(_ context.Context, h Host) error {
			r.host = h
			r.closer = &closed{done: make(chan struct{})}
			return nil
		},
		close: func() error { return r.closer.Close() },
	})
}

func TestWhatWithServiceRefuses(t *testing.T) {
	for name, opts := range map[string][]Option{
		"no name":    {withService("", started{})},
		"nil":        {withService(addedName, nil)},
		"built-in":   {withService(Register, started{})},
		"shipped":    {withService(storage.Descriptor.Name, started{})},
		"twice":      {withService(addedName, started{}), withService(addedName, started{})},
		"misspelled": {withService(addedName, started{})},
	} {
		t.Run(name, func(t *testing.T) {
			services := []string{addedName}
			if name == "misspelled" {
				services = []string{"addd"}
			}
			_, err := New(mockPeer(t), rooted(t, Config{Services: services}), opts...)
			require.Error(t, err)
		})
	}
}

func TestAnEmptyServiceListRunsAddedServicesToo(t *testing.T) {
	r := &recording{}
	n, _, _ := makeNode(t, Config{}, r.service())
	require.Contains(t, n.Services(), addedName)
	require.NoError(t, n.Start(t.Context()))
	require.NotNil(t, r.host)
}

func TestAnAddedServiceTheConfigDoesNotNameDoesNotStart(t *testing.T) {
	r := &recording{}
	n, _, _ := makeNode(t, Config{Services: []string{Discovery}}, r.service())
	require.NoError(t, n.Start(t.Context()))
	require.Nil(t, r.host)
	require.Nil(t, n.Handler(), "nothing it serves runs here")
}

func TestAnAddedServiceGetsTheNodesResources(t *testing.T) {
	r := &recording{}
	n, h, factory := makeNode(t, Config{Services: []string{Discovery, Secrets, addedName}},
		r.service())
	require.NoError(t, n.Start(t.Context()))
	host := r.host
	require.NotNil(t, host)

	require.Equal(t, addedName, host.Name())
	require.Equal(t, h, host.Peer().Host())
	require.NotNil(t, host.Peer().Messaging())

	// WHAT RUNS BESIDE IT, a service reaches by the type it wants.
	reg, ok := service.Get[*pkgregister.Register](host)
	require.False(t, ok, "no register runs here")
	require.Nil(t, reg)
	_, ok = service.Get[*discovery.Service](host)
	require.True(t, ok, "discovery runs here")
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
	svc := started{start: func(_ context.Context, h Host) error {
		host = h
		mux := http.NewServeMux()
		mux.Handle(echov1connect.NewEchoServiceHandler(echoes{}))
		if err := h.Listen(echoProtocol, mux); err != nil {
			return err
		}
		return h.Handle("/added/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			io.WriteString(w, "added")
		}))
	}}

	p := mockPeer(t)
	n, err := New(p, rooted(t, Config{Services: []string{addedName}}), withService(addedName, svc))
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })
	require.NoError(t, n.Start(t.Context()))

	client, err := rpc.NewClient(callerOf(t, p), p.ID(), echoProtocol)
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
	// A second listener on the same protocol is refused.
	require.Error(t, host.Listen(echoProtocol, http.NotFoundHandler()))
}

func TestAddedPatternsAreServedInFrontOfTheGateway(t *testing.T) {
	svc := started{start: func(_ context.Context, h Host) error {
		return h.Handle("mail.example.com/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			io.WriteString(w, "added")
		}))
	}}
	n, _, _ := makeNode(t, Config{}, withService(addedName, svc))
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
	svc := started{start: func(context.Context, Host) error { return refused }}
	n, _, _ := makeNode(t, Config{Services: []string{addedName}}, withService(addedName, svc))
	err := n.Start(t.Context())
	require.ErrorIs(t, err, refused)
	require.Contains(t, err.Error(), addedName)
}

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
			n, _, _ := makeNode(t, Config{Services: []string{addedName}, Added: map[string]any{addedName: entry}}, r.service())
			require.NoError(t, n.Start(t.Context()))
			got := settings{Port: 1}
			require.NoError(t, r.host.Config(&got))
			require.Equal(t, settings{Host: "mail.example.com", Hosts: []string{"a", "b"}, Port: 25}, got)
		})
	}

	t.Run("no entry keeps the defaults", func(t *testing.T) {
		r := &recording{}
		n, _, _ := makeNode(t, Config{Services: []string{addedName}}, r.service())
		require.NoError(t, n.Start(t.Context()))
		got := settings{Port: 1}
		require.NoError(t, r.host.Config(&got))
		require.Equal(t, settings{Port: 1}, got)
	})

	t.Run("an entry of the wrong shape is an error", func(t *testing.T) {
		r := &recording{}
		n, _, _ := makeNode(t, Config{Services: []string{addedName}, Added: map[string]any{addedName: map[string]any{"port": "many"}}}, r.service())
		require.NoError(t, n.Start(t.Context()))
		var got settings
		require.ErrorContains(t, r.host.Config(&got), addedName)
	})

}

func TestConfigForAServiceNotAddedIsRefused(t *testing.T) {
	_, err := New(mockPeer(t), rooted(t, Config{Added: map[string]any{"mail": map[string]any{}}}))
	require.ErrorContains(t, err, `"mail"`)
}

// started is a service a test writes as functions.
type started struct {
	start func(context.Context, Host) error
	close func() error
}

func (s started) Start(ctx context.Context, h Host) error {
	if s.start == nil {
		return nil
	}
	return s.start(ctx, h)
}

func (s started) Close() error {
	if s.close == nil {
		return nil
	}
	return s.close()
}

func TestStorageRunsBesideTheServicesItReaches(t *testing.T) {
	ctx := t.Context()
	p := mockPeer(t)
	n, err := New(p, rooted(t, Config{Services: []string{Register, Discovery, storage.Descriptor.Name}}))
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })
	require.NoError(t, n.Start(ctx))

	// FROM ANOTHER PEER, the way any service reaches it. The fleet is read from the
	// register on the instance, which is why a write can be acknowledged at all.
	client, err := storage.NewClient(callerOf(t, p), p.ID())
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	at, err := storage.NewAddress("shop", "prod", storage.AssetsBucket, "QmPackage")
	require.NoError(t, err)
	v, err := client.Put(ctx, at, "job-1", storage.Object{}, strings.NewReader("an artifact"))
	require.NoError(t, err)
	r, err := client.Read(ctx, v.Record.RootCID)
	require.NoError(t, err)
	body, err := io.ReadAll(r)
	require.NoError(t, r.Close())
	require.NoError(t, err)
	require.Equal(t, "an artifact", string(body))
}
