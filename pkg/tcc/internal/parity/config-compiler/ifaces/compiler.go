package ifaces

import (
	"io"

	tnsIface "github.com/taubyte/tau/core/services/tns"
)

type Compiler interface {
	io.Closer
	Load(object map[string]interface{}) error
	Build() error
	Object() map[string]interface{}
	Indexes() map[string]interface{}
	Logs() io.ReadSeeker
	Publish(tns tnsIface.Client) (err error)
}

type Result interface {
	Logs() io.ReadSeekCloser
	Data() map[string]interface{}
}
