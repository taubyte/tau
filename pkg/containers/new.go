package containers

import (
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/containers/core"
)

type Option func(*Client) error

// WithBackend uses a backend the caller already built instead of detecting one: an
// operator names a backend and socket in the node's configuration, and detection
// could pick the other daemon.
func WithBackend(b core.Backend) Option {
	return func(c *Client) error {
		if b == nil {
			return errors.New("containers: a nil backend")
		}
		c.backend = b
		return nil
	}
}

// Verbose shows progress output.
func Verbose() Option {
	return func(c *Client) error {
		c.progressOutput = true
		return nil
	}
}

// New creates a client, initialising the backend immediately (docker first, then
// containerd) unless WithBackend gave one.
func New(options ...Option) (dockerClient *Client, err error) {
	dockerClient = &Client{
		progressOutput: false,
	}

	for _, opt := range options {
		if err := opt(dockerClient); err != nil {
			return nil, err
		}
	}

	// A backend the caller chose is not re-chosen (see WithBackend).
	if dockerClient.backend != nil {
		return dockerClient, nil
	}

	backend, err := dockerClient.defaultBackend()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize backend: %w", err)
	}
	dockerClient.backend = backend

	return
}
