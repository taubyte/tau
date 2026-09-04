package logs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/urfave/cli/v2"

	buildv1 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v1"
	"github.com/taubyte/tau/tools/tau/cli/common"
	buildClient "github.com/taubyte/tau/tools/tau/clients/build"
)

// query is `tau logs`, and what it does that v1's could not is WATCH.
//
// v1's patrick could hand over a log only once the build had ended and the
// whole of it had reached IPFS, so asking for the logs of a running build
// printed nothing and gave no reason. Here the log is streamed from the node
// writing it, so the interesting case — the build you are waiting on right now
// — is the one this answers.
func (link) Query() common.Command {
	return common.Create(
		&cli.Command{
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:    "jid",
					Aliases: []string{"id"},
					Usage:   "(required) job id",
				},
				&cli.StringFlag{
					Name:    "output",
					Aliases: []string{"o"},
					Usage:   "write the log to this file instead of the terminal",
				},
				&cli.Int64Flag{
					Name:  "offset",
					Usage: "resume from this byte offset; what a previous run reported after it was cut off",
				},
			},
			Action: query,
		},
	)
}

func query(ctx *cli.Context) error {
	client, err := buildClient.Load()
	if err != nil {
		return err
	}

	jobID := ctx.String("jid")
	if jobID == "" {
		return errors.New("job id not set")
	}

	out := io.Writer(os.Stdout)
	if path := ctx.String("output"); path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o751); err != nil {
			return err
		}
		file, err := os.Create(path)
		if err != nil {
			return err
		}
		defer file.Close()
		out = file
	}

	at, err := client.Follow(ctx.Context, jobID, ctx.Int64("offset"), out)
	if err == nil {
		return nil
	}

	// A BUILD THAT IS NOT RUNNING IS NOT A FAILURE OF THIS COMMAND. It is the
	// common case for a finished job, and the reason is worth saying: what
	// survives a run is a content id, and reading one back is the object
	// store's business.
	if job, jobErr := client.Job(ctx.Context, jobID); jobErr == nil && finished(job) {
		return fmt.Errorf("job %s already %s; its output is not being written any more",
			jobID, job.GetStatus())
	}

	// WHERE IT GOT TO IS PART OF THE ERROR, because that number is what makes
	// the next attempt resume rather than start over.
	if at > 0 {
		return fmt.Errorf("%w (resume with --offset %d)", err, at)
	}
	return err
}

func finished(job *buildv1.Job) bool {
	switch job.GetStatus() {
	case buildv1.JobStatus_JobStatusSuccess,
		buildv1.JobStatus_JobStatusFailed,
		buildv1.JobStatus_JobStatusCancelled:
		return true
	}
	return false
}
