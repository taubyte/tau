package error

import (
	"io"

	cr "github.com/taubyte/tau/p2p/streams/command/response"
)

func Encode(s io.Writer, err error) error {
	return cr.Response{"error": err.Error()}.Encode(s)
}
