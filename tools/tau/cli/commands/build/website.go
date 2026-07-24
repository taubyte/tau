package build

import (
	"context"
	"fmt"
	"os"

	"github.com/taubyte/tau/core/builders"
	websiteI18n "github.com/taubyte/tau/tools/tau/i18n/website"
	"github.com/taubyte/tau/tools/tau/tcc"
	"github.com/urfave/cli/v2"
)

func runBuildWebsite(ctx *cli.Context) error {
	bc, err := getBuildContext()
	if err != nil {
		return err
	}

	_, doc, err := tcc.SelectResource(ctx, "websites")
	if err != nil {
		return err
	}

	repoName, err := tcc.RepositoryName("websites", doc)
	if err != nil {
		return err
	}

	workDir, err := bc.workDirForWebsite(repoName)
	if err != nil {
		return fmt.Errorf("website path: %w", err)
	}

	if err := verifyWorkDirExists(workDir); err != nil {
		websiteI18n.Help().BeSureToCloneWebsite()
		return err
	}

	buildCtx := context.Background()
	b, err := newBuilderFunc(buildCtx, NewBuildOutputWriter(os.Stderr), workDir)
	if err != nil {
		return err
	}
	defer b.Close()

	asset, err := b.Build(b.Wd().Website().SetWorkDir())
	if err != nil {
		return err
	}

	compressed, err := asset.Compress(builders.Website)
	if err != nil {
		return err
	}
	defer compressed.Close()

	outPath := ctx.String(outputFlag.Name)
	_, err = writeCompressedToOutput(compressed, outPath, "tau-build-*.zip")
	return err
}
