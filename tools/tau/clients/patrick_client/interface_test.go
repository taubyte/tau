package patrickClient

import (
	"io"
	"testing"

	patrickIface "github.com/taubyte/tau/core/services/patrick"
	"gotest.tools/v3/assert"
)

// mockClient implements Client for tests.
type mockClient struct {
	jobsFunc    func(projectId string) ([]string, error)
	jobFunc     func(jid string) (*patrickIface.Job, error)
	logFileFunc func(jobId, resourceId string) (io.ReadCloser, error)
	cancelFunc  func(jid string) (any, error)
	retryFunc   func(jid string) (any, error)
}

func (m *mockClient) Jobs(projectId string) ([]string, error) {
	if m.jobsFunc != nil {
		return m.jobsFunc(projectId)
	}
	return nil, nil
}

func (m *mockClient) Job(jid string) (*patrickIface.Job, error) {
	if m.jobFunc != nil {
		return m.jobFunc(jid)
	}
	return nil, nil
}

func (m *mockClient) LogFile(jobId, resourceId string) (io.ReadCloser, error) {
	if m.logFileFunc != nil {
		return m.logFileFunc(jobId, resourceId)
	}
	return nil, nil
}

func (m *mockClient) Cancel(jid string) (interface{}, error) {
	if m.cancelFunc != nil {
		return m.cancelFunc(jid)
	}
	return nil, nil
}

func (m *mockClient) Retry(jid string) (any, error) {
	if m.retryFunc != nil {
		return m.retryFunc(jid)
	}
	return nil, nil
}

// Ensure mockClient implements Client at compile time.
var _ Client = (*mockClient)(nil)

func TestClientInterface_Mock(t *testing.T) {
	var c Client = &mockClient{
		jobsFunc: func(projectId string) ([]string, error) {
			return []string{"job1", "job2"}, nil
		},
	}
	ids, err := c.Jobs("proj")
	assert.NilError(t, err)
	assert.DeepEqual(t, ids, []string{"job1", "job2"})
}
