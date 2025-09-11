package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/core/services/auth"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/core/services/tns"
	p2p "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/kvdb/mock"
	"gotest.tools/v3/assert"
)

// Mock node that embeds the interface like in websocket tests
type mockNode struct {
	p2p.Node
	pubsubError error
	pubsubCalls [][]byte
}

func (m *mockNode) PubSubPublish(ctx context.Context, topic string, data []byte) error {
	if m.pubsubCalls == nil {
		m.pubsubCalls = make([][]byte, 0)
	}
	m.pubsubCalls = append(m.pubsubCalls, data)
	return m.pubsubError
}

// Mock auth client
type mockAuthClient struct {
	auth.Client
	repos map[int]mockRepo
	hooks mockHooks
}

type mockRepo struct {
	auth.GithubRepository
	projectID string
}

func (r mockRepo) Project() string {
	return r.projectID
}

func (r mockRepo) PrivateKey() string {
	return "mock-key"
}

func (r mockRepo) Id() int {
	return 12345
}

func (m *mockAuthClient) Repositories() auth.Repositories {
	return mockRepositories{repos: m.repos}
}

func (m *mockAuthClient) Hooks() auth.Hooks {
	return m.hooks
}

type mockHooks struct {
	auth.Hooks
	hooks map[string]mockAuthHook
}

func (m mockHooks) Get(hookid string) (auth.Hook, error) {
	if hook, exists := m.hooks[hookid]; exists {
		return &hook, nil
	}
	return nil, errors.New("hook not found")
}

type mockAuthHook struct {
	auth.Hook
	secret string
}

func (m *mockAuthHook) Github() (*auth.GithubHook, error) {
	return &auth.GithubHook{Secret: m.secret}, nil
}

func (m *mockAuthHook) Bitbucket() (*auth.BitbucketHook, error) {
	return nil, errors.New("not implemented")
}

type mockRepositories struct {
	auth.Repositories
	repos map[int]mockRepo
}

func (m mockRepositories) Github() auth.GithubRepositories {
	return mockGithubRepos{repos: m.repos}
}

type mockGithubRepos struct {
	auth.GithubRepositories
	repos map[int]mockRepo
}

func (m mockGithubRepos) Get(id int) (auth.GithubRepository, error) {
	repo, exists := m.repos[id]
	if !exists {
		return nil, errors.New("repo not found")
	}
	return repo, nil
}

func (m mockGithubRepos) New(obj map[string]interface{}) (auth.GithubRepository, error) {
	return nil, errors.New("not implemented")
}

func (m mockGithubRepos) List() ([]string, error) {
	return nil, errors.New("not implemented")
}

func (m mockGithubRepos) Register(repoID string) (string, error) {
	return "", errors.New("not implemented")
}

// Mock TNS client
type mockTNSClient struct {
	tns.Client
	lookupResponse interface{}
	lookupError    error
	pushError      error
}

func (m *mockTNSClient) Lookup(query tns.Query) (interface{}, error) {
	return m.lookupResponse, m.lookupError
}

func (m *mockTNSClient) Push(path []string, data interface{}) error {
	return m.pushError
}

// Helper functions
func createTestJob(id string) *patrick.Job {
	return &patrick.Job{
		Id:        id,
		Status:    patrick.JobStatusOpen,
		Timestamp: time.Now().Unix(),
		Logs:      make(map[string]string),
		AssetCid:  make(map[string]string),
		Attempt:   0,
		Meta: patrick.Meta{
			Repository: patrick.Repository{
				ID:       12345,
				Provider: "github",
				SSHURL:   "git@github.com:test/repo.git",
			},
		},
	}
}

func createTestLock(pid peer.ID, eta int64) *Lock {
	return &Lock{
		Pid:       pid,
		Timestamp: time.Now().Unix(),
		Eta:       eta,
	}
}

