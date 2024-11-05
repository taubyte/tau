// Code generated by protoc-gen-connect-go. DO NOT EDIT.
//
// Source: taucorder/v1/swarm.proto

package taucorderv1connect

import (
	connect "connectrpc.com/connect"
	context "context"
	errors "errors"
	v1 "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
	http "net/http"
	strings "strings"
)

// This is a compile-time assertion to ensure that this generated file and the connect package are
// compatible. If you get a compiler error that this constant is not defined, this code was
// generated with a version of connect newer than the one compiled into your binary. You can fix the
// problem by either regenerating this code with an older version of connect or updating the connect
// version compiled into your binary.
const _ = connect.IsAtLeastVersion1_13_0

const (
	// SwarmServiceName is the fully-qualified name of the SwarmService service.
	SwarmServiceName = "taucorder.v1.SwarmService"
)

// These constants are the fully-qualified names of the RPCs defined in this package. They're
// exposed at runtime as Spec.Procedure and as the final two segments of the HTTP route.
//
// Note that these are different from the fully-qualified method names used by
// google.golang.org/protobuf/reflect/protoreflect. To convert from these constants to
// reflection-formatted method names, remove the leading slash and convert the remaining slash to a
// period.
const (
	// SwarmServiceWaitProcedure is the fully-qualified name of the SwarmService's Wait RPC.
	SwarmServiceWaitProcedure = "/taucorder.v1.SwarmService/Wait"
	// SwarmServiceListProcedure is the fully-qualified name of the SwarmService's List RPC.
	SwarmServiceListProcedure = "/taucorder.v1.SwarmService/List"
	// SwarmServicePingProcedure is the fully-qualified name of the SwarmService's Ping RPC.
	SwarmServicePingProcedure = "/taucorder.v1.SwarmService/Ping"
	// SwarmServiceConnectProcedure is the fully-qualified name of the SwarmService's Connect RPC.
	SwarmServiceConnectProcedure = "/taucorder.v1.SwarmService/Connect"
	// SwarmServiceDiscoverProcedure is the fully-qualified name of the SwarmService's Discover RPC.
	SwarmServiceDiscoverProcedure = "/taucorder.v1.SwarmService/Discover"
)

// These variables are the protoreflect.Descriptor objects for the RPCs defined in this package.
var (
	swarmServiceServiceDescriptor        = v1.File_taucorder_v1_swarm_proto.Services().ByName("SwarmService")
	swarmServiceWaitMethodDescriptor     = swarmServiceServiceDescriptor.Methods().ByName("Wait")
	swarmServiceListMethodDescriptor     = swarmServiceServiceDescriptor.Methods().ByName("List")
	swarmServicePingMethodDescriptor     = swarmServiceServiceDescriptor.Methods().ByName("Ping")
	swarmServiceConnectMethodDescriptor  = swarmServiceServiceDescriptor.Methods().ByName("Connect")
	swarmServiceDiscoverMethodDescriptor = swarmServiceServiceDescriptor.Methods().ByName("Discover")
)

// SwarmServiceClient is a client for the taucorder.v1.SwarmService service.
type SwarmServiceClient interface {
	Wait(context.Context, *connect.Request[v1.WaitRequest]) (*connect.Response[v1.Empty], error)
	List(context.Context, *connect.Request[v1.ListRequest]) (*connect.ServerStreamForClient[v1.Peer], error)
	Ping(context.Context, *connect.Request[v1.PingRequest]) (*connect.Response[v1.Empty], error)
	Connect(context.Context, *connect.Request[v1.ConnectRequest]) (*connect.Response[v1.Peer], error)
	Discover(context.Context, *connect.Request[v1.DiscoverRequest]) (*connect.ServerStreamForClient[v1.Peer], error)
}

