package jobs

import (
	"fmt"
	"io"

	build "bitbucket.org/taubyte/go-builder"
	"github.com/taubyte/go-interfaces/builders"
)

func (c Context) HandleLibrary() (io.ReadSeekCloser, error) {
	builder, err := build.New(c.ctx, c.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("creating new builder for git library repo `%d` failed with: %s", c.Job.Meta.Repository.ID, err)
	}
	defer builder.Close()

	var logs builders.Logs
	output, err := buildAndSetLog(builder, &logs)
	if err != nil {
		return nil, fmt.Errorf("building failed with: %s", err)
	}
	defer output.Close()

	zWasm, err := output.Compress(builders.WASM)
	if err != nil {
		return nil, logs.FormatErr("compressing build files failed with: %s", err)
	}

	_, err = logs.CopyTo(c.LogFile)
	if err != nil {
		return nil, logs.FormatErr("copying logs failed with: %s", err)
	}

	return zWasm, nil
}

func (l library) handle() error {
	zWasm, err := l.HandleLibrary()
	if err != nil {
		return fmt.Errorf("handling library failed with: %s", err)
	}
	defer zWasm.Close()

	libId, err := l.getResourceRepositoryId()
	if err != nil {
		return fmt.Errorf("resource id for library repo failed with: %s", err)
	}

	if err = l.handleBuildDetails(libId, zWasm, l.LogFile); err != nil {
		return fmt.Errorf("handling library build details failed with: %s", err)
	}

	return nil
}
