package satellite

import (
	"context"
	"math"
	"reflect"

	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-orbit/proto"
)

func (p *GRPCPluginServer) Symbols(context.Context, *proto.Empty) (*proto.FunctionDefinitions, error) {
	ret := &proto.FunctionDefinitions{
		Functions: make([]*proto.FunctionDefinition, 0, len(p.satellite.exports)),
	}
	for name, handler := range p.satellite.exports {
		fx := reflect.TypeOf(handler)
		if fx.Kind() != reflect.Func {
			return nil, serverError("handler %s for not a function", name)
		}

		argsType, err := parseSignatureValues(fx, in)
		if err != nil {
			return nil, serverError("parsing argument types failed with: %w", err)
		}

		retTypes, err := parseSignatureValues(fx, out)
		if err != nil {
			return nil, serverError("parsing return types failed with: %w", err)
		}

		ret.Functions = append(ret.Functions, &proto.FunctionDefinition{
			Name: name,
			Args: argsType,
			Rets: retTypes,
		})
	}

	return ret, nil
}

func (p *GRPCPluginServer) Meta(context.Context, *proto.Empty) (*proto.Metadata, error) {
	return &proto.Metadata{
		Name: p.satellite.name,
	}, nil
}

func (p *GRPCPluginServer) Call(ctx context.Context, req *proto.CallRequest) (*proto.CallReturn, error) {
	conn, err := p.broker.Dial(req.Broker)
	if err != nil {
		return nil, serverError("dialing broker failed with: %w", err)
	}

	defer conn.Close()

	mod := NewModuleLink(ctx, conn)

	handler, ok := p.satellite.exports[req.Function]
	if !ok {
		return nil, serverError("function `%s` is not exported", req.Function)
	}

	fx := reflect.ValueOf(handler)
	tfx := fx.Type()
	in := make([]reflect.Value, 0, len(req.Inputs)+2)

	if tfx.NumIn() >= 1 && tfx.In(0) == vm.ContextType {
		in = append(in, reflect.ValueOf(ctx))
	}

	if tfx.NumIn() >= 2 && tfx.In(1) == moduleType {
		in = append(in, reflect.ValueOf(mod))
	}

	for _, v := range req.Inputs {
		var rv reflect.Value
		switch tfx.In(len(in)).Kind() {
		case reflect.Int32:
			rv = reflect.ValueOf(int32(v))
		case reflect.Int64:
			rv = reflect.ValueOf(int64(v))
		case reflect.Uint32:
			rv = reflect.ValueOf(uint32(v))
		case reflect.Uint64:
			rv = reflect.ValueOf(uint64(v))
		case reflect.Float32:
			rv = reflect.ValueOf(float32(v))
		case reflect.Float64:
			rv = reflect.ValueOf(float64(v))
		default:
			return nil, serverError("invalid input type %#v", tfx.In(len(in)).Kind())
		}
		in = append(in, rv)
	}

	out := fx.Call(in)

	// TODO: This logic is very similar to the golangToWasm method in VM
	ret := make([]uint64, len(out))
	for i, _arg := range out {
		switch _arg.Kind() {
		case reflect.Float32:
			ret[i] = uint64(math.Float32bits(float32(_arg.Float())))
		case reflect.Float64:
			ret[i] = math.Float64bits(_arg.Float())
		case reflect.Uint, reflect.Uint32, reflect.Uint64:
			ret[i] = _arg.Uint()
		case reflect.Int, reflect.Int32, reflect.Int64:
			ret[i] = uint64(_arg.Int())
		default:
			return nil, serverError("failed to process arguments %v of type %T", _arg, _arg)
		}
	}

	return &proto.CallReturn{
		Rets: ret,
	}, nil
}
