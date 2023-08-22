package jobs

import (
	"fmt"

	"github.com/taubyte/go-interfaces/builders"

	build "github.com/taubyte/builder"
)

func (w *website) handle() error {
	builder, err := build.New(w.ctx, w.WorkDir)
	if err != nil {
		return fmt.Errorf("creating new builder for git website repo `%d` failed with: %s", w.Job.Meta.Repository.ID, err)
	}
	defer builder.Close()

	var logs builders.Logs
	output, err := buildAndSetLog(builder, &logs, builder.Wd().Website().SetWorkDir())
	if err != nil {
		return fmt.Errorf("building website failed with: %s", err)
	}
	defer output.Close()

	zip, err := output.Compress(builders.Website)
	defer closeReader(zip)
	if err != nil {
		return logs.FormatErr("compressing build files failed with: %s", err)
	}

	_, err = logs.CopyTo(w.LogFile)
	if err != nil {
		return logs.FormatErr("copying logs failed with: %s", err)
	}

	webId, err := w.getResourceRepositoryId()
	if err != nil {
		return fmt.Errorf("resource id for website rep failed with: %s", err)
	}

	if err = w.handleBuildDetails(webId, zip, w.LogFile); err != nil {
		return fmt.Errorf("handling website build details failed with: %s", err)
	}

	return nil
}
