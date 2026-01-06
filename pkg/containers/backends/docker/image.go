package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/taubyte/tau/pkg/containers/core"
)

// dockerImage implements the core.Image interface for Docker
type dockerImage struct {
	backend *DockerBackend
	name    string
}

// Pull retrieves the image from a registry/repository
func (i *dockerImage) Pull(ctx context.Context) error {
	if i.backend.client == nil {
		return fmt.Errorf("Docker client not initialized")
	}

	reader, err := i.backend.client.ImagePull(ctx, i.name, types.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull image %s: %w", i.name, err)
	}
	defer reader.Close()

	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		var status struct {
			Error       string `json:"error"`
			ErrorDetail struct {
				Message string `json:"message"`
			} `json:"errorDetail"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &status); err == nil {
			if status.Error != "" {
				return fmt.Errorf("docker pull failed: %s", status.Error)
			}
			if status.ErrorDetail.Message != "" {
				return fmt.Errorf("docker pull failed: %s", status.ErrorDetail.Message)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read pull response: %w", err)
	}

	return nil
}

// Build builds an image from backend-specific inputs
func (i *dockerImage) Build(ctx context.Context, input core.BuildInput) error {
	if i.backend.client == nil {
		return fmt.Errorf("Docker client not initialized")
	}

	if input.Type() != core.BackendTypeDocker {
		return fmt.Errorf("build input type %s not supported for Docker backend", input.Type())
	}

	// Try type assertion to DockerBuildInput first
	dockerInput, ok := input.(*DockerBuildInput)
	if !ok {
		// If type assertion fails, try to extract fields using reflection
		// This allows containers package to pass a compatible struct without importing DockerBuildInput
		dockerInput = extractDockerBuildInput(input)
		if dockerInput == nil {
			return fmt.Errorf("invalid build input type for Docker backend - expected DockerBuildInput or compatible struct")
		}
	}

	buildOptions := types.ImageBuildOptions{
		Tags:       []string{i.name},
		Remove:     true,
		Dockerfile: dockerInput.Dockerfile,
		Context:    dockerInput.Context,
	}

	buildResponse, err := i.backend.client.ImageBuild(ctx, dockerInput.Context, buildOptions)
	if err != nil {
		return fmt.Errorf("failed to build image %s: %w", i.name, err)
	}
	defer buildResponse.Body.Close()

	scanner := bufio.NewScanner(buildResponse.Body)
	scanner.Split(bufio.ScanLines)

	for scanner.Scan() {
		line := scanner.Text()
		var status struct {
			Stream      string `json:"stream"`
			Error       string `json:"error"`
			ErrorDetail struct {
				Message string `json:"message"`
			} `json:"errorDetail"`
		}

		if err := json.Unmarshal([]byte(line), &status); err != nil {
			continue
		}

		if status.Error != "" {
			return fmt.Errorf("docker build failed: %s", status.Error)
		}

		if status.ErrorDetail.Message != "" {
			return fmt.Errorf("docker build failed: %s", status.ErrorDetail.Message)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read build response: %w", err)
	}

	return nil
}

// Exists checks if the image exists locally
func (i *dockerImage) Exists(ctx context.Context) bool {
	if i.backend.client == nil {
		return false
	}

	filter := filters.NewArgs()
	filter.Add("reference", i.name)

	images, err := i.backend.client.ImageList(ctx, types.ImageListOptions{
		Filters: filter,
	})

	return err == nil && len(images) > 0
}

// Remove removes the image from the backend
func (i *dockerImage) Remove(ctx context.Context) error {
	if i.backend.client == nil {
		return fmt.Errorf("Docker client not initialized")
	}

	_, err := i.backend.client.ImageRemove(ctx, i.name, types.ImageRemoveOptions{
		Force:         false,
		PruneChildren: true,
	})

	if err != nil {
		return fmt.Errorf("failed to remove image %s: %w", i.name, err)
	}

	return nil
}

// Name returns the image name/identifier
func (i *dockerImage) Name() string {
	return i.name
}

// Digest returns the image digest
func (i *dockerImage) Digest(ctx context.Context) (string, error) {
	if i.backend.client == nil {
		return "", fmt.Errorf("Docker client not initialized")
	}

	image, _, err := i.backend.client.ImageInspectWithRaw(ctx, i.name)
	if err != nil {
		return "", fmt.Errorf("failed to inspect image %s: %w", i.name, err)
	}

	if len(image.RepoDigests) > 0 {
		parts := strings.Split(image.RepoDigests[0], "@")
		if len(parts) == 2 {
			digest := strings.TrimPrefix(parts[1], "sha256:")
			return digest, nil
		}
	}

	digest := strings.TrimPrefix(image.ID, "sha256:")

	return digest, nil
}

// Tags returns all tags for this image
func (i *dockerImage) Tags(ctx context.Context) ([]string, error) {
	if i.backend.client == nil {
		return nil, fmt.Errorf("Docker client not initialized")
	}

	image, _, err := i.backend.client.ImageInspectWithRaw(ctx, i.name)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect image %s: %w", i.name, err)
	}

	return image.RepoTags, nil
}

// DockerBuildInput represents build input for Docker backend
type DockerBuildInput struct {
	Context    io.Reader // Build context (tarball)
	Dockerfile string    // Dockerfile path within context (default: "Dockerfile")
}

// Type returns the backend type this input is for
func (d *DockerBuildInput) Type() core.BackendType {
	return core.BackendTypeDocker
}

// extractDockerBuildInput extracts build input fields using reflection
// This allows the containers package to pass compatible structs without importing DockerBuildInput
func extractDockerBuildInput(input core.BuildInput) *DockerBuildInput {
	if input == nil {
		return nil
	}

	v := reflect.ValueOf(input)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return nil
	}

	result := &DockerBuildInput{}

	// Try to extract Context field
	if contextField := v.FieldByName("Context"); contextField.IsValid() && contextField.CanInterface() {
		if reader, ok := contextField.Interface().(io.Reader); ok {
			result.Context = reader
		}
	}

	// Try to extract Dockerfile field
	if dockerfileField := v.FieldByName("Dockerfile"); dockerfileField.IsValid() && dockerfileField.CanInterface() {
		if str, ok := dockerfileField.Interface().(string); ok {
			result.Dockerfile = str
		}
	}

	// If we got both fields, return the result
	if result.Context != nil && result.Dockerfile != "" {
		return result
	}

	return nil
}
