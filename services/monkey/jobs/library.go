package jobs

import (
	"fmt"
	"io"

	"github.com/taubyte/tau/core/builders"
	build "github.com/taubyte/tau/pkg/builder"
)

func (c Context) HandleLibrary() (builders.Output, error) {
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

func (l library) handle() (err error) {
	var (
		asset           builders.Output
		id              string
		compressedAsset io.ReadSeekCloser
	)
	defer func() {
		l.mergeBuildLogs(asset.Logs())
		if compressedAsset != nil {
			if err == nil {
				if _err := l.handleCompressedBuild(id, compressedAsset); _err != nil {
					_err = fmt.Errorf("handling library build details failed with: %s", err)
					if err != nil {
						err = fmt.Errorf("%s:%w", err, _err)
					} else {
						err = _err
					}
				}
			}

			compressedAsset.Close()
		}
	}()

	if asset, err = l.HandleLibrary(); err != nil {
		return fmt.Errorf("handling library failed with: %s", err)
	}

	if id, err = l.getResourceRepositoryId(); err != nil {
		return fmt.Errorf("resource id for library repo failed with: %s", err)
	}

	if compressedAsset, err = asset.Compress(builders.WASM); err != nil {
		return fmt.Errorf("compressing build files failed with: %w", err)
	}

	return nil
}
