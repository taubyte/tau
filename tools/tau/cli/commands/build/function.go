package build

import (
	"context"
	"os"

	"github.com/taubyte/tau/core/builders"
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
	_, err = writeCompressedToOutput(compressed, outPath, "tau-build-*.wasm")
	return err
}
