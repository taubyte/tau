package service

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	commonIface "github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/p2p/peer"
	"gotest.tools/v3/assert"
)

type mockReadSeekCloser struct {
	io.Reader
}

func (m *mockReadSeekCloser) Seek(offset int64, whence int) (int64, error) {
	return 0, nil
}

func (m *mockReadSeekCloser) Close() error {
	return nil
}

func (m *mockReadSeekCloser) WriteTo(w io.Writer) (int64, error) {
	return io.Copy(w, m.Reader)
}

type mockNodeWithGetFile struct {
	*mockNode
	getFileError error
}

func (m *mockNodeWithGetFile) GetFile(ctx context.Context, id string) (peer.ReadSeekCloser, error) {
	if m.getFileError != nil {
		return nil, m.getFileError
	}
	return &mockReadSeekCloser{Reader: strings.NewReader("test file content")}, nil
}

func TestProjectAllJobHandler_SimpleCases(t *testing.T) {
	tests := []struct {
		name           string
		setupMock      func(*PatrickService)
		variables      map[string]interface{}
		expectError    bool
		expectedResult interface{}
	}{
		{
			name: "successful get all jobs for project",
			setupMock: func(s *PatrickService) {
				s.db.Put(context.Background(), "/by/project/test-project/job1", []byte("job1-data"))
				s.db.Put(context.Background(), "/by/project/test-project/job2", []byte("job2-data"))
			},
			variables: map[string]interface{}{
				"projectId": "test-project",
			},
			expectError: false,
			expectedResult: project{
				ProjectId: "test-project",
				JobIds:    []string{"job1", "job2"},
			},
		},
		{
			name: "no jobs found for project",
			setupMock: func(s *PatrickService) {
			},
			variables: map[string]interface{}{
				"projectId": "empty-project",
			},
			expectError: false,
			expectedResult: project{
				ProjectId: "empty-project",
				JobIds:    []string{},
			},
		},
		{
			name: "missing projectId variable",
			setupMock: func(s *PatrickService) {
			},
			variables:   map[string]interface{}{},
			expectError: true,
		},
		{
			name: "database error",
			setupMock: func(s *PatrickService) {
				s.db.Close()
			},
			variables: map[string]interface{}{
				"projectId": "test-project",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := createTestService()
			if tt.setupMock != nil {
				tt.setupMock(service)
			}

			ctx := newMockHTTPContext()
			ctx.SetVariables(tt.variables)

			result, err := service.projectAllJobHandler(ctx)

			if tt.expectError {
				assert.Assert(t, err != nil, "Expected error but got nil")
			} else {
				assert.NilError(t, err)
				if tt.expectedResult != nil {
					if tt.name == "successful get all jobs for project" {
						expected := tt.expectedResult.(project)
						actual := result.(project)
						assert.Equal(t, expected.ProjectId, actual.ProjectId)
						expectedSorted := make([]string, len(expected.JobIds))
						actualSorted := make([]string, len(actual.JobIds))
						copy(expectedSorted, expected.JobIds)
						copy(actualSorted, actual.JobIds)
						slices.Sort(expectedSorted)
						slices.Sort(actualSorted)
						assert.DeepEqual(t, expectedSorted, actualSorted)
					} else {
						assert.DeepEqual(t, tt.expectedResult, result)
					}
				}
			}
		})
	}
}

