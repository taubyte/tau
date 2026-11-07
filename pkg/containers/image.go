package containers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/taubyte/tau/pkg/containers/core"
)

// Image initializes the given image. It tries to pull from the registry first to get the latest;
// if pull fails and the image exists locally, that image is used. If the Build() Option is provided
// then the given DockerFile tarball is built and returned.
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

	defer markUsed(name)

	imageExists := image.checkImageExists(ctx)
	if image.buildTarball != nil && (ForceRebuild || !imageExists) {
		if err := image.buildImage(ctx); err != nil {
			return nil, errorImageBuild(name, err)
		}
	} else if imageExists && pulledWithin(name, image.reusePulled) {
		return image, nil
	} else {
		if _, err := image.Pull(ctx, nil); err != nil {
			if imageExists {
				return image, nil
			}
			return nil, errorImagePull(name, err)
		}
		markPulled(name)
	}

	return
}

var (
	// lastPulled and lastUsed are keyed by image name and hold a time.Time.
	lastPulled sync.Map
	lastUsed   sync.Map
)

func markPulled(name string) { lastPulled.Store(name, time.Now()) }
func markUsed(name string)   { lastUsed.Store(name, time.Now()) }

// pulledWithin reports whether this process pulled name less than age ago.
func pulledWithin(name string, age time.Duration) bool {
	if age <= 0 {
		return false
	}
	at, ok := lastPulled.Load(name)
	return ok && time.Since(at.(time.Time)) < age
}

// CleanUnused removes the images this process has used but not for idle or
// longer, except those named in keep. Images it never used are left alone:
// they are not tau's to remove.
// An image a container still holds fails to remove and is tried again later.
func (c *Client) CleanUnused(ctx context.Context, idle time.Duration, keep ...string) error {
	var errs []error
	lastUsed.Range(func(key, value any) bool {
		name := key.(string)
		if slices.Contains(keep, name) || time.Since(value.(time.Time)) < idle {
			return true
		}
		if err := c.backend.Image(name).Remove(ctx); err != nil {
			errs = append(errs, fmt.Errorf("removing image %s: %w", name, err))
			return true
		}
		lastUsed.Delete(name)
		lastPulled.Delete(name)
		return true
	})
	return errors.Join(errs...)
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

	err := i.backend.Image(i.image).Build(ctx, &core.DockerfileBuild{
		Context:        i.buildTarball,
		RestrictEgress: i.restrictedEgress,
	})
	if err != nil {
		return errorImageBuildDockerFile(err)
	}

	return nil
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

// Clean removes images older than age, optionally scoped to one reference.
func (c *Client) Clean(ctx context.Context, age time.Duration, reference string) error {
	if c.backend == nil {
		backend, err := getDefaultBackend()
		if err != nil {
			return fmt.Errorf("failed to initialize backend: %w", err)
		}
		c.backend = backend
	}

	return c.backend.Clean(ctx, age, reference)
}

// Name returns the name of the image
func (i *DockerImage) Name() string {
	return i.image
}

// Exists checks if the image exists locally without pulling
func (i *DockerImage) Exists(ctx context.Context) bool {
	return i.checkImageExists(ctx)
}
