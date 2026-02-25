package build

import (
	"context"
	"fmt"
	"os"

	"github.com/taubyte/tau/core/builders"
	libraryI18n "github.com/taubyte/tau/tools/tau/i18n/library"
	libraryPrompts "github.com/taubyte/tau/tools/tau/prompts/library"
	"github.com/urfave/cli/v2"
)

func runBuildLibrary(ctx *cli.Context) error {
	bc, err := getBuildContext()
	if err != nil {
		return err
	}

	lib, err := libraryPrompts.GetOrSelect(ctx)
	if err != nil {
		return err
	}

	workDir, err := bc.workDirForLibrary(lib.RepoName)
	if err != nil {
		return fmt.Errorf("library path: %w", err)
	}

	if err := verifyWorkDirExists(workDir); err != nil {
		libraryI18n.Help().BeSureToCloneLibrary()
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