func TestProjectJobHandler_SimpleCases(t *testing.T) {
	tests := []struct {
		name           string
		setupMock      func(*PatrickService)
		variables      map[string]interface{}
		expectError    bool
		expectedResult interface{}
	}{
		{
			name: "successful get job from jobs",
			setupMock: func(s *PatrickService) {
				job := createTestJobWithStatus("test-job", commonIface.JobStatusOpen)
				s.db.Put(context.Background(), "/jobs/test-job", marshalJob(job))
			},
			variables: map[string]interface{}{
				"jid": "test-job",
			},
			expectError: false,
		},
		{
			name: "successful get job from archive",
			setupMock: func(s *PatrickService) {
				job := createTestJobWithStatus("test-job", commonIface.JobStatusSuccess)
				s.db.Put(context.Background(), "/archive/jobs/test-job", marshalJob(job))
			},
			variables: map[string]interface{}{
				"jid": "test-job",
			},
			expectError: false,
		},
		{
			name: "missing jid variable",
			setupMock: func(s *PatrickService) {
			},
			variables:   map[string]interface{}{},
			expectError: true,
		},
		{
			name: "job not found",
			setupMock: func(s *PatrickService) {
			},
			variables: map[string]interface{}{
				"jid": "non-existent-job",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := createTestService()
			if tt.setupMock != nil {
				tt.setupMock(service)
			}

			ctx := newMockHTTPContext()
			ctx.SetVariables(tt.variables)

			result, err := service.projectJobHandler(ctx)

			if tt.expectError {
				assert.Assert(t, err != nil, "Expected error but got nil")
			} else {
				assert.NilError(t, err)
				if tt.expectedResult != nil {
					assert.DeepEqual(t, tt.expectedResult, result)
				}
			}
		})
	}
}

func TestCidHandler_SimpleCases(t *testing.T) {
	tests := []struct {
		name        string
		setupMock   func(*PatrickService)
		variables   map[string]interface{}
		expectError bool
	}{
		{
			name: "successful get file",
			setupMock: func(s *PatrickService) {
				s.node = &mockNodeWithGetFile{mockNode: &mockNode{}}
			},
			variables: map[string]interface{}{
				"cid": "test-cid",
			},
			expectError: false,
		},
		{
			name: "missing cid variable",
			setupMock: func(s *PatrickService) {
			},
			variables:   map[string]interface{}{},
			expectError: true,
		},
		{
			name: "node get file error",
			setupMock: func(s *PatrickService) {
				s.node = &mockNodeWithGetFile{
					mockNode:     &mockNode{},
					getFileError: errors.New("mock error"),
				}
			},
			variables: map[string]interface{}{
				"cid": "invalid-cid",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := createTestService()
			if tt.setupMock != nil {
				tt.setupMock(service)
			}

			ctx := newMockHTTPContext()
			ctx.SetVariables(tt.variables)

			result, err := service.cidHandler(ctx)

			if tt.expectError {
				assert.Assert(t, err != nil, "Expected error but got nil")
			} else {
				assert.NilError(t, err)
				assert.Assert(t, result != nil)
			}
		})
	}
}

func TestCancelJob_SimpleCases(t *testing.T) {
	tests := []struct {
		name        string
		setupMock   func(*PatrickService)
		variables   map[string]interface{}
		expectError bool
	}{
		{
			name: "cancel job with invalid lock data",
			setupMock: func(s *PatrickService) {
				job := createTestJobWithStatus("test-job", commonIface.JobStatusOpen)
				s.db.Put(context.Background(), "/jobs/test-job", marshalJob(job))
				s.db.Put(context.Background(), "/locked/jobs/test-job", []byte("simple-lock-data"))
			},
			variables: map[string]interface{}{
				"jid": "test-job",
			},
			expectError: true,
		},
		{
			name: "missing jid variable",
			setupMock: func(s *PatrickService) {
			},
			variables:   map[string]interface{}{},
			expectError: true,
		},
		{
			name: "job already finished",
			setupMock: func(s *PatrickService) {
				job := createTestJobWithStatus("test-job", commonIface.JobStatusSuccess)
				s.db.Put(context.Background(), "/archive/jobs/test-job", marshalJob(job))
			},
			variables: map[string]interface{}{
				"jid": "test-job",
			},
			expectError: true,
		},
		{
			name: "job not registered",
			setupMock: func(s *PatrickService) {
			},
			variables: map[string]interface{}{
				"jid": "non-existent-job",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := createTestService()
			if tt.setupMock != nil {
				tt.setupMock(service)
			}

			ctx := newMockHTTPContext()
			ctx.SetVariables(tt.variables)

			result, err := service.cancelJob(ctx)

			if tt.expectError {
				assert.Assert(t, err != nil, "Expected error but got nil")
			} else {
				assert.NilError(t, err)
				assert.Assert(t, result != nil)
			}
		})
	}
}

