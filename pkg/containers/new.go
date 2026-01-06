package containers

import (
	"fmt"

	_ "github.com/taubyte/tau/pkg/containers/backends/containerd" // Register containerd backend (if available)
	_ "github.com/taubyte/tau/pkg/containers/backends/docker"     // Register docker backend
)

type Option func(*Client) error

// do not show progress output
func Verbose() Option {
	return func(c *Client) error {
		c.progressOutput = true
		return nil
	}
}

// New creates a new dockerClient with default Options.
// Backend is initialized immediately (Docker first, fallback to containerd).
func New(options ...Option) (dockerClient *Client, err error) {
	dockerClient = &Client{
		progressOutput: false,
	}

	for _, opt := range options {
		if err := opt(dockerClient); err != nil {
			return nil, err
		}
	}

	backend, err := getDefaultBackend()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize backend: %w", err)
	}
	dockerClient.backend = backend

	return
}
