package service

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/core/services/monkey"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/tau/p2p/streams/command/response"
	"github.com/taubyte/tau/pkg/kvdb/mock"
	"gotest.tools/v3/assert"
)

type testCase struct {
	name          string
	setupMock     func(*PatrickService)
	body          command.Body
	expectError   bool
	errorContains string
	expectedResp  map[string]interface{}
	// Additional fields for specific test types
	pid            peer.ID
	jid            string
	status         patrick.JobStatus
	expectedLocked bool
}

type mockConnection struct {
	streams.Connection
	remotePeer peer.ID
}

func (m *mockConnection) RemotePeer() peer.ID {
	return m.remotePeer
}

type mockMonkeyClient struct {
	cancelError error
}

func (m *mockMonkeyClient) Peers(pid ...peer.ID) monkey.Client {
	return &mockMonkeyClient{cancelError: m.cancelError}
}

func (m *mockMonkeyClient) Cancel(jid string) (response.Response, error) {
	return nil, m.cancelError
}

func (m *mockMonkeyClient) Status(jid string) (*monkey.StatusResponse, error) {
	return nil, m.cancelError
}

func (m *mockMonkeyClient) Update(jid string, body map[string]interface{}) (string, error) {
	return "", m.cancelError
}

func (m *mockMonkeyClient) List() ([]string, error) {
	return nil, m.cancelError
}

func (m *mockMonkeyClient) Close() {
}

func createTestService() *PatrickService {
	mockFactory := mock.New()
	mockDB, _ := mockFactory.New(nil, "/test", 0)

	return &PatrickService{
		db:           mockDB,
		node:         &mockNode{},
		monkeyClient: &mockMonkeyClient{},
	}
}

type selectiveErrorMockKVDB struct {
	mock.KVDB
	firstCallSucceeds bool
	callCount         int
}

func (m *selectiveErrorMockKVDB) List(ctx context.Context, prefix string) ([]string, error) {
	m.callCount++
	if m.firstCallSucceeds && m.callCount == 1 {
		return []string{"/jobs/job1"}, nil
	}
	return nil, errors.New("database list error")
}

func (m *selectiveErrorMockKVDB) Get(ctx context.Context, key string) ([]byte, error) {
	return []byte("test-data"), nil
}

func (m *selectiveErrorMockKVDB) Put(ctx context.Context, key string, value []byte) error {
	m.callCount++
	if m.firstCallSucceeds && m.callCount == 1 {
		return nil
	}
	return errors.New("database put error")
}

func createTestJobWithStatus(id string, status patrick.JobStatus) *patrick.Job {
	job := createTestJob(id)
	job.Status = status
	return job
}

func marshalJob(job *patrick.Job) []byte {
	data, _ := cbor.Marshal(job)
	return data
}

func runRequestServiceHandlerTests(t *testing.T, tests []testCase) {
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := createTestService()
			conn := &mockConnection{remotePeer: peer.ID("test-peer")}
			ctx := context.Background()

			if tt.setupMock != nil {
				tt.setupMock(service)
			}

			resp, err := service.requestServiceHandler(ctx, conn, tt.body)

			if tt.expectError {
				assert.Assert(t, err != nil, "Expected error but got nil")
				assert.Assert(t, resp == nil)
				if tt.errorContains != "" {
					assert.ErrorContains(t, err, tt.errorContains)
				}
			} else {
				assert.NilError(t, err)
				if tt.expectedResp != nil {
					for key, expected := range tt.expectedResp {
						assert.DeepEqual(t, expected, resp[key])
					}
				}
			}
		})
	}
}

func runHandlerTests(t *testing.T, tests []testCase, handler func(*PatrickService, context.Context) (interface{}, error)) {
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := createTestService()
			ctx := context.Background()

			if tt.setupMock != nil {
				tt.setupMock(service)
			}

			resp, err := handler(service, ctx)

			if tt.expectError {
				assert.Assert(t, err != nil, "Expected error but got nil")
				if tt.errorContains != "" {
					assert.ErrorContains(t, err, tt.errorContains)
				}
			} else {
				assert.NilError(t, err)
				if tt.expectedResp != nil {
					for key, expected := range tt.expectedResp {
						assert.Equal(t, expected, resp.(map[string]interface{})[key])
					}
				}
			}
		})
	}
}

func addJobToDB(jobID string, status patrick.JobStatus) func(*PatrickService) {
	return func(s *PatrickService) {
		job := createTestJobWithStatus(jobID, status)
		s.db.Put(context.Background(), "/jobs/"+jobID, marshalJob(job))
	}
}

