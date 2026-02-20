//go:build docker_integration

package docker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/core"
)

func TestImage_Integration(t *testing.T) {
	t.Run("Name", func(t *testing.T) {
		image := &dockerImage{
			name: "alpine:latest",
		}

		assert.Equal(t, "alpine:latest", image.Name())
	})

	t.Run("Exists", func(t *testing.T) {
		t.Run("NoClient", func(t *testing.T) {
			image := &dockerImage{
				backend: &DockerBackend{
					client: nil,
				},
				name: "alpine:latest",
			}

			exists := image.Exists(context.Background())
			assert.False(t, exists, "Exists should return false when client is nil")
		})

		t.Run("Integration", func(t *testing.T) {
			backend, err := New(core.DockerConfig{})
			require.NoError(t, err, "Backend creation must succeed - Docker is required")
			require.NotNil(t, backend, "Backend must not be nil")

			defer func() {
				require.NotNil(t, backend.client, "Client must exist for cleanup")
				require.NoError(t, backend.client.Close(), "Client close must succeed")
			}()

			image := backend.Image("alpine:latest")
			require.NotNil(t, image, "Image must not be nil")

			exists := image.Exists(context.Background())
			assert.IsType(t, false, exists, "Exists must return a boolean")
		})
	})
}

func TestImage_Pull_Integration(t *testing.T) {
	backend, err := New(core.DockerConfig{})
	require.NoError(t, err, "Backend creation must succeed - Docker is required")
	require.NotNil(t, backend, "Backend must not be nil")

	defer func() {
		require.NotNil(t, backend.client, "Client must exist for cleanup")
		require.NoError(t, backend.client.Close(), "Client close must succeed")
	}()

	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		image := backend.Image("alpine:latest")
		require.NotNil(t, image, "Image must not be nil")

		if image.Exists(ctx) {
			image.Remove(ctx)
		}

		err = image.Pull(ctx)
		require.NoError(t, err, "Image pull must succeed")
		require.True(t, image.Exists(ctx), "Image must exist after pull")
	})

	t.Run("NoClient", func(t *testing.T) {
		image := &dockerImage{
			backend: &DockerBackend{
				client: nil,
			},
			name: "alpine:latest",
		}

		err := image.Pull(context.Background())
		assert.Error(t, err, "Pull must fail when client is nil")
		assert.Contains(t, err.Error(), "not initialized")
	})

	t.Run("InvalidImage", func(t *testing.T) {
		image := backend.Image("invalid-image-name-that-does-not-exist:999999")
		require.NotNil(t, image, "Image must not be nil")

		err = image.Pull(context.Background())
		assert.Error(t, err, "Pull must fail for invalid image")
	})

	t.Run("ErrorInStatus", func(t *testing.T) {
		image := backend.Image("invalid-image-that-will-fail:999999")
		require.NotNil(t, image, "Image must not be nil")

		err = image.Pull(context.Background())
		assert.Error(t, err, "Pull must fail for invalid image")
		assert.Contains(t, err.Error(), "failed to pull image", "Error must indicate pull failure")
	})

	t.Run("ErrorDetailInStatus", func(t *testing.T) {
		image := backend.Image("invalid-repo/invalid-image:999999")
		require.NotNil(t, image, "Image must not be nil")

		err = image.Pull(context.Background())
		assert.Error(t, err, "Pull must fail for invalid image")
	})

	t.Run("ScannerError", func(t *testing.T) {
		image := backend.Image("alpine:latest")
		require.NotNil(t, image, "Image must not be nil")

		dockerImg, ok := image.(*dockerImage)
		require.True(t, ok, "Image must be *dockerImage")

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err = dockerImg.Pull(ctx)
		assert.Error(t, err, "Pull must fail with cancelled context")
	})
}

