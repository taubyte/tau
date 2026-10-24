package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/moby/moby/api/types/build"
	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/containers/reserve"
	"github.com/taubyte/tau/pkg/netguard"
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

	return i.backend.pull(ctx, i.name, nil)
}

// pull fetches an image from its registry, for the platforms given or the
// daemon's own.
func (b *DockerBackend) pull(ctx context.Context, name string, platforms []ocispec.Platform) error {
	reader, err := b.client.ImagePull(ctx, name, client.ImagePullOptions{Platforms: platforms})
	if err != nil {
		return fmt.Errorf("failed to pull image %s: %w", name, err)
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

// Build builds an image from a Dockerfile. The docker daemon builds natively,
// so the context tar is handed straight to it, unless the build is restricted:
// then what the daemon is sent is restrictContext's.
func (i *dockerImage) Build(ctx context.Context, input *core.DockerfileBuild) error {
	if i.backend.client == nil {
		return fmt.Errorf("Docker client not initialized")
	}

	if input == nil || input.Context == nil {
		return fmt.Errorf("build requires a context")
	}

	buildContext := input.Context

	buildOptions := client.ImageBuildOptions{
		Tags:       []string{i.name},
		Remove:     true,
		Dockerfile: input.DockerfileName(),
	}

	// A RUN step is a container of the daemon's own making, so the only handle
	// on its egress is the network it runs on. Same fail-closed rule as Create:
	// no firewall, no build.
	if input.RestrictEgress {
		if err := i.backend.ensureRestrictedNetwork(ctx); err != nil {
			return err
		}
		if _, err := net.InterfaceByName(restrictedBridge); err != nil {
			return fmt.Errorf("restricted egress needs the %s bridge in tau's own network namespace (a rootless docker keeps it in its own); refusing to fail open: %w", restrictedBridge, err)
		}
		if err := netguard.InstallBridgeFilter(restrictedBridge); err != nil {
			return fmt.Errorf("installing egress firewall (image not built): %w", err)
		}

		spooled, err := i.backend.restrictBuild(ctx, input, &buildOptions, "", reserve.Default)
		if err != nil {
			return err
		}
		defer spooled.remove()
		if buildContext, err = spooled.finish(); err != nil {
			return fmt.Errorf("spooling build context: %w", err)
		}
	}

	return i.backend.build(ctx, buildContext, buildOptions)
}

// restrictBuild readies a restricted build once its firewall is in place: on
// the restricted network, with the classic builder, from the context
// restrictContext makes of the one given.
func (b *DockerBackend) restrictBuild(ctx context.Context, input *core.DockerfileBuild, buildOptions *client.ImageBuildOptions, dir string, floor reserve.Floor) (*buildContext, error) {
	buildOptions.NetworkMode = restrictedNetwork
	// What restrictContext holds a Dockerfile to is what the classic builder
	// fetches; BuildKit is another builder with other rules.
	buildOptions.Version = build.BuilderV1
	return b.restrictContext(ctx, input.Context, input.DockerfileName(), dir, floor)
}

// build sends the daemon a build and answers once it has finished, with the
// error the build stream reports.
func (b *DockerBackend) build(ctx context.Context, buildContext io.Reader, buildOptions client.ImageBuildOptions) error {
	buildOptions.Context = buildContext

	buildResponse, err := b.client.ImageBuild(ctx, buildContext, buildOptions)
	if err != nil {
		return fmt.Errorf("failed to build image %s: %w", strings.Join(buildOptions.Tags, ", "), err)
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

// Exists checks if the image exists locally using ImageList with reference filter.
// If the filter returns 0 images, ImageInspectWithRaw is tried as fallback (same resolution as "docker run").
func (i *dockerImage) Exists(ctx context.Context) bool {
	if i.backend.client == nil {
		return false
	}

	res, err := i.backend.client.ImageList(ctx, client.ImageListOptions{
		Filters: client.Filters{}.Add("reference", i.name),
	})
	if err != nil {
		return false
	}

	if len(res.Items) == 0 {
		_, inspectErr := i.backend.client.ImageInspect(ctx, i.name)
		return inspectErr == nil
	}

	return true
}

// Remove removes the image from the backend
func (i *dockerImage) Remove(ctx context.Context) error {
	if i.backend.client == nil {
		return fmt.Errorf("Docker client not initialized")
	}

	_, err := i.backend.client.ImageRemove(ctx, i.name, client.ImageRemoveOptions{
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

	image, err := i.backend.client.ImageInspect(ctx, i.name)
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

	image, err := i.backend.client.ImageInspect(ctx, i.name)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect image %s: %w", i.name, err)
	}

	return image.RepoTags, nil
}
