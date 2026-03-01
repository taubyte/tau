package build

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"

	"github.com/taubyte/tau/core/builders"
	"github.com/taubyte/tau/pkg/specs/builders/wasm"
	"github.com/taubyte/tau/tools/tau/config"
	functionPrompts "github.com/taubyte/tau/tools/tau/prompts/function"
	"github.com/urfave/cli/v2"
)

func runBuildFunction(ctx *cli.Context) error {
	bc, err := getBuildContext()
	if err != nil {
		return err
	}

	fn, err := functionPrompts.GetOrSelect(ctx)
	if err != nil {
		return err
	}

	workDir := bc.workDirForFunction(fn.Name)

	if err := verifyWorkDirExists(workDir); err != nil {
		return err
	}

	buildCtx := context.Background()
	b, err := newBuilderFunc(buildCtx, NewBuildOutputWriter(os.Stderr), workDir)
	if err != nil {
		return err
	}
	defer b.Close()

	asset, err := b.Build()
	if err != nil {
		return err
	}

	compressed, err := asset.Compress(builders.WASM)
	if err != nil {
		return err
	}
	defer compressed.Close()

	outPath := ctx.String(outputFlag.Name)
	if outPath == "" {
		dir := buildsDirForFunction(bc.projectConfig.Location, bc.selectedApp, fn.Name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		outPath = path.Join(dir, wasm.ZipFile)
	}
	_, err = writeCompressedToOutput(compressed, outPath, "tau-build-*.wasm")
	return err
}

// BuildFunctionToBuildsDir builds the given function and writes the artifact to
// the project's builds dir. Returns the path to the written artifact (artifact.zip).
// Callers can use this when run has no WASM and the user opts to build first.
func BuildFunctionToBuildsDir(projectConfig config.Project, application, functionName string, buildOutput io.Writer) (artifactPath string, err error) {
	bc := &buildContext{projectConfig: projectConfig, selectedApp: application}
	workDir := bc.workDirForFunction(functionName)
	if err := verifyWorkDirExists(workDir); err != nil {
		return "", err
	}
	w := NewBuildOutputWriter(buildOutput)
	if c, ok := w.(interface{ Close() error }); ok {
		defer c.Close()
	}
	b, err := newBuilderFunc(context.Background(), w, workDir)
	if err != nil {
		return "", fmt.Errorf("creating builder: %w", err)
	}
	defer b.Close()
	asset, err := b.Build()
	if err != nil {
		return "", fmt.Errorf("building: %w", err)
	}
	compressed, err := asset.Compress(builders.WASM)
	if err != nil {
		return "", fmt.Errorf("compressing: %w", err)
	}
	defer compressed.Close()
	dir := buildsDirForFunction(projectConfig.Location, application, functionName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating builds dir: %w", err)
	}
	outPath := path.Join(dir, wasm.ZipFile)
	if _, err := writeCompressedToOutput(compressed, outPath, "tau-build-*.wasm"); err != nil {
		return "", err
	}
	return outPath, nil
}
