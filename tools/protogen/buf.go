package protogen

import (
	"os/exec"
)

// BufVersion is the buf this repository compiles its schema with, and it is
// pinned here rather than only in CI.
//
// A DIFFERENT buf IS A DIFFERENT DESCRIPTOR. The rules in pkg/proto are asked
// about whatever buf builds, so two versions can disagree about what the schema
// IS — which turns a guard into an opinion about the developer's PATH.
const BufVersion = "v1.62.1"

// Buf is `buf` with args, from PATH when it is there and from the module cache
// when it is not.
//
// IT RESOLVES ITSELF BECAUSE THE ALTERNATIVE IS A README STEP. `buf` on PATH
// was a precondition nothing stated and nothing installed: the proto guard in
// pkg/proto FAILED — deliberately, rather than skipping, because a rule that
// goes quiet is worse than one that is absent — on any machine where it was
// missing, which is every fresh checkout and every build box. `go run` at the
// pinned version needs nothing but the Go toolchain that is already running the
// test.
//
// PATH STILL WINS when it has one, because a developer who installed buf on
// purpose is usually mid-upgrade and the slower path would hide what they are
// testing.
func Buf(args ...string) *exec.Cmd {
	if path, err := exec.LookPath("buf"); err == nil {
		return exec.Command(path, args...)
	}
	return exec.Command("go", append([]string{"run", "github.com/bufbuild/buf/cmd/buf@" + BufVersion}, args...)...)
}
