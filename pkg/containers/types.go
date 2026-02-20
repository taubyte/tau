package containers

import (
	"io"

	"github.com/taubyte/tau/pkg/containers/core"
)

var (
	ForceRebuild = false
)

// MuxedReadCloser wraps the Read/Close methods for muxed logs.
type MuxedReadCloser struct {
	reader io.ReadCloser
}

// Client wraps the methods of the docker Client.
// It now uses a Backend internally for container operations.
type Client struct {
	backend        core.Backend // Lazily initialized when needed
	progressOutput bool
}

// volume defines the source and target to be volumed in the docker container.
type volume struct {
	source string
	target string
}

// Container wraps the methods of the docker container.
// It now uses a Backend internally for container operations.
type Container struct {
	backend core.Backend
	id      core.ContainerID
	// Keep old fields for backward compatibility with options
	image   *DockerImage // Kept for reference, but operations use backend
	cmd     []string
	shell   []string
	volumes []volume
	env     []string
	workDir string
}

// DockerImage wraps the methods of the docker image.
// It now uses a Backend internally for image operations.
type DockerImage struct {
	backend      core.Backend
	image        string
	buildTarball io.Reader
	output       io.Writer
	// Keep client reference for backward compatibility
	client *Client // Kept for reference, but operations use backend
}

// Close is a no-op for backward compatibility.
// The backend handles its own lifecycle and doesn't need explicit closing.
func (c *Client) Close() error {
	return nil
}

type PullStatus struct {
	Status         string `json:"status"`
	ProgressDetail struct {
		Current int `json:"current"`
		Total   int `json:"total"`
	} `json:"progressDetail"`
	Id          string `json:"id"`
	Error       string `json:"error"`
	ErrorDetail struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
}

type BuildStatus struct {
	Stream      string `json:"stream"`
	Error       string `json:"error"`
	ErrorDetail struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
}
