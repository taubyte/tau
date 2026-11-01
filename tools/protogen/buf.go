package protogen

import (
	"os/exec"
)

// BufVersion is the buf this repository compiles its schema with. A different buf
// can build a different descriptor, which the pkg/proto rules are asked about.
const BufVersion = "v1.62.1"

// Buf is `buf` with args, from PATH when it is there and otherwise `go run` at the
// pinned version, so a fresh checkout needs only Go. PATH wins, since a deliberate
// install is usually an upgrade in progress.
func Buf(args ...string) *exec.Cmd {
	if path, err := exec.LookPath("buf"); err == nil {
		return exec.Command(path, args...)
	}
	return exec.Command("go", append([]string{"run", "github.com/bufbuild/buf/cmd/buf@" + BufVersion}, args...)...)
}
