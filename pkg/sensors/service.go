package sensors

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	"connectrpc.com/connect"
	"github.com/taubyte/tau/p2p/peer"
	sensorsv1 "github.com/taubyte/tau/pkg/sensors/proto/gen/sensors/v1"
	sensorsv1connect "github.com/taubyte/tau/pkg/sensors/proto/gen/sensors/v1/sensorsv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

var DefaultPort = 4217

type Option func(*config) error

type config struct {
	port     int
	registry *Registry
}

func WithPort(port int) Option {
	return func(c *config) error {
		c.port = port
		return nil
	}
}

func WithRegistry(registry *Registry) Option {
	return func(c *config) error {
		if registry == nil {
			return errors.New("registry cannot be nil")
		}
		c.registry = registry
		return nil
	}
}

type Service struct {
	registry *Registry
	node     peer.Node
	path     string
	handler  http.Handler
	server   *http.Server
	listener net.Listener
}

var _ sensorsv1connect.SensorServiceHandler = (*Service)(nil)

func (s *Service) Registry() *Registry {
	return s.registry
}

func (s *Service) Path() string {
	return s.path
}

func (s *Service) Handler() http.Handler {
	return s.handler
}

func New(node peer.Node, options ...Option) (*Service, error) {
	cfg := &config{
		port: DefaultPort,
	}

	for _, opt := range options {
		if err := opt(cfg); err != nil {
			return nil, err
		}
	}

	ctx := node.Context()

	registry := cfg.registry
	if registry == nil {
		registry = NewRegistry()
	}

	svc := &Service{
		registry: registry,
		node:     node,
	}

	svc.path, svc.handler = sensorsv1connect.NewSensorServiceHandler(svc)

	addr := fmt.Sprintf("127.0.0.1:%d", cfg.port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle(svc.path, svc.handler)

	server := &http.Server{
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	svc.server = server
	svc.listener = listener

	go func() {
		<-ctx.Done()
		server.Shutdown(context.Background())
	}()

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		}
	}()

	return svc, nil
}

func (s *Service) Addr() net.Addr {
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

func (s *Service) PushValue(ctx context.Context, req *connect.Request[sensorsv1.PushValueRequest]) (*connect.Response[sensorsv1.PushValueResponse], error) {
	if err := s.registry.Set(req.Msg.GetName(), req.Msg.GetValue()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	return connect.NewResponse(&sensorsv1.PushValueResponse{}), nil
}

func (s *Service) NodeInfo(ctx context.Context, req *connect.Request[sensorsv1.NodeInfoRequest]) (*connect.Response[sensorsv1.NodeInfoResponse], error) {
	nodeID := s.node.ID().String()
	return connect.NewResponse(&sensorsv1.NodeInfoResponse{
		NodeId: nodeID,
	}), nil
}