func TestImage_Build_Integration(t *testing.T) {
	backend, err := New(core.DockerConfig{})
	require.NoError(t, err, "Backend creation must succeed - Docker is required")
	require.NotNil(t, backend, "Backend must not be nil")

	defer func() {
		require.NotNil(t, backend.client, "Client must exist for cleanup")
		require.NoError(t, backend.client.Close(), "Client close must succeed")
	}()

	ctx := context.Background()

	t.Run("NoClient", func(t *testing.T) {
		image := &dockerImage{
			backend: &DockerBackend{
				client: nil,
			},
			name: "test-image:latest",
		}

		buildInput := &DockerBuildInput{
			Context:    strings.NewReader("test"),
			Dockerfile: "Dockerfile",
		}

		err := image.Build(context.Background(), buildInput)
		assert.Error(t, err, "Build must fail when client is nil")
		assert.Contains(t, err.Error(), "not initialized")
	})

	t.Run("WrongBackendType", func(t *testing.T) {
		image := backend.Image("test-image:latest")
		require.NotNil(t, image, "Image must not be nil")

		mockInput := &mockBuildInput{
			backendType: core.BackendTypeContainerd,
		}

		err = image.Build(context.Background(), mockInput)
		assert.Error(t, err, "Build must fail with wrong backend type")
		assert.Contains(t, err.Error(), "not supported")
	})

	t.Run("InvalidTypeAssertion", func(t *testing.T) {
		image := backend.Image("test-image:latest")
		require.NotNil(t, image, "Image must not be nil")

		mockInput := &mockBuildInput{
			backendType: core.BackendTypeDocker,
		}

		err = image.Build(context.Background(), mockInput)
		assert.Error(t, err, "Build must fail with invalid type assertion")
		assert.Contains(t, err.Error(), "invalid build input type")
	})

	t.Run("Success", func(t *testing.T) {
		wd, err := os.Getwd()
		require.NoError(t, err, "Getting working directory must succeed")

		fixturePath := filepath.Join(wd, "..", "..", "fixtures", "docker.tar")

		file, err := os.Open(fixturePath)
		require.NoError(t, err, "Opening docker tarball fixture must succeed")
		defer file.Close()

		randomTag := fmt.Sprintf("test-build-success:%d", time.Now().UnixNano())
		image := backend.Image(randomTag)
		require.NotNil(t, image, "Image must not be nil")

		buildInput := &DockerBuildInput{
			Context:    file,
			Dockerfile: "Dockerfile",
		}

		err = image.Build(ctx, buildInput)
		require.NoError(t, err, "Build must succeed with valid tarball")
		require.True(t, image.Exists(ctx), "Image must exist after successful build")

		defer func() {
			if image.Exists(ctx) {
				image.Remove(ctx)
			}
		}()
	})

	t.Run("InvalidContext", func(t *testing.T) {
		randomTag := fmt.Sprintf("test-build-valid:%d", time.Now().UnixNano())
		image := backend.Image(randomTag)
		require.NotNil(t, image, "Image must not be nil")

		dockerfile := "FROM alpine:latest\nRUN echo 'test'"
		buildInput := &DockerBuildInput{
			Context:    strings.NewReader(dockerfile),
			Dockerfile: "Dockerfile",
		}

		err = image.Build(ctx, buildInput)
		assert.Error(t, err, "Build must fail with invalid context (not a tarball)")
		assert.Contains(t, err.Error(), "failed to build image", "Error must indicate build failure")

		defer func() {
			if image.Exists(ctx) {
				image.Remove(ctx)
			}
		}()
	})

	t.Run("ErrorInStatus", func(t *testing.T) {
		image := backend.Image("test-build-error:latest")
		require.NotNil(t, image, "Image must not be nil")

		invalidDockerfile := "INVALID DOCKERFILE SYNTAX !!!"
		buildInput := &DockerBuildInput{
			Context:    strings.NewReader(invalidDockerfile),
			Dockerfile: "Dockerfile",
		}

		err = image.Build(context.Background(), buildInput)
		assert.Error(t, err, "Build must fail with invalid Dockerfile")
	})

	t.Run("ErrorDetailInStatus", func(t *testing.T) {
		image := backend.Image("test-build-error-detail:latest")
		require.NotNil(t, image, "Image must not be nil")

		invalidContext := "NOT A VALID TARBALL"
		buildInput := &DockerBuildInput{
			Context:    strings.NewReader(invalidContext),
			Dockerfile: "Dockerfile",
		}

		err = image.Build(context.Background(), buildInput)
		assert.Error(t, err, "Build must fail with invalid context")
	})

	t.Run("ScannerError", func(t *testing.T) {
		image := backend.Image("test-build-image:latest")
		require.NotNil(t, image, "Image must not be nil")

		dockerfile := "FROM alpine:latest\nRUN echo 'test'"
		buildInput := &DockerBuildInput{
			Context:    strings.NewReader(dockerfile),
			Dockerfile: "Dockerfile",
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err = image.Build(ctx, buildInput)
		assert.Error(t, err, "Build must fail with cancelled context")
	})
}

func TestImage_Remove_Integration(t *testing.T) {
	backend, err := New(core.DockerConfig{})
	require.NoError(t, err, "Backend creation must succeed - Docker is required")
	require.NotNil(t, backend, "Backend must not be nil")

	defer func() {
		require.NotNil(t, backend.client, "Client must exist for cleanup")
		require.NoError(t, backend.client.Close(), "Client close must succeed")
	}()

	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		randomTag := fmt.Sprintf("alpine:test-remove-%d", time.Now().UnixNano())
		image := backend.Image(randomTag)
		require.NotNil(t, image, "Image must not be nil")

		if !image.Exists(ctx) {
			baseImage := backend.Image("alpine:latest")
			if !baseImage.Exists(ctx) {
				err = baseImage.Pull(ctx)
				require.NoError(t, err, "Base image pull must succeed")
			}

			err = backend.client.ImageTag(ctx, "alpine:latest", randomTag)
			require.NoError(t, err, "Image tag must succeed")
		}

		require.True(t, image.Exists(ctx), "Image must exist before removal")

		err = image.Remove(ctx)
		require.NoError(t, err, "Image removal must succeed")
		assert.False(t, image.Exists(ctx), "Image must not exist after removal")
	})

	t.Run("NoClient", func(t *testing.T) {
		image := &dockerImage{
			backend: &DockerBackend{
				client: nil,
			},
			name: "alpine:latest",
		}

		err := image.Remove(context.Background())
		assert.Error(t, err, "Remove must fail when client is nil")
		assert.Contains(t, err.Error(), "not initialized")
	})
}