func addJobToArchive(jobID string, status patrick.JobStatus) func(*PatrickService) {
	return func(s *PatrickService) {
		job := createTestJobWithStatus(jobID, status)
		s.db.Put(context.Background(), "/archive/jobs/"+jobID, marshalJob(job))
	}
}

func addInvalidJobData(jobID string) func(*PatrickService) {
	return func(s *PatrickService) {
		s.db.Put(context.Background(), "/jobs/"+jobID, []byte("invalid-cbor"))
	}
}

func addInvalidArchiveData(jobID string) func(*PatrickService) {
	return func(s *PatrickService) {
		s.db.Put(context.Background(), "/archive/jobs/"+jobID, []byte("invalid-cbor"))
	}
}

func closeDB() func(*PatrickService) {
	return func(s *PatrickService) {
		s.db.Close()
	}
}

func addLockData(jobID string) func(*PatrickService) {
	return func(s *PatrickService) {
		s.db.Put(context.Background(), "/locked/jobs/"+jobID, []byte("simple-lock-data"))
	}
}

func TestStatsServiceHandler(t *testing.T) {
	tests := []struct {
		name           string
		body           command.Body
		expectedError  bool
		expectedResult map[string]interface{}
	}{
		{
			name:          "valid db action",
			body:          command.Body{"action": "db"},
			expectedError: false,
			expectedResult: map[string]interface{}{
				"stats": []byte{},
			},
		},
		{
			name:          "invalid action",
			body:          command.Body{"action": "invalid"},
			expectedError: true,
		},
		{
			name:          "empty action",
			body:          command.Body{"action": ""},
			expectedError: true,
		},
		{
			name:          "missing action",
			body:          command.Body{},
			expectedError: true,
		},
		{
			name:          "nil action",
			body:          command.Body{"action": nil},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := createTestService()
			conn := &mockConnection{remotePeer: peer.ID("test-peer")}
			ctx := context.Background()

			resp, err := service.statsServiceHandler(ctx, conn, tt.body)

			if tt.expectedError {
				assert.Assert(t, err != nil, "Expected error but got nil")
				assert.Assert(t, resp == nil)
			} else {
				assert.NilError(t, err)
				assert.Assert(t, resp != nil)
				assert.DeepEqual(t, tt.expectedResult["stats"], resp["stats"])
			}
		})
	}
}

func TestRequestServiceHandler_List(t *testing.T) {
	service := createTestService()
	conn := &mockConnection{remotePeer: peer.ID("test-peer")}

	job1 := createTestJobWithStatus("job1", patrick.JobStatusOpen)
	job2 := createTestJobWithStatus("job2", patrick.JobStatusSuccess)

	service.db.Put(context.Background(), "/jobs/job1", marshalJob(job1))
	service.db.Put(context.Background(), "/archive/jobs/job2", marshalJob(job2))

	body := command.Body{"action": "list", "jid": "dummy"}
	ctx := context.Background()

	resp, err := service.requestServiceHandler(ctx, conn, body)

	assert.NilError(t, err)
	assert.Assert(t, resp != nil)

	ids, ok := resp["Ids"].([]string)
	assert.Assert(t, ok)
	assert.Assert(t, slices.Contains(ids, "job1"))
	assert.Assert(t, slices.Contains(ids, "job2"))
}

func TestListHandler_ErrorCases(t *testing.T) {
	tests := []testCase{
		{
			name: "successful list",
			setupMock: func(s *PatrickService) {
				s.db.Put(context.Background(), "/jobs/job1", []byte("data1"))
				s.db.Put(context.Background(), "/archive/jobs/job2", []byte("data2"))
			},
			expectError: false,
		},
		{
			name:        "empty list",
			expectError: false,
		},
		{
			name: "jobs with different path formats",
			setupMock: func(s *PatrickService) {
				s.db.Put(context.Background(), "/jobs/job1", []byte("data1"))
				s.db.Put(context.Background(), "/jobs/another/job2", []byte("data2"))
				s.db.Put(context.Background(), "/archive/jobs/job3", []byte("data3"))
			},
			expectError: false,
		},
		{
			name:          "database error on jobs list",
			setupMock:     closeDB(),
			expectError:   true,
			errorContains: "failed getting jobs with error",
		},
		{
			name: "database error on archive jobs list",
			setupMock: func(s *PatrickService) {
				s.db.Put(context.Background(), "/jobs/job1", []byte("data1"))
				s.db = &selectiveErrorMockKVDB{firstCallSucceeds: true}
			},
			expectError:   true,
			errorContains: "failed getting archive jobs with error",
		},
	}

	runHandlerTests(t, tests, func(s *PatrickService, ctx context.Context) (interface{}, error) {
		return s.listHandler(ctx)
	})
}

