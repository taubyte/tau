package containers

import (
	"fmt"

	"github.com/docker/docker/client"
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
func New(options ...Option) (dockerClient *Client, err error) {
	dockerClient = &Client{
		progressOutput: false,
	}

	for _, opt := range options {
		if err := opt(dockerClient); err != nil {
			return nil, err
		}
	}
	dockerClient.Client, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("new docker client failed with: %w", err)
	}

	return
}
