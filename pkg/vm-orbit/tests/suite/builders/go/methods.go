package goBuilder

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"

	_ "embed"

	"github.com/pterm/pterm"
	"github.com/taubyte/tau/pkg/vm-orbit/tests/suite/builders"

	"github.com/otiai10/copy"
)

// goBuilder wraps methods for a Builder
type goBuilder struct{}

// returns a goBuilder
func New() *goBuilder {
	return &goBuilder{}
}

func (g *goBuilder) Wasm(ctx context.Context, codeFiles ...string) (wasmFile string, err error) {
	tempDir, err := builders.CopyFixture(fixture)
	if err != nil {
		return
	}

	// TODO: This may become generic
	for _, codeFile := range codeFiles {
		if err = copy.Copy(codeFile, path.Join(tempDir, path.Base(codeFile))); err != nil {
			return
		}
	}

	return builders.Wasm(ctx, tempDir)
}

func (g *goBuilder) Name() string {
	return "go"
}

func (g *goBuilder) Plugin(_path string, name string, extraArgs ...string) (string, error) {
	tempDir, err := os.MkdirTemp("", "*")
	if err != nil {
		return "", fmt.Errorf("creating temp dir failed with: %w", err)
	}

	pterm.Success.Printfln("Building go plugin in %s", tempDir)

	output := path.Join(tempDir, name)
	args := []string{"build", "-o", output}
	args = append(args, extraArgs...)
	cmd := exec.Command("go", args...)
	cmd.Dir = _path
	if err = cmd.Run(); err != nil {
		return "", fmt.Errorf("building go plugin failed with: %w", err)
	}

	return output, nil
}
