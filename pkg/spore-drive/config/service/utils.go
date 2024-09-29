package service

import (
	pb "github.com/taubyte/tau/pkg/spore-drive/config/proto/go"
)

func returnString(s string) *pb.Return {
	return &pb.Return{
		Return: &pb.Return_String_{String_: s},
	}
}

func returnStringSlice(s []string) *pb.Return {
	return &pb.Return{
		Return: &pb.Return_Slice{Slice: &pb.StringSlice{Value: s}},
	}
}

func returnBytes(s []byte) *pb.Return {
	return &pb.Return{
		Return: &pb.Return_Bytes{Bytes: s},
	}
}

func returnUint(s uint64) *pb.Return {
	return &pb.Return{
		Return: &pb.Return_Uint64{Uint64: s},
	}
}
