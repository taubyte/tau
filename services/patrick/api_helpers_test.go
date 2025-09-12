package service

import (
	"context"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	libp2p "github.com/libp2p/go-libp2p/core/peer"
	commonIface "github.com/taubyte/tau/core/services/patrick"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	"gotest.tools/v3/assert"
)

func TestLockHelper(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(*PatrickService)
		pid           libp2p.ID
		lockData      []byte
		jid           string
		eta           int64
		method        bool
		expectError   bool
		errorContains string
		expectedResp  cr.Response
	}{
		{
			name: "invalid lock data - CBOR unmarshal error",
			setupMock: func(s *PatrickService) {
			},
			pid:           libp2p.ID("test-peer"),
			lockData:      []byte("invalid-cbor-data"),
			jid:           "test-job",
			eta:           30,
			method:        true,
			expectError:   true,
			errorContains: "cbor:",
		},
		{
			name: "lock expired - method true - same peer",
			setupMock: func(s *PatrickService) {
			},
			pid:           libp2p.ID("test-peer"),
			lockData:      []byte("simple-lock-data"),
			jid:           "test-job",
			eta:           30,
			method:        true,
			expectError:   true,
			errorContains: "unexpected EOF",
		},
		{
			name: "lock expired - method false",
			setupMock: func(s *PatrickService) {
			},
			pid:           libp2p.ID("test-peer"),
			lockData:      []byte("simple-lock-data"),
			jid:           "test-job",
			eta:           30,
			method:        false,
			expectError:   true,
			errorContains: "unexpected EOF",
		},
		{
			name: "lock active - method true - different peer",
			setupMock: func(s *PatrickService) {
			},
			pid:           libp2p.ID("different-peer"),
			lockData:      []byte("simple-lock-data"),
			jid:           "test-job",
			eta:           30,
			method:        true,
			expectError:   true,
			errorContains: "unexpected EOF",
		},
		{
			name: "lock active - method false",
			setupMock: func(s *PatrickService) {
			},
			pid:           libp2p.ID("test-peer"),
			lockData:      []byte("simple-lock-data"),
			jid:           "test-job",
			eta:           30,
			method:        false,
			expectError:   true,
			errorContains: "unexpected EOF",
		},
		{
			name: "lock expired - method false - success path",
			setupMock: func(s *PatrickService) {
			},
			pid:         libp2p.ID("test-peer"),
			lockData:    createTestLockData(time.Now().Unix()-100, 30),
			jid:         "test-job",
			eta:         30,
			method:      false,
			expectError: false,
			expectedResp: cr.Response{
				"locked": false,
			},
		},
		{
			name: "lock active - method false - success path",
			setupMock: func(s *PatrickService) {
			},
			pid:         libp2p.ID("test-peer"),
			lockData:    createTestLockData(time.Now().Unix(), 30),
			jid:         "test-job",
			eta:         30,
			method:      false,
			expectError: false,
			expectedResp: cr.Response{
				"locked":    true,
				"locked-by": "2Uw1bppLugs5B",
			},
		},
		{
			name: "lock active - method true - different peer - success path",
			setupMock: func(s *PatrickService) {
			},
			pid:           libp2p.ID("different-peer"),
			lockData:      createTestLockData(time.Now().Unix(), 30),
			jid:           "test-job",
			eta:           30,
			method:        true,
			expectError:   true,
			errorContains: "job is locked by",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := createTestService()

			if tt.setupMock != nil {
				tt.setupMock(service)
			}

			result, err := service.lockHelper(context.Background(), tt.pid, tt.lockData, tt.jid, tt.eta, tt.method)

			if tt.expectError {
				assert.Assert(t, err != nil, "Expected error but got nil")
				if tt.errorContains != "" {
					assert.ErrorContains(t, err, tt.errorContains)
				}
				if tt.expectedResp != nil {
					assert.DeepEqual(t, tt.expectedResp, result)
				}
			} else {
				assert.NilError(t, err)
				if tt.expectedResp != nil {
					assert.DeepEqual(t, tt.expectedResp, result)
				} else {
					assert.Assert(t, result != nil)
				}
			}
		})
	}
}

