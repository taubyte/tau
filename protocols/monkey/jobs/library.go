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

	asset, err := builder.Build()
	if err != nil {
		err = fmt.Errorf("building library failed with: %w", err)
	}

	return asset, err
}

func (l *library) handle() (err error) {
	var (
		asset builders.Output
		id    string
		zWasm io.ReadSeekCloser
	)
	defer func() {
		handleAsset(&asset, l.LogFile, nil)
		if zWasm != nil {
			if err == nil {
				if _err := l.handleBuildDetails(id, zWasm, nil); _err != nil {
					_err = fmt.Errorf("handling library build details failed with: %s", err)
					if err != nil {
						err = fmt.Errorf("%s:%w", err, _err)
					} else {
						err = _err
					}
				}
			}

			zWasm.Close()
		}
	}()

	if asset, err = l.HandleLibrary(); err != nil {
		return fmt.Errorf("handling library failed with: %s", err)
	}

	if id, err = l.getResourceRepositoryId(); err != nil {
		return fmt.Errorf("resource id for library repo failed with: %s", err)
	}

	if zWasm, err = asset.Compress(builders.WASM); err != nil {
		return fmt.Errorf("compressing build files failed with: %w", err)
	}

	return nil
}