func TestRequestServiceHandler_Info(t *testing.T) {
	service := createTestService()
	conn := &mockConnection{remotePeer: peer.ID("test-peer")}

	job := createTestJobWithStatus("test-job", patrick.JobStatusOpen)
	service.db.Put(context.Background(), "/jobs/test-job", marshalJob(job))

	body := command.Body{"action": "info", "jid": "test-job"}
	ctx := context.Background()

	resp, err := service.requestServiceHandler(ctx, conn, body)

	assert.NilError(t, err)
	assert.Assert(t, resp != nil)

	jobResp, ok := resp["job"].(*patrick.Job)
	assert.Assert(t, ok)
	assert.Equal(t, "test-job", jobResp.Id)
	assert.Equal(t, patrick.JobStatusOpen, jobResp.Status)
}

func TestRequestServiceHandler_Info_NotFound(t *testing.T) {
	service := createTestService()
	conn := &mockConnection{remotePeer: peer.ID("test-peer")}

	body := command.Body{"action": "info", "jid": "nonexistent-job"}
	ctx := context.Background()

	resp, err := service.requestServiceHandler(ctx, conn, body)

	assert.Assert(t, err != nil, "Expected error but got nil")
	assert.Assert(t, resp == nil)
	assert.ErrorContains(t, err, "could not find")
}

func TestInfoHandler_ErrorCases(t *testing.T) {
	tests := []struct {
		name          string
		jid           string
		setupMock     func(*PatrickService)
		expectError   bool
		errorContains string
	}{
		{
			name:        "successful info from jobs",
			jid:         "test-job",
			setupMock:   addJobToDB("test-job", patrick.JobStatusOpen),
			expectError: false,
		},
		{
			name:        "successful info from archive",
			jid:         "test-job",
			setupMock:   addJobToArchive("test-job", patrick.JobStatusSuccess),
			expectError: false,
		},
		{
			name:          "not found in both locations",
			jid:           "nonexistent-job",
			expectError:   true,
			errorContains: "could not find",
		},
		{
			name:          "invalid CBOR data in jobs",
			jid:           "test-job",
			setupMock:     addInvalidJobData("test-job"),
			expectError:   true,
			errorContains: "unmarshal job",
		},
		{
			name:          "invalid CBOR data in archive",
			jid:           "test-job",
			setupMock:     addInvalidArchiveData("test-job"),
			expectError:   true,
			errorContains: "unmarshal job",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := createTestService()
			if tt.setupMock != nil {
				tt.setupMock(service)
			}

			ctx := context.Background()
			resp, err := service.infoHandler(ctx, tt.jid)

			if tt.expectError {
				assert.Assert(t, err != nil, "Expected error but got nil")
				assert.Assert(t, resp == nil)
				if tt.errorContains != "" {
					assert.ErrorContains(t, err, tt.errorContains)
				}
			} else {
				assert.NilError(t, err)
				assert.Assert(t, resp != nil)
				job, ok := resp["job"].(*patrick.Job)
				assert.Assert(t, ok)
				assert.Equal(t, tt.jid, job.Id)
			}
		})
	}
}

func TestRequestServiceHandler_IsLocked(t *testing.T) {
	service := createTestService()
	conn := &mockConnection{remotePeer: peer.ID("test-peer")}

	// Test when job is not locked
	body := command.Body{"action": "isLocked", "jid": "test-job"}
	ctx := context.Background()

	resp, err := service.requestServiceHandler(ctx, conn, body)

	assert.NilError(t, err)
	assert.Assert(t, resp != nil)
	assert.Assert(t, !resp["locked"].(bool))
}

func TestRequestServiceHandler_Cancel(t *testing.T) {
	service := createTestService()
	conn := &mockConnection{remotePeer: peer.ID("test-peer")}

	job := createTestJobWithStatus("test-job", patrick.JobStatusOpen)
	service.db.Put(context.Background(), "/jobs/test-job", marshalJob(job))

	body := command.Body{
		"action": "cancel",
		"jid":    "test-job",
		"cid":    map[interface{}]interface{}{"log1": "cid1"},
	}
	ctx := context.Background()

	resp, err := service.requestServiceHandler(ctx, conn, body)

	assert.NilError(t, err)
	assert.Assert(t, resp != nil)
	assert.Equal(t, "test-job", resp["cancelled"].(string))

	archivedJob, err := service.getJob(ctx, "/archive/jobs/", "test-job")
	assert.NilError(t, err)
	assert.Equal(t, patrick.JobStatusCancelled, archivedJob.Status)
}

