package jobs

import (
	"fmt"
	"io"
	"os"
	"path"
	"sync"

	"github.com/ipfs/go-log/v2"
	build "github.com/taubyte/builder"
	"github.com/taubyte/go-interfaces/builders"
	projectSchema "github.com/taubyte/go-project-schema/project"
	git "github.com/taubyte/go-simple-git"
)

func (c *Context) logErrorHandler(format string, args ...any) error {
	err := fmt.Errorf(format, args...)
	c.addDebugMsg(log.LevelError, err.Error())
	return err
}

func (c *code) handle() error {
	if err := c.checkConfig(); err != nil {
		return c.logErrorHandler("checking config repo for project failed with: %s", err)
	}

	project, err := projectSchema.Open(projectSchema.SystemFS(c.ConfigRepoRoot))
	if err != nil {
		return c.logErrorHandler("opening project from path `%s` failed with: %s", c.ConfigRepoRoot, err)
	}

	// Decompile and get includes and id of each function, website and library
	ops, err := buildTodoFromConfig(project)
	if err != nil {
		return c.logErrorHandler("building todo from config for project `%s` failed with: %s", project.Get().Id(), err)
	}

	if err = c.handleOps(ops); err != nil {
		return c.logErrorHandler(err.Error())
	}

	return nil
}

func (c *code) handleOps(ops []Op) error {
	if len(ops) == 0 {
		return nil
	}

	var mainHandleErr error
	var errLock sync.Mutex
	errChan := make(chan error, 1)
	doneChan := make(chan bool, 1)
	var doneCount int

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

func (c *code) handleOp(op Op, logFile *os.File) error {
	moduleReader, err := c.HandleOp(op, logFile)
	if moduleReader != nil {
		defer moduleReader.Close()
	}

	if err := c.handleBuildDetails(op.id, moduleReader, logFile); err != nil {
		return fmt.Errorf("handling build details failed with: %s", err)
	}

	return err
}

func (c *Context) HandleOp(op Op, logFile *os.File) (io.ReadSeekCloser, error) {
	var debugMsg string
	appendDebug := func(format string, args ...any) error {
		err := fmt.Errorf(format, args...)
		debugMsg += err.Error() + "\n"
		return err
	}

	sourcePath := path.Join(c.gitDir, op.application, op.pathVariable, op.name)
	builder, err := build.New(c.ctx, sourcePath)
	if err != nil {
		return nil, appendDebug("creating new wasm builder failed with: %s", err.Error())
	}

	var logs builders.Logs
	defer func() {
		if logs != nil {
			if _, err := logs.CopyTo(logFile); err != nil {
				appendDebug("copying logs from builder failed with: %w", err)
				logFile.Seek(0, io.SeekEnd)
				logFile.WriteString(fmt.Sprintf("DEBUG:\n%s", debugMsg))
				logFile.Seek(0, io.SeekStart)
			}

			logs.Close()
		}
	}()

	output, err := buildAndSetLog(builder, &logs)
	if err != nil {
		return nil, appendDebug("building function %s/%s failed with: %s", op.application, op.name, err.Error())
	}
	defer output.Close()

	moduleReader, err := output.Compress(builders.WASM)
	if err != nil {
		return nil, appendDebug("compressing build files failed with: %s", err.Error())
	}

	_, err = logs.CopyTo(logFile)
	if err != nil {
		return nil, appendDebug("copying logs failed with: %s", err.Error())
	}

	return moduleReader, nil
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
