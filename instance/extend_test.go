package instance

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/taubyte/tau/pkg/containers"
	"github.com/taubyte/tau/pkg/forge/drivers"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/rpc"
	echov1 "github.com/taubyte/tau/p2p/rpc/testdata/gen/echo/v1"
	"github.com/taubyte/tau/p2p/rpc/testdata/gen/echo/v1/echov1connect"
	pkgregister "github.com/taubyte/tau/pkg/register"
	statsService "github.com/taubyte/tau/services/stats"
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
	n, _, _ := makeNode(t, Config{Services: []string{Stats}}, r.service())
	require.NoError(t, n.Start(t.Context()))
	require.Nil(t, r.host)
	require.Nil(t, n.Handler(), "nothing it serves runs here")
}

func TestAServiceIsGivenItsInstance(t *testing.T) {
	r := &recording{}
	n, h, _ := makeNode(t, Config{Services: []string{Stats, addedName}}, r.service())
	require.NoError(t, n.Start(t.Context()))
	host := r.host
	require.NotNil(t, host)

	require.Equal(t, h, host.Peer().Host())
	require.NotNil(t, host.Peer().Messaging())
	require.NotEmpty(t, host.Root())

	// WHAT RUNS BESIDE IT, a service reaches by the type it wants.
	reg, ok := service.Get[*pkgregister.Register](host)
	require.False(t, ok, "no register runs here")
	require.Nil(t, reg)
	_, ok = service.Get[statsService.Service](host)
	require.True(t, ok, "stats runs here")
	_, ok = service.Get[started](host)
	require.True(t, ok, "and so does the service itself, once started")

	// It is reported as running, and it is given the finder every service here finds
	// a peer through.
	require.Contains(t, n.serving(), addedName)
	_, ok = service.Get[service.Finder](host)
	require.True(t, ok)

	require.NoError(t, n.Close())
	select {
	case <-r.closer.done:
	default:
		t.Fatal("the instance closed without closing the service")
	}
}

func TestAServiceListensOnItsInstancesPeer(t *testing.T) {
	var server *rpc.Server
	svc := started{
		start: func(_ context.Context, h Host) error {
			mux := http.NewServeMux()
			mux.Handle(echov1connect.NewEchoServiceHandler(echoes{}))
			var err error
			server, err = rpc.Listen(context.Background(), h.Peer().Host(), echoProtocol, mux)
			return err
		},
		close: func() error { return server.Close() },
	}

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
	require.Nil(t, n.Handler(), "no gateway, nothing public")
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
			n, _, _ := makeNode(t, Config{Services: []string{addedName}, Sections: map[string]any{addedName: entry}}, r.service())
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
		n, _, _ := makeNode(t, Config{Services: []string{addedName}, Sections: map[string]any{addedName: map[string]any{"port": "many"}}}, r.service())
		require.NoError(t, n.Start(t.Context()))
		var got settings
		require.ErrorContains(t, r.host.Config(&got), addedName)
	})

	t.Run("the cloud-wide keys it declares come from the node, once", func(t *testing.T) {
		r := &recording{}
		n, _, _ := makeNode(t, Config{
			Services:  []string{addedName},
			CloudFqdn: "tau.example.io",
			Tenancy:   []string{"github.com/acme"},
			Forges:    []drivers.Forge{{Host: "git.example.com", Driver: drivers.Gitea}},
			Container: containers.Declared{Backend: containers.Docker},
			Sections:  map[string]any{addedName: map[string]any{"port": 25}},
		}, r.service())
		require.NoError(t, n.Start(t.Context()))
		var got cloudAware
		require.NoError(t, r.host.Config(&got))
		require.Equal(t, cloudAware{
			CloudFqdn: "tau.example.io",
			Tenancy:   []string{"github.com/acme"},
			Forges:    []drivers.Forge{{Host: "git.example.com", Driver: drivers.Gitea}},
			Container: containers.Declared{Backend: containers.Docker},
			Port:      25,
		}, got)

		own := settings{Port: 1}
		require.NoError(t, r.host.Config(&own), "a service that declares none of them is not handed them")
		require.Equal(t, settings{Port: 25}, own)
	})

	t.Run("a cloud-wide key the node leaves empty keeps the default", func(t *testing.T) {
		r := &recording{}
		n, _, _ := makeNode(t, Config{Services: []string{addedName}}, r.service())
		require.NoError(t, n.Start(t.Context()))
		got := cloudAware{CloudFqdn: "default.example.io"}
		require.NoError(t, r.host.Config(&got))
		require.Equal(t, cloudAware{CloudFqdn: "default.example.io"}, got)
	})
}

// cloudAware is a service's configuration that reads cloud-wide keys beside its own.
type cloudAware struct {
	CloudFqdn string              `yaml:"cloudfqdn"`
	Tenancy   []string            `yaml:"tenancy"`
	Forges    []drivers.Forge     `yaml:"forges"`
	Container containers.Declared `yaml:"container"`
	Port      int                 `yaml:"port"`
}

func TestConfigForAServiceNotAddedIsRefused(t *testing.T) {
	_, err := New(mockPeer(t), rooted(t, Config{Sections: map[string]any{"mail": map[string]any{}}}))
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
	// STORAGE STARTS ONCE DISCOVERY HAS SETTLED ON ITS NODE, after one liveness
	// window here rather than a minute.
	cfg := Config{Services: []string{Register, Stats, storage.Descriptor.Name}}
	cfg.Sections = map[string]any{statsService.Descriptor.Name: statsService.Config{Stabilize: statsService.Config{}.Freshness()}}
	n, err := New(p, rooted(t, cfg))
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })
	require.NoError(t, n.Start(ctx))

	// FROM ANOTHER PEER, the way any service reaches it. The region's settings are
	// read from the cm the instance runs beside storage, which is why a write can be
	// acknowledged at all.
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
