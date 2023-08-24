package jobs

import (
	"fmt"
	"io"

	"github.com/taubyte/go-interfaces/builders"

	build "github.com/taubyte/builder"
)

func (w *website) handle() (err error) {
	builder, err := build.New(w.ctx, w.WorkDir)
	if err != nil {
		return fmt.Errorf("creating new builder for git website repo `%d` failed with: %w", w.Job.Meta.Repository.ID, err)
	}
	defer builder.Close()

	var (
		asset builders.Output
		id    string
		zip   io.ReadSeekCloser
	)
	defer func() {
		handleAsset(&asset, w.LogFile, nil)
		if zip != nil {
			if err == nil {
				if err = w.handleBuildDetails(id, zip, w.LogFile); err != nil {
					err = fmt.Errorf("handling website build details failed with: %w", err)
				}
			}

			zip.Close()
		}

		builder.Close()
	}()

	if asset, err = builder.Build(builder.Wd().Website().SetWorkDir()); err != nil {
		return fmt.Errorf("building website failed with: %w", err)
	}

	if zip, err = asset.Compress(builders.Website); err != nil {
		return fmt.Errorf("compressing build files failed with: %w", err)
	}

	if id, err = w.getResourceRepositoryId(); err != nil {
		return fmt.Errorf("resource id for website rep failed with: %w", err)
	}

	return nil
}
