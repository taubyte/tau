package service

import (
	"connectrpc.com/connect"
	pb "github.com/taubyte/tau/pkg/spore-drive/proto/gen/config/v1"
)

func returnString(s string) *connect.Response[pb.Return] {
	return connect.NewResponse(&pb.Return{
		Return: &pb.Return_String_{String_: s},
	})
}

func returnStringSlice(s []string) *connect.Response[pb.Return] {
	return connect.NewResponse(&pb.Return{
		Return: &pb.Return_Slice{Slice: &pb.StringSlice{Value: s}},
	})
}

func returnBytes(s []byte) *connect.Response[pb.Return] {
	return connect.NewResponse(&pb.Return{
		Return: &pb.Return_Bytes{Bytes: s},
	})
}

func returnUint(s uint64) *connect.Response[pb.Return] {
	return connect.NewResponse(&pb.Return{
		Return: &pb.Return_Uint64{Uint64: s},
	})
}

func returnEmpty(err error) (*connect.Response[pb.Return], error) {
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.Return{Return: &pb.Return_Empty{Empty: &pb.Empty{}}}), nil
}

func noValReturn(err error) (*connect.Response[pb.Empty], error) {
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.Empty{}), nil
}