func TestCancelJob(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(*PatrickService, *mockHTTPContext)
		expectError   bool
		errorContains string
		expectedResp  interface{}
	}{
		{
			name: "missing jid variable",
			setupMock: func(s *PatrickService, ctx *mockHTTPContext) {
			},
			expectError: true,
		},
		{
			name: "job already finished in archive",
			setupMock: func(s *PatrickService, ctx *mockHTTPContext) {
				ctx.SetVariable("jid", "test-job")
				s.db.Put(context.Background(), "/archive/jobs/test-job", []byte("archived-job-data"))
			},
			expectError:   true,
			errorContains: "job test-job already finished, cannot cancel",
		},
		{
			name: "job not registered",
			setupMock: func(s *PatrickService, ctx *mockHTTPContext) {
				ctx.SetVariable("jid", "test-job")
				// No job in /jobs/ and no lock
			},
			expectError:   true,
			errorContains: "job test-job is not registered",
		},
		{
			name: "successful cancel with valid lock",
			setupMock: func(s *PatrickService, ctx *mockHTTPContext) {
				ctx.SetVariable("jid", "test-job")
				job := createTestJob("test-job")
				jobBytes, _ := cbor.Marshal(job)
				s.db.Put(context.Background(), "/jobs/test-job", jobBytes)
				lockData := createTestLockData(time.Now().Unix(), 30)
				s.db.Put(context.Background(), "/locked/jobs/test-job", lockData)
			},
			expectError: false,
			expectedResp: map[string]interface{}{
				"cancelled": "test-job",
			},
		},
		{
			name: "invalid lock data - CBOR unmarshal error",
			setupMock: func(s *PatrickService, ctx *mockHTTPContext) {
				ctx.SetVariable("jid", "test-job")
				s.db.Put(context.Background(), "/jobs/test-job", []byte("job-data"))
				s.db.Put(context.Background(), "/locked/jobs/test-job", []byte("invalid-lock-data"))
			},
			expectError:   true,
			errorContains: "failed unmarshal job test-job",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := createTestService()
			ctx := newMockHTTPContext()

			if tt.setupMock != nil {
				tt.setupMock(service, ctx)
			}

			result, err := service.cancelJob(ctx)

			if tt.expectError {
				assert.Assert(t, err != nil, "Expected error but got nil")
				if tt.errorContains != "" {
					assert.ErrorContains(t, err, tt.errorContains)
				}
			} else {
				assert.NilError(t, err)
				if tt.expectedResp != nil {
					assert.DeepEqual(t, tt.expectedResp, result)
				}
			}
		})
	}
}

func TestRetryJob(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(*PatrickService, *mockHTTPContext)
		expectError   bool
		errorContains string
		expectedResp  interface{}
	}{
		{
			name: "missing jid variable",
			setupMock: func(s *PatrickService, ctx *mockHTTPContext) {
			},
			expectError:   true,
			errorContains: "failed finding map jid",
		},
		{
			name: "job not found in archive",
			setupMock: func(s *PatrickService, ctx *mockHTTPContext) {
				ctx.SetVariable("jid", "test-job")
			},
			expectError:   true,
			errorContains: "failed grabbing archived job test-job",
		},
		{
			name: "successful retry - failed job",
			setupMock: func(s *PatrickService, ctx *mockHTTPContext) {
				ctx.SetVariable("jid", "test-job")
				// Add failed job to archive
				job := createTestJob("test-job")
				job.Status = commonIface.JobStatusFailed
				jobBytes, _ := cbor.Marshal(job)
				s.db.Put(context.Background(), "/archive/jobs/test-job", jobBytes)
			},
			expectError: false,
			expectedResp: map[string]interface{}{
				"retry": "test-job",
			},
		},
		{
			name: "successful retry - cancelled job",
			setupMock: func(s *PatrickService, ctx *mockHTTPContext) {
				ctx.SetVariable("jid", "test-job")
				// Add cancelled job to archive
				job := createTestJob("test-job")
				job.Status = commonIface.JobStatusCancelled
				jobBytes, _ := cbor.Marshal(job)
				s.db.Put(context.Background(), "/archive/jobs/test-job", jobBytes)
			},
			expectError: false,
			expectedResp: map[string]interface{}{
				"retry": "test-job",
			},
		},
		{
			name: "successful retry - success job",
			setupMock: func(s *PatrickService, ctx *mockHTTPContext) {
				ctx.SetVariable("jid", "test-job")
				// Add success job to archive
				job := createTestJob("test-job")
				job.Status = commonIface.JobStatusSuccess
				jobBytes, _ := cbor.Marshal(job)
				s.db.Put(context.Background(), "/archive/jobs/test-job", jobBytes)
			},
			expectError: false,
			expectedResp: map[string]interface{}{
				"retry": "test-job",
			},
		},
		{
			name: "job not retryable - open status",
			setupMock: func(s *PatrickService, ctx *mockHTTPContext) {
				ctx.SetVariable("jid", "test-job")
				// Add open job to archive (not retryable)
				job := createTestJob("test-job")
				job.Status = commonIface.JobStatusOpen
				jobBytes, _ := cbor.Marshal(job)
				s.db.Put(context.Background(), "/archive/jobs/test-job", jobBytes)
			},
			expectError:  false,
			expectedResp: nil,
		},
		{
			name: "job not retryable - locked status",
			setupMock: func(s *PatrickService, ctx *mockHTTPContext) {
				ctx.SetVariable("jid", "test-job")
				// Add locked job to archive (not retryable)
				job := createTestJob("test-job")
				job.Status = commonIface.JobStatusLocked
				jobBytes, _ := cbor.Marshal(job)
				s.db.Put(context.Background(), "/archive/jobs/test-job", jobBytes)
			},
			expectError:  false,
			expectedResp: nil,
		},
		{
			name: "invalid job data - CBOR unmarshal error",
			setupMock: func(s *PatrickService, ctx *mockHTTPContext) {
				ctx.SetVariable("jid", "test-job")
				s.db.Put(context.Background(), "/archive/jobs/test-job", []byte("invalid-job-data"))
			},
			expectError:   true,
			errorContains: "failed grabbing archived job test-job",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := createTestService()
			ctx := newMockHTTPContext()

			if tt.setupMock != nil {
				tt.setupMock(service, ctx)
			}

			result, err := service.retryJob(ctx)

			if tt.expectError {
				assert.Assert(t, err != nil, "Expected error but got nil")
				if tt.errorContains != "" {
					assert.ErrorContains(t, err, tt.errorContains)
				}
			} else {
				assert.NilError(t, err)
				if tt.expectedResp != nil {
					assert.DeepEqual(t, tt.expectedResp, result)
				} else {
					assert.Assert(t, result == nil)
				}
			}
		})
	}
}