func TestRequestServiceHandler_InvalidAction(t *testing.T) {
	service := createTestService()
	conn := &mockConnection{remotePeer: peer.ID("test-peer")}

	body := command.Body{"action": "invalid", "jid": "dummy"}
	ctx := context.Background()

	resp, err := service.requestServiceHandler(ctx, conn, body)

	assert.NilError(t, err)
	assert.Assert(t, resp == nil) // Invalid action returns nil, nil
}

func TestRequestServiceHandler_MissingJid(t *testing.T) {
	service := createTestService()
	conn := &mockConnection{remotePeer: peer.ID("test-peer")}

	body := command.Body{"action": "info"}
	ctx := context.Background()

	resp, err := service.requestServiceHandler(ctx, conn, body)

	assert.Assert(t, err != nil, "Expected error but got nil")
	assert.Assert(t, resp == nil)
	assert.ErrorContains(t, err, "failed getting jid")
}

func TestRequestServiceHandler_MissingEta(t *testing.T) {
	service := createTestService()
	conn := &mockConnection{remotePeer: peer.ID("test-peer")}

	body := command.Body{"action": "lock", "jid": "test-job"}
	ctx := context.Background()

	resp, err := service.requestServiceHandler(ctx, conn, body)

	assert.Assert(t, err != nil, "Expected error but got nil")
	assert.Assert(t, resp == nil)
	assert.ErrorContains(t, err, "failed getting eta")
}

func TestConvertToStringMap(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected map[string]string
		hasError bool
	}{
		{
			name: "valid map",
			input: map[interface{}]interface{}{
				"key1": "value1",
				"key2": "value2",
			},
			expected: map[string]string{
				"key1": "value1",
				"key2": "value2",
			},
			hasError: false,
		},
		{
			name:     "invalid type",
			input:    "not a map",
			expected: nil,
			hasError: true,
		},
		{
			name:     "nil input",
			input:    nil,
			expected: nil,
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := convertToStringMap(tt.input)

			if tt.hasError {
				assert.Assert(t, err != nil, "Expected error but got nil")
				assert.Assert(t, result == nil)
			} else {
				assert.NilError(t, err)
				assert.DeepEqual(t, tt.expected, result)
			}
		})
	}
}

func TestRequestServiceHandler_ConvertToStringMapErrors(t *testing.T) {
	tests := []testCase{
		{
			name: "invalid assetCid type",
			body: command.Body{
				"action":   "done",
				"jid":      "test-job",
				"assetCid": "invalid-type",
			},
			expectError:   true,
			errorContains: "failed converting map to map[string][string]",
		},
		{
			name: "invalid cid type",
			body: command.Body{
				"action": "done",
				"jid":    "test-job",
				"cid":    "invalid-type",
			},
			expectError:   true,
			errorContains: "failed converting map to map[string][string]",
		},
		{
			name: "valid assetCid and cid",
			body: command.Body{
				"action":   "done",
				"jid":      "test-job",
				"assetCid": map[interface{}]interface{}{"key1": "value1"},
				"cid":      map[interface{}]interface{}{"key2": "value2"},
			},
			setupMock:   addJobToDB("test-job", patrick.JobStatusOpen),
			expectError: false,
		},
		{
			name: "nil assetCid and cid",
			body: command.Body{
				"action": "done",
				"jid":    "test-job",
			},
			setupMock:   addJobToDB("test-job", patrick.JobStatusOpen),
			expectError: false,
		},
	}

	runRequestServiceHandlerTests(t, tests)
}

func TestRequestServiceHandler_UnknownAction(t *testing.T) {
	service := createTestService()
	conn := &mockConnection{remotePeer: peer.ID("test-peer")}

	body := command.Body{"action": "unknown-action", "jid": "test-job"}
	ctx := context.Background()

	resp, err := service.requestServiceHandler(ctx, conn, body)

	assert.NilError(t, err)
	assert.Assert(t, resp == nil)
}