// NewSwarmServiceClient constructs a client for the taucorder.v1.SwarmService service. By default,
// it uses the Connect protocol with the binary Protobuf Codec, asks for gzipped responses, and
// sends uncompressed requests. To use the gRPC or gRPC-Web protocols, supply the connect.WithGRPC()
// or connect.WithGRPCWeb() options.
//
// The URL supplied here should be the base URL for the Connect or gRPC server (for example,
// http://api.acme.com or https://acme.com/grpc).
func NewSwarmServiceClient(httpClient connect.HTTPClient, baseURL string, opts ...connect.ClientOption) SwarmServiceClient {
	baseURL = strings.TrimRight(baseURL, "/")
	return &swarmServiceClient{
		wait: connect.NewClient[v1.WaitRequest, v1.Empty](
			httpClient,
			baseURL+SwarmServiceWaitProcedure,
			connect.WithSchema(swarmServiceWaitMethodDescriptor),
			connect.WithClientOptions(opts...),
		),
		list: connect.NewClient[v1.ListRequest, v1.Peer](
			httpClient,
			baseURL+SwarmServiceListProcedure,
			connect.WithSchema(swarmServiceListMethodDescriptor),
			connect.WithClientOptions(opts...),
		),
		ping: connect.NewClient[v1.PingRequest, v1.Empty](
			httpClient,
			baseURL+SwarmServicePingProcedure,
			connect.WithSchema(swarmServicePingMethodDescriptor),
			connect.WithClientOptions(opts...),
		),
		connect: connect.NewClient[v1.ConnectRequest, v1.Peer](
			httpClient,
			baseURL+SwarmServiceConnectProcedure,
			connect.WithSchema(swarmServiceConnectMethodDescriptor),
			connect.WithClientOptions(opts...),
		),
		discover: connect.NewClient[v1.DiscoverRequest, v1.Peer](
			httpClient,
			baseURL+SwarmServiceDiscoverProcedure,
			connect.WithSchema(swarmServiceDiscoverMethodDescriptor),
			connect.WithClientOptions(opts...),
		),
	}
}

// swarmServiceClient implements SwarmServiceClient.
type swarmServiceClient struct {
	wait     *connect.Client[v1.WaitRequest, v1.Empty]
	list     *connect.Client[v1.ListRequest, v1.Peer]
	ping     *connect.Client[v1.PingRequest, v1.Empty]
	connect  *connect.Client[v1.ConnectRequest, v1.Peer]
	discover *connect.Client[v1.DiscoverRequest, v1.Peer]
}

// Wait calls taucorder.v1.SwarmService.Wait.
func (c *swarmServiceClient) Wait(ctx context.Context, req *connect.Request[v1.WaitRequest]) (*connect.Response[v1.Empty], error) {
	return c.wait.CallUnary(ctx, req)
}

// List calls taucorder.v1.SwarmService.List.
func (c *swarmServiceClient) List(ctx context.Context, req *connect.Request[v1.ListRequest]) (*connect.ServerStreamForClient[v1.Peer], error) {
	return c.list.CallServerStream(ctx, req)
}

// Ping calls taucorder.v1.SwarmService.Ping.
func (c *swarmServiceClient) Ping(ctx context.Context, req *connect.Request[v1.PingRequest]) (*connect.Response[v1.Empty], error) {
	return c.ping.CallUnary(ctx, req)
}

// Connect calls taucorder.v1.SwarmService.Connect.
func (c *swarmServiceClient) Connect(ctx context.Context, req *connect.Request[v1.ConnectRequest]) (*connect.Response[v1.Peer], error) {
	return c.connect.CallUnary(ctx, req)
}

// Discover calls taucorder.v1.SwarmService.Discover.
func (c *swarmServiceClient) Discover(ctx context.Context, req *connect.Request[v1.DiscoverRequest]) (*connect.ServerStreamForClient[v1.Peer], error) {
	return c.discover.CallServerStream(ctx, req)
}

// SwarmServiceHandler is an implementation of the taucorder.v1.SwarmService service.
type SwarmServiceHandler interface {
	Wait(context.Context, *connect.Request[v1.WaitRequest]) (*connect.Response[v1.Empty], error)
	List(context.Context, *connect.Request[v1.ListRequest], *connect.ServerStream[v1.Peer]) error
	Ping(context.Context, *connect.Request[v1.PingRequest]) (*connect.Response[v1.Empty], error)
	Connect(context.Context, *connect.Request[v1.ConnectRequest]) (*connect.Response[v1.Peer], error)
	Discover(context.Context, *connect.Request[v1.DiscoverRequest], *connect.ServerStream[v1.Peer]) error
}