func TestRetryJob_SimpleCases(t *testing.T) {
	tests := []struct {
		name        string
		setupMock   func(*PatrickService)
		variables   map[string]interface{}
		expectError bool
	}{
		{
			name: "successful retry job",
			setupMock: func(s *PatrickService) {
				job := createTestJobWithStatus("test-job", commonIface.JobStatusFailed)
				s.db.Put(context.Background(), "/archive/jobs/test-job", marshalJob(job))
			},
			variables: map[string]interface{}{
				"jid": "test-job",
			},
			expectError: false,
		},
		{
			name: "missing jid variable",
			setupMock: func(s *PatrickService) {
			},
			variables:   map[string]interface{}{},
			expectError: true,
		},
		{
			name: "job not found in archive",
			setupMock: func(s *PatrickService) {
			},
			variables: map[string]interface{}{
				"jid": "non-existent-job",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := createTestService()
			if tt.setupMock != nil {
				tt.setupMock(service)
			}

			ctx := newMockHTTPContext()
			ctx.SetVariables(tt.variables)

			result, err := service.retryJob(ctx)

			if tt.expectError {
				assert.Assert(t, err != nil, "Expected error but got nil")
			} else {
				assert.NilError(t, err)
				assert.Assert(t, result != nil)
			}
		})
	}
}

func TestDownloadAsset_SimpleCases(t *testing.T) {
	tests := []struct {
		name        string
		setupMock   func(*PatrickService)
		variables   map[string]interface{}
		expectError bool
	}{
		{
			name: "successful download asset",
			setupMock: func(s *PatrickService) {
				job := createTestJobWithStatus("test-job", commonIface.JobStatusSuccess)
				job.AssetCid = map[string]string{"resource1": "asset-cid-1"}
				s.db.Put(context.Background(), "/archive/jobs/test-job", marshalJob(job))
				s.node = &mockNodeWithGetFile{mockNode: &mockNode{}}
			},
			variables: map[string]interface{}{
				"jobId":      "test-job",
				"resourceId": "resource1",
			},
			expectError: false,
		},
		{
			name: "missing jobId variable",
			setupMock: func(s *PatrickService) {
			},
			variables: map[string]interface{}{
				"resourceId": "resource1",
			},
			expectError: true,
		},
		{
			name: "missing resourceId variable",
			setupMock: func(s *PatrickService) {
			},
			variables: map[string]interface{}{
				"jobId": "test-job",
			},
			expectError: true,
		},
		{
			name: "job not found",
			setupMock: func(s *PatrickService) {
			},
			variables: map[string]interface{}{
				"jobId":      "non-existent-job",
				"resourceId": "resource1",
			},
			expectError: true,
		},
		{
			name: "resource not found in job",
			setupMock: func(s *PatrickService) {
				job := createTestJobWithStatus("test-job", commonIface.JobStatusSuccess)
				job.AssetCid = map[string]string{"other-resource": "asset-cid-1"}
				s.db.Put(context.Background(), "/archive/jobs/test-job", marshalJob(job))
			},
			variables: map[string]interface{}{
				"jobId":      "test-job",
				"resourceId": "resource1",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := createTestService()
			if tt.setupMock != nil {
				tt.setupMock(service)
			}

			ctx := newMockHTTPContext()
			ctx.SetVariables(tt.variables)

			result, err := service.downloadAsset(ctx)

			if tt.expectError {
				assert.Assert(t, err != nil, "Expected error but got nil")
			} else {
				assert.NilError(t, err)
				assert.Assert(t, result != nil)
			}
		})
	}
}