func TestTryLock_ErrorCases(t *testing.T) {
	tests := []testCase{
		{
			name:        "successful lock",
			expectError: false,
		},
		{
			name:          "database put error",
			setupMock:     closeDB(),
			expectError:   true,
			errorContains: "locking",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := createTestService()
			if tt.setupMock != nil {
				tt.setupMock(service)
			}

			ctx := context.Background()
			pid := peer.ID("test-peer")
			jid := "test-job"
			timestamp := int64(1234567890)
			eta := int64(3600)

			resp, err := service.tryLock(ctx, pid, jid, timestamp, eta)

			if tt.expectError {
				assert.Assert(t, err != nil, "Expected error but got nil")
				assert.Assert(t, resp == nil)
				if tt.errorContains != "" {
					assert.ErrorContains(t, err, tt.errorContains)
				}
			} else {
				assert.NilError(t, err)
				assert.Assert(t, resp != nil)
			}
		})
	}
}

func TestLockHandler_Branches(t *testing.T) {
	tests := []testCase{
		{
			name:        "no existing lock - should call tryLock",
			expectError: false,
		},
		{
			name:          "existing lock - should call lockHelper",
			setupMock:     addLockData("test-job"),
			expectError:   true,
			errorContains: "error in lockHandler",
		},
		{
			name:          "database get error - should call tryLock",
			setupMock:     closeDB(),
			expectError:   true,
			errorContains: "locking",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := createTestService()
			if tt.setupMock != nil {
				tt.setupMock(service)
			}

			ctx := context.Background()
			conn := &mockConnection{remotePeer: peer.ID("test-peer")}
			jid := "test-job"
			eta := int64(3600)

			resp, err := service.lockHandler(ctx, jid, eta, conn)

			if tt.expectError {
				assert.Assert(t, err != nil, "Expected error but got nil")
				assert.Assert(t, resp == nil)
				if tt.errorContains != "" {
					assert.ErrorContains(t, err, tt.errorContains)
				}
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

func TestIsLockedHandler_Branches(t *testing.T) {
	tests := []testCase{
		{
			name:           "no lock data - should return locked false",
			expectError:    false,
			expectedLocked: false,
		},
		{
			name:          "existing lock data - should call lockHelper",
			setupMock:     addLockData("test-job"),
			expectError:   true,
			errorContains: "unexpected EOF", // CBOR unmarshal will fail
		},
		{
			name:           "database get error - should return locked false",
			setupMock:      closeDB(),
			expectError:    false,
			expectedLocked: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := createTestService()
			if tt.setupMock != nil {
				tt.setupMock(service)
			}

			ctx := context.Background()
			conn := &mockConnection{remotePeer: peer.ID("test-peer")}
			jid := "test-job"

			resp, err := service.isLockedHandler(ctx, jid, conn)

			if tt.expectError {
				assert.Assert(t, err != nil, "Expected error but got nil")
				assert.Assert(t, resp != nil)
			} else {
				assert.NilError(t, err)
				assert.Assert(t, resp != nil)
				locked, ok := resp["locked"].(bool)
				assert.Assert(t, ok)
				assert.Equal(t, tt.expectedLocked, locked)
			}
		})
	}
}

func TestUpdateStatus_ErrorCases(t *testing.T) {
	tests := []testCase{
		{
			name:        "successful update - no lock check",
			setupMock:   addJobToDB("test-job", patrick.JobStatusOpen),
			pid:         "", // Empty pid skips lock check
			jid:         "test-job",
			status:      patrick.JobStatusSuccess,
			expectError: false,
		},
		{
			name:        "successful update - with lock check",
			setupMock:   addJobToDB("test-job", patrick.JobStatusOpen),
			pid:         peer.ID("test-peer"),
			jid:         "test-job",
			status:      patrick.JobStatusSuccess,
			expectError: false,
		},
		{
			name:          "job not found error",
			pid:           peer.ID("test-peer"),
			jid:           "nonexistent-job",
			status:        patrick.JobStatusSuccess,
			expectError:   true,
			errorContains: "failed getting job in updateStatus",
		},
		{
			name:          "invalid job CBOR data",
			setupMock:     addInvalidJobData("test-job"),
			pid:           peer.ID("test-peer"),
			jid:           "test-job",
			status:        patrick.JobStatusSuccess,
			expectError:   true,
			errorContains: "failed unmarshalling job",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := createTestService()
			if tt.setupMock != nil {
				tt.setupMock(service)
			}

			ctx := context.Background()
			cidLog := map[string]string{"log1": "cid1"}
			assetCid := map[string]string{"asset1": "cid1"}

			err := service.updateStatus(ctx, tt.pid, tt.jid, cidLog, tt.status, assetCid)

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

func TestUnlockHandler_SimpleCases(t *testing.T) {
	tests := []testCase{
		{
			name:         "successful unlock with invalid CBOR data",
			setupMock:    addLockData("test-job"),
			expectError:  false,
			expectedResp: map[string]interface{}{"unlocked": "test-job"},
		},
		{
			name:        "job not found",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := createTestService()
			if tt.setupMock != nil {
				tt.setupMock(service)
			}

			ctx := context.Background()
			resp, err := service.unlockHandler(ctx, "test-job")

			if tt.expectError {
				assert.Assert(t, err != nil, "Expected error but got nil")
				assert.Assert(t, resp == nil)
			} else {
				assert.NilError(t, err)
				assert.Assert(t, resp != nil)
				if tt.expectedResp != nil {
					for key, expected := range tt.expectedResp {
						assert.DeepEqual(t, expected, resp[key])
					}
				}
			}
		})
	}
}

func TestTimeoutHandler_SimpleCases(t *testing.T) {
	tests := []testCase{
		{
			name:        "successful retry - job not at max attempts",
			setupMock:   addJobToDB("test-job", patrick.JobStatusOpen),
			expectError: false,
		},
		{
			name:        "job not found",
			expectError: true,
		},
		{
			name:        "job already finished",
			setupMock:   addJobToArchive("test-job", patrick.JobStatusSuccess),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := createTestService()
			if tt.setupMock != nil {
				tt.setupMock(service)
			}

			ctx := context.Background()
			cidLog := map[string]string{"log1": "cid1"}

			err := service.timeoutHandler(ctx, "test-job", cidLog)

			if tt.expectError {
				assert.Assert(t, err != nil, "Expected error but got nil")
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

func TestTimeoutHandler_EdgeCases(t *testing.T) {
	tests := []testCase{
		{
			name: "job at max attempts - should fail",
			setupMock: func(s *PatrickService) {
				job := createTestJobWithStatus("test-job", patrick.JobStatusOpen)
				job.Attempt = 2 // MaxJobAttempts = 2
				s.db.Put(context.Background(), "/jobs/test-job", marshalJob(job))
			},
			expectError: false,
		},
		{
			name: "database error during job retrieval",
			setupMock: func(s *PatrickService) {
				s.db.Close()
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

			ctx := context.Background()
			cidLog := map[string]string{"log1": "cid1"}

			err := service.timeoutHandler(ctx, "test-job", cidLog)

			if tt.expectError {
				assert.Assert(t, err != nil, "Expected error but got nil")
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

func TestUnlockHandler_EdgeCases(t *testing.T) {
	tests := []testCase{
		{
			name: "database error during get",
			setupMock: func(s *PatrickService) {
				s.db.Close()
			},
			expectError: true,
		},
		{
			name: "database error during put",
			setupMock: func(s *PatrickService) {
				s.db = &selectiveErrorMockKVDB{firstCallSucceeds: true}
				s.db.Put(context.Background(), "/locked/jobs/test-job", []byte("simple-lock-data"))
			},
			expectError: true,
		},
		{
			name: "invalid CBOR data - unmarshal error",
			setupMock: func(s *PatrickService) {
				s.db.Put(context.Background(), "/locked/jobs/test-job", []byte("invalid-cbor-data"))
			},
			expectError:  false,
			expectedResp: map[string]interface{}{"unlocked": "test-job"},
		},
		{
			name: "valid lock data - successful unlock",
			setupMock: func(s *PatrickService) {
				lock := struct {
					Pid       string `cbor:"4,keyasint"`
					Timestamp int64  `cbor:"8,keyasint"`
					Eta       int64  `cbor:"16,keyasint"`
				}{
					Pid:       "test-peer",
					Timestamp: time.Now().Unix(),
					Eta:       30,
				}
				lockData, _ := cbor.Marshal(lock)
				s.db.Put(context.Background(), "/locked/jobs/test-job", lockData)
			},
			expectError:  false,
			expectedResp: map[string]interface{}{"unlocked": "test-job"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := createTestService()
			if tt.setupMock != nil {
				tt.setupMock(service)
			}

			ctx := context.Background()
			resp, err := service.unlockHandler(ctx, "test-job")

			if tt.expectError {
				assert.Assert(t, err != nil, "Expected error but got nil")
				assert.Assert(t, resp == nil)
			} else {
				assert.NilError(t, err)
				assert.Assert(t, resp != nil)
			}
		})
	}
}