func TestDeleteJob(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(*PatrickService)
		loc           []string
		jid           string
		expectError   bool
		errorContains string
	}{
		{
			name: "successful delete - single location",
			setupMock: func(s *PatrickService) {
				s.db.Put(context.Background(), "/jobs/test-job", []byte("job-data"))
			},
			loc:         []string{"/jobs/"},
			jid:         "test-job",
			expectError: false,
		},
		{
			name: "successful delete - multiple locations",
			setupMock: func(s *PatrickService) {
				s.db.Put(context.Background(), "/jobs/test-job", []byte("job-data"))
				s.db.Put(context.Background(), "/archive/jobs/test-job", []byte("archived-job-data"))
			},
			loc:         []string{"/jobs/", "/archive/jobs/"},
			jid:         "test-job",
			expectError: false,
		},
		{
			name: "delete error - database error",
			setupMock: func(s *PatrickService) {
				s.db.Close()
			},
			loc:           []string{"/jobs/"},
			jid:           "test-job",
			expectError:   true,
			errorContains: "failed deleting test-job at /jobs/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := createTestService()

			if tt.setupMock != nil {
				tt.setupMock(service)
			}

			err := service.deleteJob(context.Background(), tt.jid, tt.loc...)

			if tt.expectError {
				assert.Assert(t, err != nil, "Expected error but got nil")
				if tt.errorContains != "" {
					assert.ErrorContains(t, err, tt.errorContains)
				}
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

func TestGetJob(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(*PatrickService)
		loc           string
		jid           string
		expectError   bool
		errorContains string
	}{
		{
			name: "successful get job",
			setupMock: func(s *PatrickService) {
				job := createTestJob("test-job")
				jobBytes, _ := cbor.Marshal(job)
				s.db.Put(context.Background(), "/jobs/test-job", jobBytes)
			},
			loc:         "/jobs/",
			jid:         "test-job",
			expectError: false,
		},
		{
			name: "job not found",
			setupMock: func(s *PatrickService) {
			},
			loc:           "/jobs/",
			jid:           "nonexistent-job",
			expectError:   true,
			errorContains: "get job nonexistent-job failed with",
		},
		{
			name: "invalid job data - CBOR unmarshal error",
			setupMock: func(s *PatrickService) {
				s.db.Put(context.Background(), "/jobs/test-job", []byte("invalid-job-data"))
			},
			loc:           "/jobs/",
			jid:           "test-job",
			expectError:   true,
			errorContains: "unmarshal job test-job failed with",
		},
		{
			name: "database error",
			setupMock: func(s *PatrickService) {
				s.db.Close()
			},
			loc:           "/jobs/",
			jid:           "test-job",
			expectError:   true,
			errorContains: "get job test-job failed with",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := createTestService()

			if tt.setupMock != nil {
				tt.setupMock(service)
			}

			job, err := service.getJob(context.Background(), tt.loc, tt.jid)

			if tt.expectError {
				assert.Assert(t, err != nil, "Expected error but got nil")
				if tt.errorContains != "" {
					assert.ErrorContains(t, err, tt.errorContains)
				}
				assert.Assert(t, job == nil, "Expected job to be nil on error")
			} else {
				assert.NilError(t, err)
				assert.Assert(t, job != nil, "Expected job to be non-nil on success")
				assert.Equal(t, tt.jid, job.Id)
			}
		})
	}
}

func createTestLockData(timestamp, eta int64) []byte {
	lock := struct {
		Pid       string `cbor:"4,keyasint"`
		Timestamp int64  `cbor:"8,keyasint"`
		Eta       int64  `cbor:"16,keyasint"`
	}{
		Pid:       "test-peer",
		Timestamp: timestamp,
		Eta:       eta,
	}
	data, err := cbor.Marshal(lock)
	if err != nil {
		return []byte("simple-lock-data")
	}
	return data
}
