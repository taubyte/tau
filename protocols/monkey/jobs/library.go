package jobs

import (
	"fmt"
	"io"

	build "github.com/taubyte/builder"
	"github.com/taubyte/go-interfaces/builders"
)

func (c *Context) HandleLibrary() (builders.Output, error) {
	builder, err := build.New(c.ctx, c.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("creating new builder for git library repo `%d` failed with: %w", c.Job.Meta.Repository.ID, err)
	}
	defer builder.Close()

	output, err := builder.Build()
	if err != nil {
		err = fmt.Errorf("building library failed with: %w", err)
	}

	return output, err
}

func (l *library) handle() (err error) {
	var (
		output builders.Output
		id     string
		zWasm  io.ReadSeekCloser
	)
	defer func() {
		if err != nil {
			l.logErrorHandler(err.Error())
		}

		handleOutput(&output, l.LogFile, new(debugMessage).append(l.debug))
		if zWasm != nil {
			if err == nil {
				if err = l.handleBuildDetails(id, zWasm, l.LogFile); err != nil {
					err = fmt.Errorf("handling library build details failed with: %s", err)
				}
			}

			zWasm.Close()
		}
	}()

	if output, err = l.HandleLibrary(); err != nil {
		return fmt.Errorf("handling library failed with: %s", err)
	}

	if id, err = l.getResourceRepositoryId(); err != nil {
		return fmt.Errorf("resource id for library repo failed with: %s", err)
	}

	if zWasm, err = output.Compress(builders.WASM); err != nil {
		return fmt.Errorf("compressing build files failed with: %w", err)
	}

	return nil
}