func TestImage_Digest_Integration(t *testing.T) {
	backend, err := New(core.DockerConfig{})
	require.NoError(t, err, "Backend creation must succeed - Docker is required")
	require.NotNil(t, backend, "Backend must not be nil")

	defer func() {
		require.NotNil(t, backend.client, "Client must exist for cleanup")
		require.NoError(t, backend.client.Close(), "Client close must succeed")
	}()

	ctx := context.Background()

	image := backend.Image("alpine:latest")
	require.NotNil(t, image, "Image must not be nil")
	if !image.Exists(ctx) {
		err = image.Pull(ctx)
		require.NoError(t, err, "Image pull must succeed")
	}

	t.Run("Success", func(t *testing.T) {
		digest, err := image.Digest(ctx)
		require.NoError(t, err, "Digest must succeed")
		require.NotEmpty(t, digest, "Digest must not be empty")
		assert.NotContains(t, digest, "sha256:", "Digest must not contain sha256: prefix")
	})

	t.Run("NoClient", func(t *testing.T) {
		image := &dockerImage{
			backend: &DockerBackend{
				client: nil,
			},
			name: "alpine:latest",
		}

		digest, err := image.Digest(context.Background())
		assert.Error(t, err, "Digest must fail when client is nil")
		assert.Empty(t, digest, "Digest must be empty on error")
		assert.Contains(t, err.Error(), "not initialized")
	})

	t.Run("InvalidImage", func(t *testing.T) {
		image := backend.Image("invalid-image-name-that-does-not-exist:999999")
		require.NotNil(t, image, "Image must not be nil")

		digest, err := image.Digest(context.Background())
		assert.Error(t, err, "Digest must fail for invalid image")
		assert.Empty(t, digest, "Digest must be empty on error")
	})
}

func TestImage_Tags_Integration(t *testing.T) {
	backend, err := New(core.DockerConfig{})
	require.NoError(t, err, "Backend creation must succeed - Docker is required")
	require.NotNil(t, backend, "Backend must not be nil")

	defer func() {
		require.NotNil(t, backend.client, "Client must exist for cleanup")
		require.NoError(t, backend.client.Close(), "Client close must succeed")
	}()

	ctx := context.Background()

	image := backend.Image("alpine:latest")
	require.NotNil(t, image, "Image must not be nil")

	if !image.Exists(ctx) {
		err = image.Pull(ctx)
		require.NoError(t, err, "Image pull must succeed")
	}

	t.Run("Success", func(t *testing.T) {
		tags, err := image.Tags(ctx)
		require.NoError(t, err, "Tags must succeed")
		require.NotNil(t, tags, "Tags must not be nil")
		assert.Contains(t, tags, "alpine:latest", "Tags must contain alpine:latest")
	})

	t.Run("NoClient", func(t *testing.T) {
		image := &dockerImage{
			backend: &DockerBackend{
				client: nil,
			},
			name: "alpine:latest",
		}

		tags, err := image.Tags(context.Background())
		assert.Error(t, err, "Tags must fail when client is nil")
		assert.Nil(t, tags, "Tags must be nil on error")
		assert.Contains(t, err.Error(), "not initialized")
	})

	t.Run("InvalidImage", func(t *testing.T) {
		image := backend.Image("invalid-image-name-that-does-not-exist:999999")
		require.NotNil(t, image, "Image must not be nil")

		tags, err := image.Tags(context.Background())
		assert.Error(t, err, "Tags must fail for invalid image")
		assert.Nil(t, tags, "Tags must be nil on error")
	})
}

func TestDockerBuildInputType_Integration(t *testing.T) {
	input := &DockerBuildInput{}
	assert.Equal(t, core.BackendTypeDocker, input.Type())
}

type mockBuildInput struct {
	backendType core.BackendType
}

func (m *mockBuildInput) Type() core.BackendType {
	return m.backendType
}
