package validate

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/taubyte/tau/tools/tau/config"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	tcc "github.com/taubyte/tau/utils/tcc"
	"github.com/urfave/cli/v2"

	tccCompiler "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
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

	_, deferred, err := compiler.Compile(context.Background())
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

	// AND WHAT IT COULD NOT CHECK, because a compile that says nothing about
	// these reads as "everything is checked". A region name is an opaque label
	// the config holds no list of, so it is refusable only where the cloud is
	// in the room — and the first time an author should learn their label means
	// nothing is not a deploy that placed their database somewhere else.
	outstanding := tcc.Deferred(deferred)
	for _, kind := range slices.Sorted(maps.Keys(outstanding)) {
		if _, err := fmt.Fprintf(os.Stdout, "Checked at deploy — %s: %s\n",
			kind, strings.Join(outstanding[kind], ", ")); err != nil {
			return err
		}
	}
	return nil
}
