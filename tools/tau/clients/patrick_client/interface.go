package patrickClient

import (
	"io"

	patrickIface "github.com/taubyte/tau/core/services/patrick"
)

// Client is the interface for the Patrick API used by the tau CLI (jobs, logs, cancel, retry).
// Implementations can be the real HTTP client or a mock for tests.
type Client interface {
	Jobs(projectId string) ([]string, error)
	Job(jid string) (*patrickIface.Job, error)
	LogFile(jobId, resourceId string) (io.ReadCloser, error)
	Cancel(jid string) (any, error)
	Retry(jid string) (any, error)
}
