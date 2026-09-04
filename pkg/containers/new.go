package containers

import (
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/containers/core"

	_ "github.com/taubyte/tau/pkg/containers/backends/containerd" // Register containerd backend (if available)
	_ "github.com/taubyte/tau/pkg/containers/backends/docker"     // Register docker backend
)

type Option func(*Client) error

// WithBackend uses a backend the caller already built, instead of detecting
// one.
//
// AN OPERATOR NAMES A BACKEND AND A SOCKET in the node's own configuration, and
// a client that auto-detected beside it could pick the other one — so a build
// would run on a daemon nobody configured while the workloads ran on the one
// they did.
func WithBackend(b core.Backend) Option {
	return func(c *Client) error {
		if b == nil {
			return errors.New("containers: a nil backend")
		}
		c.backend = b
		return nil
	}
}

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

	// A BACKEND THE CALLER ALREADY CHOSE IS NOT RE-CHOSEN. An operator names a
	// backend and a socket in the node's own configuration; a client that
	// auto-detected beside it could pick the other one, so a build would run on
	// a daemon nobody configured while the workloads ran on the one they did.
	if dockerClient.backend != nil {
		return dockerClient, nil
	}

	backend, err := getDefaultBackend()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize backend: %w", err)
	}
	dockerClient.backend = backend

	return
}
