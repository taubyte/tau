package error

import (
	"io"

	cr "github.com/taubyte/tau/p2p/streams/command/response"
	"github.com/taubyte/tau/pkg/platformerr"
)

// Encode writes err for the peer. A coded error also sends its code; old
// clients only read "error" and ignore it.
func Encode(s io.Writer, err error) error {
	resp := cr.Response{"error": err.Error()}
	if code := platformerr.CodeOf(err); code != "" {
		resp["code"] = string(code)
	}
	return resp.Encode(s)
}
