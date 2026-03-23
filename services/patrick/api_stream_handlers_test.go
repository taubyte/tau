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
	"github.com/taubyte/tau/pkg/raft"
	"gotest.tools/v3/assert"
)

type testCase struct {
	name          string
	setupMock     func(*PatrickService)
	body          command.Body
	expectError   bool
	errorContains string
	expectedResp  map[string]interface{}
	pid           peer.ID
	jid           string
	status        patrick.JobStatus
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

type pushCall struct {
	id   string
	data []byte
}

// mockJobQueue implements raft.Queue for unit tests with call recording.
type mockJobQueue struct {
	popID     string
	popData   []byte
	popErr    error
	pushErr   error
	pushCalls []pushCall
	popCalls  int
}

func (m *mockJobQueue) Push(id string, data []byte, _ time.Duration) error {
	m.pushCalls = append(m.pushCalls, pushCall{id: id, data: data})
	return m.pushErr
}
func (m *mockJobQueue) Pop(_ time.Duration) (string, []byte, error) {
	m.popCalls++
	return m.popID, m.popData, m.popErr
}
func (m *mockJobQueue) Peek() (string, []byte, bool) { return "", nil, false }
func (m *mockJobQueue) Len() int                     { return 0 }
func (m *mockJobQueue) Close() error                 { return nil }

var _ raft.Queue = (*mockJobQueue)(nil)

func createTestService() *PatrickService {
	mockFactory := mock.New()
	mockDB, _ := mockFactory.New(nil, "/test", 0)

	return &PatrickService{
		db:           mockDB,
		node:         &mockNode{},
		monkeyClient: &mockMonkeyClient{},
		jobQueue:     &mockJobQueue{popErr: raft.ErrQueueEmpty},
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

func addAssignmentData(jobID string, monkeyPID string) func(*PatrickService) {
	return func(s *PatrickService) {
		assignment := Assignment{
			MonkeyPID: monkeyPID,
			Timestamp: time.Now().Unix(),
		}
		data, _ := cbor.Marshal(assignment)
		s.db.Put(context.Background(), "/assigned/"+jobID, data)
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

func TestDequeueHandler(t *testing.T) {
	t.Run("empty queue returns available false", func(t *testing.T) {
		service := createTestService()
		conn := &mockConnection{remotePeer: peer.ID("monkey-1")}

		resp, err := service.dequeueHandler(context.Background(), conn)
		assert.NilError(t, err)
		assert.Assert(t, resp != nil)
		assert.Equal(t, false, resp["available"].(bool))
	})

	t.Run("successful dequeue assigns job", func(t *testing.T) {
		service := createTestService()
		job := createTestJobWithStatus("job-1", patrick.JobStatusOpen)
		service.db.Put(context.Background(), "/jobs/job-1", marshalJob(job))

		monkeyPeer := peer.ID("monkey-1")
		service.jobQueue = &mockJobQueue{popID: "job-1", popData: nil, popErr: nil}
		conn := &mockConnection{remotePeer: monkeyPeer}

		resp, err := service.dequeueHandler(context.Background(), conn)
		assert.NilError(t, err)
		assert.Assert(t, resp != nil)
		assert.Equal(t, true, resp["available"].(bool))

		assignData, err := service.db.Get(context.Background(), "/assigned/job-1")
		assert.NilError(t, err)
		var assignment Assignment
		assert.NilError(t, cbor.Unmarshal(assignData, &assignment))
		assert.Equal(t, monkeyPeer.String(), assignment.MonkeyPID)
	})
}

func TestIsAssignedHandler(t *testing.T) {
	t.Run("not assigned returns false", func(t *testing.T) {
		service := createTestService()
		conn := &mockConnection{remotePeer: peer.ID("monkey-1")}

		resp, err := service.isAssignedHandler(context.Background(), "job-1", conn)
		assert.NilError(t, err)
		assert.Equal(t, false, resp["assigned"].(bool))
	})

	t.Run("assigned to caller returns true", func(t *testing.T) {
		service := createTestService()
		monkeyPeer := peer.ID("monkey-1")
		addAssignmentData("job-1", monkeyPeer.String())(service)
		conn := &mockConnection{remotePeer: monkeyPeer}

		resp, err := service.isAssignedHandler(context.Background(), "job-1", conn)
		assert.NilError(t, err)
		assert.Equal(t, true, resp["assigned"].(bool))
	})

	t.Run("assigned to different monkey returns false", func(t *testing.T) {
		service := createTestService()
		addAssignmentData("job-1", peer.ID("monkey-2").String())(service)
		conn := &mockConnection{remotePeer: peer.ID("monkey-1")}

		resp, err := service.isAssignedHandler(context.Background(), "job-1", conn)
		assert.NilError(t, err)
		assert.Equal(t, false, resp["assigned"].(bool))
	})
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
	assert.Assert(t, resp == nil)
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

func TestUpdateStatus_ErrorCases(t *testing.T) {
	tests := []testCase{
		{
			name:        "successful update - no assignment check",
			setupMock:   addJobToDB("test-job", patrick.JobStatusOpen),
			pid:         "",
			jid:         "test-job",
			status:      patrick.JobStatusSuccess,
			expectError: false,
		},
		{
			name:        "successful update - with assignment check",
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
				job.Attempt = 2
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

// #16: Test poll-based job assignment flow — dequeue, assign, verify via isAssigned.
func TestDequeueAndAssignmentFlow(t *testing.T) {
	service := createTestService()
	job := createTestJobWithStatus("flow-job", patrick.JobStatusOpen)
	service.db.Put(context.Background(), "/jobs/flow-job", marshalJob(job))

	mq := &mockJobQueue{popID: "flow-job"}
	service.jobQueue = mq

	monkey1 := peer.ID("monkey-1")
	monkey2 := peer.ID("monkey-2")
	conn1 := &mockConnection{remotePeer: monkey1}
	conn2 := &mockConnection{remotePeer: monkey2}

	resp, err := service.dequeueHandler(context.Background(), conn1)
	assert.NilError(t, err)
	assert.Equal(t, true, resp["available"].(bool))
	assert.Assert(t, resp["job"] != nil)
	assert.Equal(t, 1, mq.popCalls)

	assignData, err := service.db.Get(context.Background(), "/assigned/flow-job")
	assert.NilError(t, err)
	var assignment Assignment
	assert.NilError(t, cbor.Unmarshal(assignData, &assignment))
	assert.Equal(t, monkey1.String(), assignment.MonkeyPID)

	resp, err = service.isAssignedHandler(context.Background(), "flow-job", conn1)
	assert.NilError(t, err)
	assert.Equal(t, true, resp["assigned"].(bool))

	resp, err = service.isAssignedHandler(context.Background(), "flow-job", conn2)
	assert.NilError(t, err)
	assert.Equal(t, false, resp["assigned"].(bool))
}

// #17: Test timeout → re-push → reassignment cycle.
func TestTimeoutRepushAndReassignment(t *testing.T) {
	service := createTestService()
	mq := &mockJobQueue{}
	service.jobQueue = mq

	job := createTestJobWithStatus("timeout-job", patrick.JobStatusOpen)
	service.db.Put(context.Background(), "/jobs/timeout-job", marshalJob(job))
	addAssignmentData("timeout-job", peer.ID("monkey-old").String())(service)

	ctx := context.Background()
	cidLog := map[string]string{"log1": "cid1"}
	err := service.timeoutHandler(ctx, "timeout-job", cidLog)
	assert.NilError(t, err)

	assert.Equal(t, 1, len(mq.pushCalls))
	assert.Equal(t, "timeout-job", mq.pushCalls[0].id)

	_, err = service.db.Get(ctx, "/assigned/timeout-job")
	assert.Assert(t, err != nil, "assignment should be deleted after timeout")

	updatedJob, err := service.getJob(ctx, "/jobs/", "timeout-job")
	assert.NilError(t, err)
	assert.Equal(t, patrick.JobStatusOpen, updatedJob.Status)
	assert.Equal(t, 1, updatedJob.Attempt)

	mq.popID = "timeout-job"
	mq.popErr = nil
	monkey2 := peer.ID("monkey-new")
	conn2 := &mockConnection{remotePeer: monkey2}

	resp, err := service.dequeueHandler(ctx, conn2)
	assert.NilError(t, err)
	assert.Equal(t, true, resp["available"].(bool))

	assignData, err := service.db.Get(ctx, "/assigned/timeout-job")
	assert.NilError(t, err)
	var newAssignment Assignment
	assert.NilError(t, cbor.Unmarshal(assignData, &newAssignment))
	assert.Equal(t, monkey2.String(), newAssignment.MonkeyPID)
}

// #18: Test zombie Monkey protection — non-owner rejected, real owner accepted.
func TestZombieMonkeyProtection(t *testing.T) {
	service := createTestService()
	ctx := context.Background()

	job := createTestJobWithStatus("zombie-job", patrick.JobStatusOpen)
	service.db.Put(ctx, "/jobs/zombie-job", marshalJob(job))

	monkey1 := peer.ID("monkey-owner")
	monkey2 := peer.ID("monkey-zombie")
	addAssignmentData("zombie-job", monkey1.String())(service)

	cidLog := map[string]string{"log1": "cid1"}
	assetCid := map[string]string{"asset1": "cid1"}

	err := service.updateStatus(ctx, monkey2, "zombie-job", cidLog, patrick.JobStatusSuccess, assetCid)
	assert.Assert(t, err != nil)
	assert.ErrorContains(t, err, "is not the owner")

	_, err = service.getJob(ctx, "/jobs/", "zombie-job")
	assert.NilError(t, err, "job should still be in /jobs/ after zombie rejection")
	_, err = service.getJob(ctx, "/archive/jobs/", "zombie-job")
	assert.Assert(t, err != nil, "job should NOT be in archive after zombie rejection")

	err = service.updateStatus(ctx, monkey1, "zombie-job", cidLog, patrick.JobStatusSuccess, assetCid)
	assert.NilError(t, err)

	_, err = service.getJob(ctx, "/archive/jobs/", "zombie-job")
	assert.NilError(t, err, "job should be archived after real owner reports success")
}

// #21: Test timeoutHandler queue push — retry pushes, max-attempts does not, push error propagates.
func TestTimeoutHandler_QueuePush(t *testing.T) {
	t.Run("retry pushes job back to queue", func(t *testing.T) {
		service := createTestService()
		mq := &mockJobQueue{}
		service.jobQueue = mq

		addJobToDB("push-job", patrick.JobStatusOpen)(service)

		err := service.timeoutHandler(context.Background(), "push-job", nil)
		assert.NilError(t, err)
		assert.Equal(t, 1, len(mq.pushCalls))
		assert.Equal(t, "push-job", mq.pushCalls[0].id)
	})

	t.Run("max attempts archives without push", func(t *testing.T) {
		service := createTestService()
		mq := &mockJobQueue{}
		service.jobQueue = mq

		job := createTestJobWithStatus("max-job", patrick.JobStatusOpen)
		job.Attempt = 2
		service.db.Put(context.Background(), "/jobs/max-job", marshalJob(job))

		err := service.timeoutHandler(context.Background(), "max-job", nil)
		assert.NilError(t, err)
		assert.Equal(t, 0, len(mq.pushCalls))

		_, err = service.getJob(context.Background(), "/archive/jobs/", "max-job")
		assert.NilError(t, err, "max-attempt job should be archived")
	})

	t.Run("queue push error propagates", func(t *testing.T) {
		service := createTestService()
		mq := &mockJobQueue{pushErr: errors.New("raft not leader")}
		service.jobQueue = mq

		addJobToDB("err-job", patrick.JobStatusOpen)(service)

		err := service.timeoutHandler(context.Background(), "err-job", nil)
		assert.Assert(t, err != nil)
		assert.ErrorContains(t, err, "failed to push")
	})
}
