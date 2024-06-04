package link

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-orbit/proto"
	"google.golang.org/grpc"
)

func clientErr(msg string, args ...any) error {
	return fmt.Errorf("[]client "+msg, args...)
}

func (c *GRPCPluginClient) Symbols(ctx context.Context) ([]vm.FunctionDefinition, error) {
	resp, err := c.client.Symbols(ctx, &proto.Empty{})
	if err != nil {
		return nil, clientErr("calling symbols failed with: %w", err)
	}

	funcDefs := make([]vm.FunctionDefinition, len(resp.Functions))
	for idx, function := range resp.Functions {
		args, err := typesToBytes(function.Args)
		if err != nil {
			return nil, clientErr("getting arg types failed with: %w", err)
		}

		rets, err := typesToBytes(function.Rets)
		if err != nil {
			return nil, clientErr("getting return types failed with: %w", err)
		}

		funcDefs[idx] = &functionDefinition{function.Name, args, rets}
	}

	return funcDefs, nil
}

func (c *GRPCPluginClient) Meta(ctx context.Context) (*proto.Metadata, error) {
	meta, err := c.client.Meta(ctx, &proto.Empty{})
	if err != nil {
		return nil, clientErr("meta failed with: %w", err)
	}

	return meta, nil
}

func (c *GRPCPluginClient) Call(ctx context.Context, module vm.Module, function string, inputs []uint64) ([]uint64, error) {
	moduleServer := NewModule(module)

	var s *grpc.Server
	serverFunc := func(opts []grpc.ServerOption) *grpc.Server {
		s = grpc.NewServer(opts...)

		proto.RegisterModuleServer(s, moduleServer)

		return s
	}

	brokerID := c.broker.NextId()
	go c.broker.AcceptAndServe(brokerID, serverFunc)

	resp, err := c.client.Call(ctx, &proto.CallRequest{
		Broker:   brokerID,
		Function: function,
		Inputs:   inputs,
	})
	if err != nil {
		return nil, clientErr("calling `%s/%s` failed with: %w", module.Name(), function, err)
	}
	defer s.Stop()

	return resp.Rets, nil
}

func (c *GRPCPluginClient) Close() error {
	return c.broker.Close()
}

func typesToBytes(valueTypes []proto.Type) ([]vm.ValueType, error) {
	types := make([]vm.ValueType, len(valueTypes))
	for idx, vt := range valueTypes {
		switch vm.ValueType(vt) {
		case vm.ValueTypeF32, vm.ValueTypeF64, vm.ValueTypeI32, vm.ValueTypeI64:
			types[idx] = vm.ValueType(vt)
		default:
			return nil, errors.New("unknown type")
		}
	}

	return types, nil
}
