package runtime

import (
	"io"
)

var NoStdin = &noStdin{}

type noStdin struct{}

func (*noStdin) Read(p []byte) (n int, err error) {
	return 0, io.EOF
}
