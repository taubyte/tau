package jobs

import (
	"fmt"
	"io"

	"github.com/taubyte/tau/core/builders"

	build "github.com/taubyte/tau/pkg/builder"
)

func (w website) handle() (err error) {
	builder, err := build.New(w.ctx, w.WorkDir)
	if err != nil {
		return fmt.Errorf("creating new builder for git website repo `%d` failed with: %w", w.Job.Meta.Repository.ID, err)
	}
	defer builder.Close()

	var (
		asset           builders.Output
		id              string
		compressedAsset io.ReadSeekCloser
	)
	defer func() {
		w.mergeBuildLogs(asset.Logs())
		if compressedAsset != nil {
			if err == nil {
				if err = w.handleCompressedBuild(id, compressedAsset); err != nil {
					err = fmt.Errorf("handling website build details failed with: %w", err)
				}
			}

			compressedAsset.Close()
		}

		builder.Close()
	}()

	if asset, err = builder.Build(builder.Wd().Website().SetWorkDir()); err != nil {
		return fmt.Errorf("building website failed with: %w", err)
	}

	if compressedAsset, err = asset.Compress(builders.Website); err != nil {
		return fmt.Errorf("compressing build files failed with: %w", err)
	}

	if id, err = w.getResourceRepositoryId(); err != nil {
		return fmt.Errorf("resource id for website rep failed with: %w", err)
	}

	if err != nil {
		w.LogFile.WriteString(fmt.Sprintf("Error: %s\n", err))
	}

	// Create CID and update job
	logCid, err := w.storeLogFile(w.LogFile)
	if err != nil {
		return fmt.Errorf("storing log file failed with: %w", err)
	}

	// Update job with CID
	if err := w.updateJobWithCid(id, logCid); err != nil {
		return fmt.Errorf("updating job with CID failed with: %w", err)
	}

	return nil
}

// updateJobWithCid updates the job with the given CID.
func (w *website) updateJobWithCid(jobId, logCid string) error {
	// Implement the logic to update the job with the CID
	// This is a placeholder implementation
	fmt.Printf("Updating job %s with CID %s\n", jobId, logCid)
	return nil
}
