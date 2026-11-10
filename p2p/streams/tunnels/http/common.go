package httptun

import (
	"errors"

	"github.com/taubyte/tau/p2p/streams/packer"
)

var (
	Magic   = packer.Magic{0x02, 0xfc}
	Version = packer.Version(0x01)
)

const (
	HeadersOp packer.Channel = 1
	RequestOp packer.Channel = 8
	BodyOp    packer.Channel = 16
	// AbortOp says the response failed after it began. A frontend older than this op fails on the unknown channel, which aborts as well.
	AbortOp packer.Channel = 32
)

var (
	BodyStreamBufferSize = 4 * 1024

	ErrNotBody = errors.New("payload not an http body")

	// ErrRefused is Frontend's answer when the backend refused a request that
	// can safely go to another backend. Nothing has been written to the client.
	ErrRefused = errors.New("backend refused the request before running it")

	// ErrAborted is Frontend's answer when the backend failed after the response began.
	ErrAborted = errors.New("backend aborted the response")
)
