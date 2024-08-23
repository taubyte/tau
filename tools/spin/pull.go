package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/taubyte/tau/pkg/spin"
	"github.com/taubyte/tau/pkg/spin/registry"
	"github.com/urfave/cli/v2"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

var pullCommand = &cli.Command{
	Name:  "pull",
	Usage: "pulls and converts container images",
	Action: func(ctx *cli.Context) error {
		reg, err := registry.New(ctx.Context, root())
		if err != nil {
			return fmt.Errorf("registry init failed with %w", err)
		}

		image := ctx.Args().First()
		if image == "" {
			return errors.New("you need to provide image name")
		}

		pbar := mpb.New(mpb.WithWidth(60), mpb.WithRefreshRate(100*time.Millisecond), mpb.WithOutput(os.Stderr))
		pullChan := make(chan error, 1)

		pull(ctx.Context, reg, image, pbar, pullChan, false)

		err = <-pullChan
		if err != nil {
			return fmt.Errorf("pulling image failed with %w", err)
		}

		pbar.Wait()

		return nil
	},
}

func pull(ctx context.Context, reg spin.Registry, image string, pbar *mpb.Progress, pullChan chan<- error, remove bool) {
	progress := make(chan spin.PullProgress, 1024)
	var pullErr error
	go func() {
		opts := []mpb.BarOption{
			mpb.AppendDecorators(decor.Percentage()),
			mpb.PrependDecorators(
				decor.Name("Pulling "),
			),
		}
		if remove {
			opts = append(opts, mpb.BarRemoveOnComplete())
		}
		pullBar, _ := pbar.Add(100,
			mpb.BarStyle().Build(),
			opts...,
		)
		for pr := range progress {
			if pr.Error() != io.EOF {
				pullBar.SetCurrent(int64(pr.Completion()))
			}
		}
		pullBar.SetCurrent(100)
		pullChan <- pullErr
	}()
	go func() {
		pullErr = reg.Pull(ctx, image, progress)
		close(progress)
	}()
}
