package proto

//go:generate protoc -I .  ./orbit.proto --go-grpc_out=../ --go_out=../ --experimental_allow_proto3_optional

import (
	"errors"
	"io"
)

// TODO: Below should be generated
func (x IOError) Error() error {
	switch x {
	case IOError_none:
		return nil
	case IOError_shortWrite:
		return io.ErrShortWrite
	case IOError_invalidWrite:
		// Not exported by io package
		return errors.New("invalid write result")
	case IOError_shortBuffer:
		return io.ErrShortBuffer
	case IOError_eof:
		return io.EOF
	case IOError_noProgress:
		return io.ErrNoProgress
	default:
		return errors.New("unknown")
	}
}