// NewSwarmServiceHandler builds an HTTP handler from the service implementation. It returns the
// path on which to mount the handler and the handler itself.
//
// By default, handlers support the Connect, gRPC, and gRPC-Web protocols with the binary Protobuf
// and JSON codecs. They also support gzip compression.
func NewSwarmServiceHandler(svc SwarmServiceHandler, opts ...connect.HandlerOption) (string, http.Handler) {
	swarmServiceWaitHandler := connect.NewUnaryHandler(
		SwarmServiceWaitProcedure,
		svc.Wait,
		connect.WithSchema(swarmServiceWaitMethodDescriptor),
		connect.WithHandlerOptions(opts...),
	)
	swarmServiceListHandler := connect.NewServerStreamHandler(
		SwarmServiceListProcedure,
		svc.List,
		connect.WithSchema(swarmServiceListMethodDescriptor),
		connect.WithHandlerOptions(opts...),
	)
	swarmServicePingHandler := connect.NewUnaryHandler(
		SwarmServicePingProcedure,
		svc.Ping,
		connect.WithSchema(swarmServicePingMethodDescriptor),
		connect.WithHandlerOptions(opts...),
	)
	swarmServiceConnectHandler := connect.NewUnaryHandler(
		SwarmServiceConnectProcedure,
		svc.Connect,
		connect.WithSchema(swarmServiceConnectMethodDescriptor),
		connect.WithHandlerOptions(opts...),
	)
	swarmServiceDiscoverHandler := connect.NewServerStreamHandler(
		SwarmServiceDiscoverProcedure,
		svc.Discover,
		connect.WithSchema(swarmServiceDiscoverMethodDescriptor),
		connect.WithHandlerOptions(opts...),
	)
	return "/taucorder.v1.SwarmService/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case SwarmServiceWaitProcedure:
			swarmServiceWaitHandler.ServeHTTP(w, r)
		case SwarmServiceListProcedure:
			swarmServiceListHandler.ServeHTTP(w, r)
		case SwarmServicePingProcedure:
			swarmServicePingHandler.ServeHTTP(w, r)
		case SwarmServiceConnectProcedure:
			swarmServiceConnectHandler.ServeHTTP(w, r)
		case SwarmServiceDiscoverProcedure:
			swarmServiceDiscoverHandler.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

// UnimplementedSwarmServiceHandler returns CodeUnimplemented from all methods.
type UnimplementedSwarmServiceHandler struct{}

func (UnimplementedSwarmServiceHandler) Wait(context.Context, *connect.Request[v1.WaitRequest]) (*connect.Response[v1.Empty], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("taucorder.v1.SwarmService.Wait is not implemented"))
}

func (UnimplementedSwarmServiceHandler) List(context.Context, *connect.Request[v1.ListRequest], *connect.ServerStream[v1.Peer]) error {
	return connect.NewError(connect.CodeUnimplemented, errors.New("taucorder.v1.SwarmService.List is not implemented"))
}

func (UnimplementedSwarmServiceHandler) Ping(context.Context, *connect.Request[v1.PingRequest]) (*connect.Response[v1.Empty], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("taucorder.v1.SwarmService.Ping is not implemented"))
}

func (UnimplementedSwarmServiceHandler) Connect(context.Context, *connect.Request[v1.ConnectRequest]) (*connect.Response[v1.Peer], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("taucorder.v1.SwarmService.Connect is not implemented"))
}

func (UnimplementedSwarmServiceHandler) Discover(context.Context, *connect.Request[v1.DiscoverRequest], *connect.ServerStream[v1.Peer]) error {
	return connect.NewError(connect.CodeUnimplemented, errors.New("taucorder.v1.SwarmService.Discover is not implemented"))
}
