package tccUtils

import (
	"bytes"
	"io"
)

// Logs converts an error to an io.ReadSeeker, similar to the old compiler.Logs() behavior
func Logs(err error) io.ReadSeeker {
	if err == nil {
		return bytes.NewReader([]byte{})
	}
	return bytes.NewReader([]byte(err.Error() + "\n"))
}
