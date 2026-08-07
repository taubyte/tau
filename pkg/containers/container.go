package containers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"
)

// Run starts the container and waits for the container to exit before returning the container logs.
// Logs are returned even when the container exits with a non-zero status so callers
// can inspect build output (e.g. compiler errors); that status comes back as an
// error wrapping ErrorExitCode.
func (c *Container) Run(ctx context.Context) (*MuxedReadCloser, error) {
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
		return nil, errorContainerStart(c.id, imageName, err)
	}

	if err := c.Wait(ctx); err != nil {
		return nil, err
	}

	info, err := c.backend.Inspect(ctx, c.id)
	if err != nil {
		return nil, errorContainerInspect(c.id, imageName, err)
	}

	logs, err := c.readLogs(ctx)
	if err != nil {
		return nil, errorContainerLogs(c.id, imageName, err)
	}

	if err := c.Cleanup(ctx); err == nil {
		cleaned = true
	}

	if info.ExitCode != 0 {
		return logs, errorContainerExitCode(c.id, imageName, info.ExitCode)
	}

	return logs, nil
}

// readLogs drains the container's log stream. It is read to the end before Run
// removes the container, because removing a container cuts a stream still being
// read from it and silently truncates the build output.
//
// ponytail: whole log held in memory; spill to a temp file if build logs ever outgrow that.
func (c *Container) readLogs(ctx context.Context) (*MuxedReadCloser, error) {
	stream, err := c.backend.Logs(ctx, c.id)
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, stream); err != nil {
		return nil, err
	}

	return &MuxedReadCloser{reader: io.NopCloser(&buf)}, nil
}

// Wait calls the ContainerWait method for the container, and returns once a response has been received.
// If there is an error response then wait will return the error
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

// Cleanup removes the container from the docker host client.
func (c *Container) Cleanup(ctx context.Context) error {
	if err := c.backend.Remove(ctx, c.id); err != nil {
		return fmt.Errorf("removing container with id `%s` failed with: %s", c.id, err)
	}
	return nil
}
