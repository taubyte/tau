package containers

import (
	"context"
	"fmt"
)

// Run starts the container and waits for the container to exit before returning the container logs.
func (c *Container) Run(ctx context.Context) (*MuxedReadCloser, error) {
	imageName := ""
	if c.image != nil {
		imageName = c.image.image
	}

	// Start container using backend
	if err := c.backend.Start(ctx, c.id); err != nil {
		return nil, errorContainerStart(string(c.id), imageName, err)
	}

	// Wait for container to exit
	if err := c.Wait(ctx); err != nil {
		return nil, err
	}

	// Inspect container to get exit code
	info, err := c.backend.Inspect(ctx, c.id)
	if err != nil {
		return nil, errorContainerInspect(string(c.id), imageName, err)
	}

	var RetCodeErr error
	if info.ExitCode != 0 {
		RetCodeErr = errorContainerExitCode(string(c.id), imageName, info.ExitCode)
	}

	// Get container logs
	muxed, err := c.backend.Logs(ctx, c.id)
	if err != nil {
		return nil, errorContainerLogs(string(c.id), imageName, err)
	}

	c.Cleanup(ctx)

	return &MuxedReadCloser{reader: muxed}, RetCodeErr
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
		return errorContainerWait(string(c.id), imageName, err)
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