func TestReannounceJobs(t *testing.T) {
	tests := []struct {
		name          string
		setupMocks    func(*mock.KVDB)
		expectedError string
	}{
		{
			name: "successful reannounce with expired jobs",
			setupMocks: func(mockDB *mock.KVDB) {
				job1 := createTestJob("job1")
				job1Bytes, _ := cbor.Marshal(job1)
				mockDB.Put(context.Background(), "/jobs/job1", job1Bytes)

				job2 := createTestJob("job2")
				job2Bytes, _ := cbor.Marshal(job2)
				mockDB.Put(context.Background(), "/jobs/job2", job2Bytes)

				// Expired lock for job1
				expiredLock := createTestLock(peer.ID("peer1"), 300)
				expiredLock.Timestamp = time.Now().Unix() - 400
				lockData, _ := cbor.Marshal(expiredLock)
				mockDB.Put(context.Background(), "/locked/jobs/job1", lockData)

				// Valid lock for job2
				validLock := createTestLock(peer.ID("peer2"), 300)
				validLock.Timestamp = time.Now().Unix() - 50
				lockData2, _ := cbor.Marshal(validLock)
				mockDB.Put(context.Background(), "/locked/jobs/job2", lockData2)
			},
			expectedError: "",
		},
		{
			name: "no jobs to reannounce",
			setupMocks: func(mockDB *mock.KVDB) {
				// No jobs set up
			},
			expectedError: "",
		},
		{
			name: "database list error",
			setupMocks: func(mockDB *mock.KVDB) {
				mockDB.Close()
			},
			expectedError: "failed grabbing all jobs with error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factory := mock.New()
			mockDB, _ := factory.New(nil, "test", 0)

			srv := &PatrickService{
				db: mockDB,
			}

			tt.setupMocks(mockDB.(*mock.KVDB))

			err := srv.ReannounceJobs(context.Background())

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

func TestRepublishJob(t *testing.T) {
	tests := []struct {
		name          string
		jid           string
		setupMocks    func(*mock.KVDB, *mockNode)
		expectedError string
		expectPubSub  bool
	}{
		{
			name: "successful republish",
			jid:  "test-job-1",
			setupMocks: func(mockDB *mock.KVDB, mockNode *mockNode) {
				job := createTestJob("test-job-1")
				jobBytes, _ := cbor.Marshal(job)
				mockDB.Put(context.Background(), "/jobs/test-job-1", jobBytes)
			},
			expectedError: "",
			expectPubSub:  true,
		},
		{
			name: "job already archived",
			jid:  "test-job-2",
			setupMocks: func(mockDB *mock.KVDB, mockNode *mockNode) {
				job := createTestJob("test-job-2")
				jobBytes, _ := cbor.Marshal(job)
				mockDB.Put(context.Background(), "/archive/jobs/test-job-2", jobBytes)
			},
			expectedError: "",
			expectPubSub:  false,
		},
		{
			name: "job not found",
			jid:  "test-job-3",
			setupMocks: func(mockDB *mock.KVDB, mockNode *mockNode) {
				// No job data set up
			},
			expectedError: "could not find job test-job-3",
			expectPubSub:  false,
		},
		{
			name: "pubsub error",
			jid:  "test-job-4",
			setupMocks: func(mockDB *mock.KVDB, mockNode *mockNode) {
				job := createTestJob("test-job-4")
				jobBytes, _ := cbor.Marshal(job)
				mockDB.Put(context.Background(), "/jobs/test-job-4", jobBytes)
				mockNode.pubsubError = errors.New("pubsub failed")
			},
			expectedError: "failed to send over in republishJob pubsub error",
			expectPubSub:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factory := mock.New()
			mockDB, _ := factory.New(nil, "test", 0)
			mockNode := &mockNode{}

			srv := &PatrickService{
				db:   mockDB,
				node: mockNode,
			}

			tt.setupMocks(mockDB.(*mock.KVDB), mockNode)

			err := srv.republishJob(context.Background(), tt.jid)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
			} else {
				assert.NilError(t, err)
			}

			if tt.expectPubSub {
				assert.Assert(t, len(mockNode.pubsubCalls) > 0, "Expected pubsub call")
			} else {
				assert.Assert(t, len(mockNode.pubsubCalls) == 0, "Expected no pubsub call")
			}
		})
	}
}

func TestConnectToProject(t *testing.T) {
	tests := []struct {
		name          string
		job           *patrick.Job
		setupMocks    func(*mock.KVDB, *mockAuthClient, *mockTNSClient)
		expectedError string
	}{
		{
			name: "successful connection with auth client",
			job:  createTestJob("test-job-1"),
			setupMocks: func(mockDB *mock.KVDB, mockAuth *mockAuthClient, mockTNS *mockTNSClient) {
				mockAuth.repos[12345] = mockRepo{projectID: "project-123"}
			},
			expectedError: "",
		},
		{
			name: "successful connection with TNS client",
			job:  createTestJob("test-job-2"),
			setupMocks: func(mockDB *mock.KVDB, mockAuth *mockAuthClient, mockTNS *mockTNSClient) {
				mockTNS.lookupResponse = []string{"repositories/github/12345/extra/project-456"}
			},
			expectedError: "",
		},
		{
			name: "TNS lookup error",
			job:  createTestJob("test-job-3"),
			setupMocks: func(mockDB *mock.KVDB, mockAuth *mockAuthClient, mockTNS *mockTNSClient) {
				mockTNS.lookupError = errors.New("TNS lookup failed")
			},
			expectedError: "TNS lookup failed",
		},
		{
			name: "database put error",
			job:  createTestJob("test-job-4"),
			setupMocks: func(mockDB *mock.KVDB, mockAuth *mockAuthClient, mockTNS *mockTNSClient) {
				mockAuth.repos[12345] = mockRepo{projectID: "project-123"}
				mockDB.Close()
			},
			expectedError: "failed putting job into project with error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factory := mock.New()
			mockDB, _ := factory.New(nil, "test", 0)
			mockAuth := &mockAuthClient{repos: make(map[int]mockRepo)}
			mockTNS := &mockTNSClient{}

			srv := &PatrickService{
				db:         mockDB,
				authClient: mockAuth,
				tnsClient:  mockTNS,
			}

			tt.setupMocks(mockDB.(*mock.KVDB), mockAuth, mockTNS)

			err := srv.connectToProject(context.Background(), tt.job)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

func TestGetProjectIDFromJob(t *testing.T) {
	tests := []struct {
		name          string
		job           *patrick.Job
		setupMocks    func(*mockAuthClient, *mockTNSClient)
		expectedID    string
		expectedError string
	}{
		{
			name: "successful auth client lookup",
			job:  createTestJob("test-job-1"),
			setupMocks: func(mockAuth *mockAuthClient, mockTNS *mockTNSClient) {
				mockAuth.repos[12345] = mockRepo{projectID: "project-123"}
			},
			expectedID:    "project-123",
			expectedError: "",
		},
		{
			name: "successful TNS lookup",
			job:  createTestJob("test-job-2"),
			setupMocks: func(mockAuth *mockAuthClient, mockTNS *mockTNSClient) {
				mockTNS.lookupResponse = []string{"repositories/github/12345/extra/project-456"}
			},
			expectedID:    "project-456",
			expectedError: "",
		},
		{
			name: "TNS lookup error",
			job:  createTestJob("test-job-3"),
			setupMocks: func(mockAuth *mockAuthClient, mockTNS *mockTNSClient) {
				mockTNS.lookupError = errors.New("TNS lookup failed")
			},
			expectedID:    "",
			expectedError: "TNS lookup failed",
		},
		{
			name: "TNS invalid response",
			job:  createTestJob("test-job-4"),
			setupMocks: func(mockAuth *mockAuthClient, mockTNS *mockTNSClient) {
				mockTNS.lookupResponse = "invalid response"
			},
			expectedID:    "",
			expectedError: "response from lookup not an array or is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAuth := &mockAuthClient{repos: make(map[int]mockRepo)}
			mockTNS := &mockTNSClient{}

			srv := &PatrickService{
				authClient: mockAuth,
				tnsClient:  mockTNS,
			}

			tt.setupMocks(mockAuth, mockTNS)

			projectID, err := srv.getProjectIDFromJob(tt.job)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
			} else {
				assert.NilError(t, err)
			}

			assert.Equal(t, tt.expectedID, projectID)
		})
	}
}
