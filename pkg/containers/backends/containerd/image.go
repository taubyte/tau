//go:build linux

package containerd

import (
	"context"
	"fmt"
	"strings"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/namespaces"
	"github.com/taubyte/tau/pkg/containers/core"
)

// containerdImage implements the core.Image interface for containerd
type containerdImage struct {
	backend *ContainerdBackend
	name    string
}

// Pull retrieves the image from a registry/repository
func (i *containerdImage) Pull(ctx context.Context) error {
	if i.backend.client == nil {
		return fmt.Errorf("containerd client not initialized")
	}

	ctx = namespaces.WithNamespace(ctx, i.backend.config.Namespace)

	_, err := i.backend.client.Pull(ctx, i.name, containerd.WithPullUnpack)
	if err != nil {
		return fmt.Errorf("failed to pull image %s: %w", i.name, err)
	}

	return nil
}

// Build builds an image from backend-specific inputs
func (i *containerdImage) Build(ctx context.Context, input core.BuildInput) error {
	return core.ErrBuildNotSupported
}

// Exists checks if the image exists locally
func (i *containerdImage) Exists(ctx context.Context) bool {
	if i.backend.client == nil {
		return false
	}

	ctx = namespaces.WithNamespace(ctx, i.backend.config.Namespace)

	image, err := i.backend.client.GetImage(ctx, i.name)
	if err != nil {
		return false
	}

	return image != nil
}

// Remove removes the image from the backend
func (i *containerdImage) Remove(ctx context.Context) error {
	if i.backend.client == nil {
		return fmt.Errorf("containerd client not initialized")
	}

	ctx = namespaces.WithNamespace(ctx, i.backend.config.Namespace)

	imageService := i.backend.client.ImageService()
	err := imageService.Delete(ctx, i.name)
	if err != nil {
		return fmt.Errorf("failed to remove image %s: %w", i.name, err)
	}

	return nil
}

// Name returns the image name/identifier
func (i *containerdImage) Name() string {
	return i.name
}

// Digest returns the image digest
func (i *containerdImage) Digest(ctx context.Context) (string, error) {
	if i.backend.client == nil {
		return "", fmt.Errorf("containerd client not initialized")
	}

	ctx = namespaces.WithNamespace(ctx, i.backend.config.Namespace)

	image, err := i.backend.client.GetImage(ctx, i.name)
	if err != nil {
		return "", fmt.Errorf("failed to get image %s: %w", i.name, err)
	}

	digest := image.Target().Digest.String()
	if strings.HasPrefix(digest, "sha256:") {
		return strings.TrimPrefix(digest, "sha256:"), nil
	}

	return digest, nil
}

// Tags returns all tags for this image
func (i *containerdImage) Tags(ctx context.Context) ([]string, error) {
	if i.backend.client == nil {
		return nil, fmt.Errorf("containerd client not initialized")
	}

	ctx = namespaces.WithNamespace(ctx, i.backend.config.Namespace)

	image, err := i.backend.client.GetImage(ctx, i.name)
	if err != nil {
		return nil, fmt.Errorf("failed to get image %s: %w", i.name, err)
	}

	digest := image.Target().Digest

	imageService := i.backend.client.ImageService()
	allImages, err := imageService.List(ctx)
	if err != nil {
		return []string{i.name}, nil
	}

	var tags []string
	seen := make(map[string]bool)
	for _, img := range allImages {
		if img.Target.Digest == digest {
			name := img.Name
			if !seen[name] {
				tags = append(tags, name)
				seen[name] = true
			}
		}
	}

	if len(tags) == 0 {
		tags = []string{i.name}
	}

	return tags, nil
}
