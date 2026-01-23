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
)

var (
	BodyStreamBufferSize = 4 * 1024

	ErrNotBody = errors.New("payload not an http body")
)
