package validate

import (
	"context"
	"io"
	"os"

	"github.com/taubyte/tau/tools/tau/config"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	tcc "github.com/taubyte/tau/utils/tcc"
	"github.com/urfave/cli/v2"

	tccCompiler "github.com/taubyte/tau/pkg/tcc/taubyte/v1"
)

func runValidateConfig(c *cli.Context) error {
	projectConfig, err := projectLib.SelectedProjectConfig()
	if err != nil {
		return err
	}

	projectName, err := config.GetSelectedProject()
	if err != nil {
		return err
	}

	branch := c.String(branchFlag.Name)
	if branch == "" {
		h := projectLib.Repository(projectName)
		repos, err := h.Open()
		if err != nil {
			return err
		}
		branch, err = repos.CurrentBranch()
		if err != nil {
			return err
		}
	}

	compiler, err := tccCompiler.New(
		tccCompiler.WithLocal(projectConfig.ConfigLoc()),
		tccCompiler.WithBranch(branch),
	)
	if err != nil {
		return err
	}

	_, _, err = compiler.Compile(context.Background())
	if err != nil {
		logs := tcc.Logs(err)
		logs.Seek(0, io.SeekStart)
		io.Copy(os.Stderr, logs)
		return err
	}

	// Success
	if _, err := io.WriteString(os.Stdout, "Config is valid\n"); err != nil {
		return err
	}
	return nil
}
