package builders

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"

	"github.com/pterm/pterm"
	build "github.com/taubyte/tau/pkg/builder"
)

// CopyFixture writes the given fixture tarball to a temp directory and unzips it
func CopyFixture(fixture []byte) (tempDir string, err error) {
	tempDir, err = os.MkdirTemp("/tmp", "*")
	if err != nil {
		err = fmt.Errorf("creating temp dir failed with: %w", err)
		return
	}

	pterm.Success.Printfln("Building code in: %s", tempDir)

	if err = os.WriteFile(path.Join(tempDir, "fixture.tar"), fixture, 0644); err != nil {
		err = fmt.Errorf("writing fixture.tar failed with: %w", err)
		return
	}

	cmd := exec.Command("tar", "-xvf", "fixture.tar")
	cmd.Dir = tempDir
	if err = cmd.Run(); err != nil {
		err = fmt.Errorf("un-tar fixture.tar failed with: %w", err)
	}

	return
}

// Wasm builds the a wasm file from the given directory
func Wasm(ctx context.Context, buildDir string) (wasmFile string, err error) {
	builder, err := build.New(ctx, buildDir)
	if err != nil {
		err = fmt.Errorf("new builder failed with: %w", err)
		return
	}

	out, err := builder.Build()
	if err != nil {
		err = fmt.Errorf("builder.Build() failed with: %w", err)
		return
	}

	wasmFile = path.Join(out.OutDir(), "artifact.wasm")
	return
}
