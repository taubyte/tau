package containers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// Run starts the container, waits for it to exit and returns its logs. Logs are
// returned on a non-zero exit too, so callers see build output; that status comes
// back as an error wrapping ErrorExitCode.
func (c *Container) Run(ctx context.Context) (*MuxedReadCloser, error) {
	var buf bytes.Buffer
	err := c.run(ctx, func(stream io.Reader) error {
		_, err := io.Copy(&buf, stream)
		return err
	})
	if err != nil && !errors.Is(err, ErrorExitCode) {
		return nil, err
	}
	return &MuxedReadCloser{reader: io.NopCloser(&buf)}, err
}

// Stream starts the container and writes its output to stdout and stderr as
// the container produces it, returning once it has exited and all of its
// output is written. A non-zero exit comes back as an error wrapping
// ErrorExitCode, after the output that led to it.
func (c *Container) Stream(ctx context.Context, stdout, stderr io.Writer) error {
	return c.run(ctx, func(stream io.Reader) error {
		return demux(stdout, stderr, stream)
	})
}

// run starts the container and hands consume its log stream while it runs.
// The stream is read to the end before the container is removed, because
// removing a container cuts a stream still being read from it and silently
// truncates the output.
func (c *Container) run(ctx context.Context, consume func(io.Reader) error) error {
	imageName := ""
	if c.image != nil {
		imageName = c.image.image
	}

	// The container already exists — Instantiate created it — so every exit from
	// here has to take it with it, or a failed build step leaves a container and
	// its snapshot behind on the host. Cleanup uses its own context because the
	// caller's may be exactly what just expired.
	cleaned := false
	defer func() {
		if !cleaned {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			defer cancel()
			c.Cleanup(cleanupCtx)
		}
	}()

	if err := c.backend.Start(ctx, c.id); err != nil {
		return errorContainerStart(c.id, imageName, err)
	}

	stream, err := c.backend.Logs(ctx, c.id)
	if err != nil {
		return errorContainerLogs(c.id, imageName, err)
	}
	consumed := consume(stream)
	stream.Close()

	if err := c.Wait(ctx); err != nil {
		return err
	}
	if consumed != nil {
		return errorContainerLogs(c.id, imageName, consumed)
	}

	info, err := c.backend.Inspect(ctx, c.id)
	if err != nil {
		return errorContainerInspect(c.id, imageName, err)
	}

	if err := c.Cleanup(ctx); err == nil {
		cleaned = true
	}

	if info.ExitCode != 0 {
		return errorContainerExitCode(c.id, imageName, info.ExitCode)
	}
	return nil
}

// Wait returns once the container has exited, or waiting failed.
func (c *Container) Wait(ctx context.Context) error {
	imageName := ""
	if c.image != nil {
		imageName = c.image.image
	}

	err := c.backend.Wait(ctx, c.id)
	if err != nil {
		return errorContainerWait(c.id, imageName, err)
	}
	return nil
}

func (c *Container) Cleanup(ctx context.Context) error {
	if err := c.backend.Remove(ctx, c.id); err != nil {
		return fmt.Errorf("removing container with id `%s` failed with: %s", c.id, err)
	}
	return nil
}
