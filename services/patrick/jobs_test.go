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

type mockAuthClient struct {
	auth.Client
	repos map[string]mockRepo
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

func (r mockRepo) Id() string {
	return testJobRepoKey
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
	repos map[string]mockRepo
}

func (m mockRepositories) Github() auth.GithubRepositories {
	return mockGithubRepos{repos: m.repos}
}

type mockGithubRepos struct {
	auth.GithubRepositories
	repos map[string]mockRepo
}

func (m mockGithubRepos) Get(id string) (auth.GithubRepository, error) {
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

// testJobRepoKey is what createTestJob's clone URL derives to. It is written
// out rather than computed so the test pins the key the service will build.
const testJobRepoKey = "github.com/test/repo"

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

func createTestAssignment(pid peer.ID) *Assignment {
	return &Assignment{
		MonkeyPID: pid.String(),
		Timestamp: time.Now().Unix(),
	}
}

func TestReannounceJobs(t *testing.T) {
	tests := []struct {
		name          string
		setupMocks    func(*mock.KVDB)
		expectedError string
	}{
		{
			name: "successful reannounce with expired assignments",
			setupMocks: func(mockDB *mock.KVDB) {
				job1 := createTestJob("job1")
				job1Bytes, _ := cbor.Marshal(job1)
				mockDB.Put(context.Background(), "/jobs/job1", job1Bytes)

				job2 := createTestJob("job2")
				job2Bytes, _ := cbor.Marshal(job2)
				mockDB.Put(context.Background(), "/jobs/job2", job2Bytes)

				// Expired assignment for job1
				expiredAssignment := createTestAssignment(peer.ID("peer1"))
				expiredAssignment.Timestamp = time.Now().Unix() - 120
				assignData, _ := cbor.Marshal(expiredAssignment)
				mockDB.Put(context.Background(), "/assigned/job1", assignData)

				// Recent assignment for job2
				recentAssignment := createTestAssignment(peer.ID("peer2"))
				recentAssignment.Timestamp = time.Now().Unix()
				assignData2, _ := cbor.Marshal(recentAssignment)
				mockDB.Put(context.Background(), "/assigned/job2", assignData2)
			},
			expectedError: "",
		},
		{
			name: "no jobs to reannounce",
			setupMocks: func(mockDB *mock.KVDB) {
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
				db:       mockDB,
				node:     &mockNode{},
				jobQueue: &mockJobQueue{popErr: nil},
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
		expectedError string
	}{
		{
			name:          "successful republish",
			jid:           "test-job-1",
			expectedError: "",
		},
		{
			name:          "republish with no job in DB",
			jid:           "test-job-2",
			expectedError: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factory := mock.New()
			mockDB, _ := factory.New(nil, "test", 0)

			srv := &PatrickService{
				db:       mockDB,
				node:     &mockNode{},
				jobQueue: &mockJobQueue{popErr: nil},
			}

			err := srv.republishJob(context.Background(), tt.jid)

			if tt.expectedError != "" {
				assert.ErrorContains(t, err, tt.expectedError)
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

// #20: Verify clusterPidPayload CBOR uses string keys (not integer keys).
func TestClusterPidPayload_CBORRoundTrip(t *testing.T) {
	original := clusterPidPayload{
		Pid:       "QmTestPeer123",
		Timestamp: 1234567890,
	}

	data, err := cbor.Marshal(original)
	assert.NilError(t, err)

	var decoded clusterPidPayload
	assert.NilError(t, cbor.Unmarshal(data, &decoded))
	assert.Equal(t, original.Pid, decoded.Pid)
	assert.Equal(t, original.Timestamp, decoded.Timestamp)

	var generic map[string]interface{}
	assert.NilError(t, cbor.Unmarshal(data, &generic))
	_, hasPid := generic["pid"]
	assert.Assert(t, hasPid, "CBOR should use string key 'pid', not integer key")
	_, hasTimestamp := generic["timestamp"]
	assert.Assert(t, hasTimestamp, "CBOR should use string key 'timestamp', not integer key")
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
				mockAuth.repos[testJobRepoKey] = mockRepo{projectID: "project-123"}
			},
			expectedError: "",
		},
		{
			// The TNS index is keyed by the provider's number and auth is keyed
			// by the clone URL. A TNS answer to a question auth could not answer
			// is an answer in a different grammar, so there is no longer a
			// fallback: an unregistered repository is refused by name.
			name: "repository not registered with auth is refused, not guessed",
			job:  createTestJob("test-job-2"),
			setupMocks: func(mockDB *mock.KVDB, mockAuth *mockAuthClient, mockTNS *mockTNSClient) {
				mockTNS.lookupResponse = []string{"repositories/github/12345/extra/project-456"}
			},
			expectedError: "repository `github.com/test/repo` is not registered with auth",
		},
		{
			name: "repository registered but linked to no project",
			job:  createTestJob("test-job-3"),
			setupMocks: func(mockDB *mock.KVDB, mockAuth *mockAuthClient, mockTNS *mockTNSClient) {
				mockAuth.repos[testJobRepoKey] = mockRepo{projectID: ""}
			},
			expectedError: "linked to no project",
		},
		{
			name: "database put error",
			job:  createTestJob("test-job-4"),
			setupMocks: func(mockDB *mock.KVDB, mockAuth *mockAuthClient, mockTNS *mockTNSClient) {
				mockAuth.repos[testJobRepoKey] = mockRepo{projectID: "project-123"}
				mockDB.Close()
			},
			expectedError: "failed putting job into project with error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factory := mock.New()
			mockDB, _ := factory.New(nil, "test", 0)
			mockAuth := &mockAuthClient{repos: make(map[string]mockRepo)}
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
				mockAuth.repos[testJobRepoKey] = mockRepo{projectID: "project-123"}
			},
			expectedID:    "project-123",
			expectedError: "",
		},
		{
			// The old TNS fallback would have answered "project-456" here, from
			// an index keyed by the provider's number and read by segment
			// position. Both of those are wrong under a clone-URL key, and a
			// wrong project id builds one project's code into another's.
			name: "a TNS answer no longer substitutes for an auth miss",
			job:  createTestJob("test-job-2"),
			setupMocks: func(mockAuth *mockAuthClient, mockTNS *mockTNSClient) {
				mockTNS.lookupResponse = []string{"repositories/github/12345/extra/project-456"}
			},
			expectedID:    "",
			expectedError: "repository `github.com/test/repo` is not registered with auth",
		},
		{
			name: "registered with no project is not a project id",
			job:  createTestJob("test-job-3"),
			setupMocks: func(mockAuth *mockAuthClient, mockTNS *mockTNSClient) {
				mockAuth.repos[testJobRepoKey] = mockRepo{projectID: ""}
			},
			expectedID:    "",
			expectedError: "linked to no project",
		},
		{
			name: "a job whose repository is not a clone URL is refused",
			job: &patrick.Job{
				Id:   "test-job-4",
				Meta: patrick.Meta{Repository: patrick.Repository{ID: 12345, Provider: "github"}},
			},
			setupMocks:    func(mockAuth *mockAuthClient, mockTNS *mockTNSClient) {},
			expectedID:    "",
			expectedError: "names no repository",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockAuth := &mockAuthClient{repos: make(map[string]mockRepo)}
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
