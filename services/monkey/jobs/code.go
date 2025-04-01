package jobs

import (
	"fmt"
	"io"
	"path"

	"github.com/taubyte/tau/core/builders"
	build "github.com/taubyte/tau/pkg/builder"
	"github.com/taubyte/tau/pkg/git"
	projectSchema "github.com/taubyte/tau/pkg/schema/project"
)

func (c code) handle() error {
	if err := c.checkConfig(); err != nil {
		return fmt.Errorf("checking config repo for project failed with: %w", err)
	}

	project, err := projectSchema.Open(projectSchema.SystemFS(c.ConfigRepoRoot))
	if err != nil {
		return fmt.Errorf("opening project from path `%s` failed with: %w", c.ConfigRepoRoot, err)
	}

	// Decompile and get includes and id of each function, website and library
	ops, err := buildTodoFromConfig(project)
	if err != nil {
		return fmt.Errorf("building todo from config for project `%s` failed with: %w", project.Get().Id(), err)
	}

	if err = c.handleOps(ops); err != nil {
		return err
	}

	return nil
}

func (c code) handleOps(ops []Op) error {
	if len(ops) == 0 {
		return nil
	}

	for _, op := range ops {
		op.err = c.handleOp(op)
		if op.err != nil {
			fmt.Fprintf(c.LogFile, "Error building %s: %s\n", op.name, op.err.Error())
			return op.err
		}
	}

	return nil
}

func (c code) handleOp(op Op) error {
	moduleReader, err := c.HandleOp(op)
	if err != nil {
		return err
	}
	defer moduleReader.Close()

	if err := c.handleCompressedBuild(op.id, moduleReader); err != nil {
		return fmt.Errorf("handling build details failed with: %s", err)
	}

	return err
}

func (c Context) HandleOp(op Op) (io.ReadSeekCloser, error) {
	sourcePath := path.Join(c.gitDir, op.application, op.pathVariable, op.name)
	builder, err := build.New(c.ctx, sourcePath)
	if err != nil {
		err = fmt.Errorf("creating new wasm builder failed with: %w", err)
		return nil, err
	}

	var asset builders.Output
	defer func() {
		fmt.Fprintf(c.LogFile, "Building %s -----\n", op.name)
		c.mergeBuildLogs(asset.Logs())
		builder.Close()
		asset.Close()
	}()

	if asset, err = builder.Build(); err != nil {
		return nil, fmt.Errorf("building function %s/%s failed with: %w", op.application, op.name, err)
	}

	compressedAsset, err := asset.Compress(builders.WASM)
	if err != nil {
		return nil, fmt.Errorf("compressing build files failed with: %w", err)
	}

	return compressedAsset, nil
}

func (c *code) checkConfig() error {
	if len(c.ConfigRepoRoot) < 1 {
		url, err := c.fetchConfigSshUrl()
		if err != nil {
			return fmt.Errorf("failed fetch config ssh url with: %s", err)
		}

		configRepo, err := git.New(
			c.ctx,
			git.URL(url),
			git.SSHKey(c.ConfigPrivateKey),
			git.Temporary(),
			git.Branch(c.Job.Meta.Repository.Branch),
		)
		if err != nil {
			return fmt.Errorf("getting git repo from url `%s` failed with: %s", url, err)
		}

		c.ConfigRepoRoot = configRepo.Root()
	}

	return nil
}
