package jobs

import (
	"fmt"
	"io"
	"os"
	"path"
	"sync"

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

	var (
		mainHandleErr error
		errLock       sync.Mutex
		doneCount     int

		errChan  = make(chan error, 1)
		doneChan = make(chan bool, 1)
	)

	for _, op := range ops {
		logFile, err := os.CreateTemp("/tmp", fmt.Sprintf("log-%s", op.id))
		if err != nil {
			return fmt.Errorf("creating log temp-file failed with: %s", err)
		}

		go func(_op Op, log *os.File) {
			if handleErr := c.handleOp(_op, log); handleErr != nil {
				errChan <- handleErr
			}

			doneChan <- true
			log.Close()
		}(op, logFile)
	}

	for {
		select {
		case err := <-errChan:
			if err != nil {
				errLock.Lock()
				if mainHandleErr != nil {
					mainHandleErr = fmt.Errorf("%s && %s", mainHandleErr, err)
				} else {
					mainHandleErr = err
				}
				errLock.Unlock()
			}
		case <-doneChan:
			doneCount++
			if doneCount == len(ops) {
				return mainHandleErr
			}
		}
	}
}

func (c code) handleOp(op Op, logFile *os.File) error {
	moduleReader, err := c.HandleOp(op, logFile)
	if moduleReader != nil {
		defer moduleReader.Close()
	}

	if err := c.handleBuildDetails(op.id, moduleReader, logFile); err != nil {
		return fmt.Errorf("handling build details failed with: %s", err)
	}

	return err
}

func (c Context) HandleOp(op Op, logFile *os.File) (rsk io.ReadSeekCloser, err error) {
	sourcePath := path.Join(c.gitDir, op.application, op.pathVariable, op.name)
	builder, err := build.New(c.ctx, sourcePath)
	if err != nil {
		err = fmt.Errorf("creating new wasm builder failed with: %w", err)
		return
	}

	var asset builders.Output
	defer func() {
		handleAsset(&asset, logFile, &err)
		builder.Close()
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
			// uncomment to keep directory
			// git.Preserve(),
		)
		if err != nil {
			return fmt.Errorf("getting git repo from url `%s` failed with: %s", url, err)
		}

		c.ConfigRepoRoot = configRepo.Root()
	}

	return nil
}
