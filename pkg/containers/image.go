package containers

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/docker/docker/api/types/filters"
	"github.com/taubyte/tau/pkg/containers/core"
)

// Image initializes the given image, and attempts to pull the container from docker hub.
// If the Build() Option is provided then the given DockerFile tarball is built and returned.
func (c *Client) Image(ctx context.Context, name string, options ...ImageOption) (image *DockerImage, err error) {
	image = &DockerImage{
		backend: c.backend,
		client:  c, // Keep for backward compatibility
		image:   name,
		output:  os.Stdout,
	}

	for _, opt := range options {
		if err := opt(image); err != nil {
			return nil, errorImageOptions(name, err)
		}
	}

	imageExists := image.checkImageExists(ctx)
	if image.buildTarball != nil && (ForceRebuild || !imageExists) {
		if err := image.buildImage(ctx); err != nil {
			return nil, errorImageBuild(name, err)
		}
	} else {
		if image, err = image.Pull(ctx, nil); err != nil {
			err = errorImagePull(name, err)
			if !imageExists {
				image = nil
			}

			return image, err
		}
	}

	return
}

// checkImage checks the docker host client if the image is known.
func (i *DockerImage) checkImageExists(ctx context.Context) bool {
	return i.backend.Image(i.image).Exists(ctx)
}

// buildImage builds a DockerFile tarball as a docker image.
// Uses the backend for building if it supports building.
func (i *DockerImage) buildImage(ctx context.Context) error {
	if !i.backend.Capabilities().SupportsBuild {
		return errorImageBuildDockerFile(fmt.Errorf("backend does not support building images"))
	}

	buildInput := &dockerBuildInput{
		Context:    i.buildTarball,
		Dockerfile: "Dockerfile",
	}

	err := i.backend.Image(i.image).Build(ctx, buildInput)
	if err != nil {
		return errorImageBuildDockerFile(err)
	}

	return nil
}

// dockerBuildInput is a local struct that matches DockerBuildInput structure
// This avoids import cycles while allowing the docker backend to type-assert it
// Fields must be exported (capitalized) so reflection can access them
type dockerBuildInput struct {
	Context    io.Reader
	Dockerfile string
}

func (d *dockerBuildInput) Type() core.BackendType {
	return core.BackendTypeDocker
}

// Pull retrieves latest changes to the image from docker hub.
func (i *DockerImage) Pull(ctx context.Context, statusChan chan<- PullStatus) (*DockerImage, error) {
	err := i.backend.Image(i.image).Pull(ctx)
	if err != nil {
		return i, errorClientPull(err)
	}

	if statusChan != nil {
		select {
		case statusChan <- PullStatus{
			Status: "Image pulled successfully",
		}:
		default:
		}
	}

	return i, nil
}

// Instantiate sets given options and creates the container from the docker image.
func (i *DockerImage) Instantiate(ctx context.Context, options ...ContainerOption) (*Container, error) {
	c := &Container{
		backend: i.backend,
		image:   i, // Keep for backward compatibility
	}
	for _, opt := range options {
		err := opt(c)
		if err != nil {
			return nil, errorContainerOptions(i.image, err)
		}
	}

	// Convert old container options to ContainerConfig
	config := convertToContainerConfig(i.image, c)

	containerID, err := i.backend.Create(ctx, config)
	if err != nil {
		return nil, errorContainerCreate(i.image, err)
	}
	c.id = containerID

	return c, nil
}

// Clean removes all docker images that match the given filter, and max age.
// This method is deprecated - use backend image operations directly instead.
func (c *Client) Clean(ctx context.Context, age time.Duration, filter filters.Args) error {
	// Lazily initialize backend if not already initialized
	if c.backend == nil {
		backend, err := getDefaultBackend()
		if err != nil {
			return fmt.Errorf("failed to initialize backend: %w", err)
		}
		c.backend = backend
	}

	// Clean() method functionality would need to be implemented via backend
	// For now, return an error indicating this needs backend-specific implementation
	return fmt.Errorf("Clean() method not yet implemented via backend - use backend image operations directly")
}

// Name returns the name of the image
func (i *DockerImage) Name() string {
	return i.image
}

// Exists checks if the image exists locally without pulling
func (i *DockerImage) Exists(ctx context.Context) bool {
	return i.checkImageExists(ctx)
}
