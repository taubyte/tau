package service

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	commonIface "github.com/taubyte/tau/core/services/patrick"
	"gotest.tools/v3/assert"
)

func generateTestPeerID(t *testing.T) peer.ID {
	t.Helper()
	priv, _, err := crypto.GenerateEd25519Key(rand.Reader)
	assert.NilError(t, err)
	id, err := peer.IDFromPrivateKey(priv)
	assert.NilError(t, err)
	return id
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
			name: "successful cancel with valid assignment",
			setupMock: func(s *PatrickService, ctx *mockHTTPContext) {
				ctx.SetVariable("jid", "test-job")
				job := createTestJob("test-job")
				jobBytes, _ := cbor.Marshal(job)
				s.db.Put(context.Background(), "/jobs/test-job", jobBytes)
				priv, _, _ := crypto.GenerateEd25519Key(rand.Reader)
				pid, _ := peer.IDFromPrivateKey(priv)
				assignment := Assignment{
					MonkeyPID: pid.String(),
					Timestamp: time.Now().Unix(),
				}
				assignBytes, _ := cbor.Marshal(assignment)
				s.db.Put(context.Background(), "/assigned/test-job", assignBytes)
			},
			expectError: false,
			expectedResp: map[string]interface{}{
				"cancelled": "test-job",
			},
		},
		{
			name: "invalid assignment data - CBOR unmarshal error",
			setupMock: func(s *PatrickService, ctx *mockHTTPContext) {
				ctx.SetVariable("jid", "test-job")
				s.db.Put(context.Background(), "/jobs/test-job", []byte("job-data"))
				s.db.Put(context.Background(), "/assigned/test-job", []byte("invalid-assignment-data"))
			},
			expectError:   true,
			errorContains: "failed unmarshal assignment for job test-job",
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
			expectError:   true,
			errorContains: "job is not in a state to be retried",
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
			expectError:   true,
			errorContains: "job is not in a state to be retried",
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

// #21: Test retryJob pushes the re-opened job onto the queue.
func TestRetryJob_QueuePush(t *testing.T) {
	t.Run("successful retry pushes to queue", func(t *testing.T) {
		service := createTestService()
		mq := &mockJobQueue{}
		service.jobQueue = mq

		ctx := newMockHTTPContext()
		ctx.SetVariable("jid", "retry-q-job")

		job := createTestJob("retry-q-job")
		job.Status = commonIface.JobStatusFailed
		jobBytes, _ := cbor.Marshal(job)
		service.db.Put(context.Background(), "/archive/jobs/retry-q-job", jobBytes)

		result, err := service.retryJob(ctx)
		assert.NilError(t, err)
		assert.DeepEqual(t, map[string]interface{}{"retry": "retry-q-job"}, result)

		assert.Equal(t, 1, len(mq.pushCalls))
		assert.Equal(t, "retry-q-job", mq.pushCalls[0].id)
	})

	t.Run("queue push error propagates", func(t *testing.T) {
		service := createTestService()
		mq := &mockJobQueue{pushErr: errors.New("raft not leader")}
		service.jobQueue = mq

		ctx := newMockHTTPContext()
		ctx.SetVariable("jid", "retry-fail-job")

		job := createTestJob("retry-fail-job")
		job.Status = commonIface.JobStatusFailed
		jobBytes, _ := cbor.Marshal(job)
		service.db.Put(context.Background(), "/archive/jobs/retry-fail-job", jobBytes)

		_, err := service.retryJob(ctx)
		assert.Assert(t, err != nil)
		assert.ErrorContains(t, err, "failed to push retried job")
	})
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
