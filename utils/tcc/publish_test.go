package tccUtils

import (
	"errors"
	"testing"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/core/kvdb"
	tnsIface "github.com/taubyte/tau/core/services/tns"
	"gotest.tools/v3/assert"
)

// mockTNSClient is a mock implementation of tnsIface.Client for testing
type mockTNSClient struct {
	tnsIface.Client
	pushCalls   []pushCall
	pushErr     error
	failOnCall  int // Which call to fail on (1-indexed)
	currentCall int
}

type pushCall struct {
	path  []string
	value interface{}
}

func (m *mockTNSClient) Push(path []string, value interface{}) error {
	m.currentCall++
	if m.failOnCall > 0 && m.currentCall == m.failOnCall {
		return m.pushErr
	}
	m.pushCalls = append(m.pushCalls, pushCall{path: path, value: value})
	return nil
}

func (m *mockTNSClient) Fetch(path tnsIface.Path) (tnsIface.Object, error) {
	return nil, errors.New("not implemented")
}

func (m *mockTNSClient) Lookup(query tnsIface.Query) (interface{}, error) {
	return nil, errors.New("not implemented")
}

func (m *mockTNSClient) List(depth int) ([][]string, error) {
	return nil, errors.New("not implemented")
}

func (m *mockTNSClient) Stats() tnsIface.Stats {
	return &mockStats{}
}

type mockStats struct{}

func (m *mockStats) Database() (kvdb.Stats, error) {
	return nil, errors.New("not implemented")
}

func (m *mockTNSClient) Peers(...peerCore.ID) tnsIface.Client {
	return m
}

func TestPublish_Success(t *testing.T) {
	mockTNS := &mockTNSClient{}
	object := map[string]interface{}{
		"id":   "QmTestProject123",
		"name": "test-project",
	}
	indexes := map[string]interface{}{
		"domains": map[string]interface{}{},
	}

	err := Publish(mockTNS, object, indexes, "QmTestProject123", "main", "abc123")
	assert.NilError(t, err)

	// Verify Push was called 3 times (indexes, object, current commit)
	assert.Equal(t, len(mockTNS.pushCalls), 3)

	// First call should be indexes (empty path)
	assert.Equal(t, len(mockTNS.pushCalls[0].path), 0)
	// Check indexes values
	indexesMap, ok := mockTNS.pushCalls[0].value.(map[string]interface{})
	assert.Assert(t, ok)
	assert.Assert(t, indexesMap["domains"] != nil)

	// Second call should be object (with project prefix path)
	assert.Assert(t, len(mockTNS.pushCalls[1].path) > 0)
	// Check object values
	objectMap, ok := mockTNS.pushCalls[1].value.(map[string]interface{})
	assert.Assert(t, ok)
	assert.Equal(t, objectMap["id"], "QmTestProject123")
	assert.Equal(t, objectMap["name"], "test-project")

	// Third call should be current commit
	assert.Assert(t, len(mockTNS.pushCalls[2].path) > 0)
	commitMap, ok := mockTNS.pushCalls[2].value.(map[string]string)
	assert.Assert(t, ok)
	// Check that commit value is in the map (key is "current" not "current_commit")
	commitValue, exists := commitMap["current"]
	assert.Assert(t, exists, "current key should exist")
	assert.Equal(t, commitValue, "abc123")
}

func TestPublish_NilObject(t *testing.T) {
	mockTNS := &mockTNSClient{}
	indexes := map[string]interface{}{}

	err := Publish(mockTNS, nil, indexes, "QmTestProject123", "main", "abc123")
	assert.ErrorContains(t, err, "object and indexes must not be nil")
	assert.Equal(t, len(mockTNS.pushCalls), 0)
}

func TestPublish_NilIndexes(t *testing.T) {
	mockTNS := &mockTNSClient{}
	object := map[string]interface{}{
		"id": "QmTestProject123",
	}

	err := Publish(mockTNS, object, nil, "QmTestProject123", "main", "abc123")
	assert.ErrorContains(t, err, "object and indexes must not be nil")
	assert.Equal(t, len(mockTNS.pushCalls), 0)
}

func TestPublish_IndexesPushError(t *testing.T) {
	mockTNS := &mockTNSClient{
		pushErr:    errors.New("indexes push failed"),
		failOnCall: 1, // Fail on first call
	}
	object := map[string]interface{}{
		"id": "QmTestProject123",
	}
	indexes := map[string]interface{}{}

	err := Publish(mockTNS, object, indexes, "QmTestProject123", "main", "abc123")
	assert.ErrorContains(t, err, "publish index failed")
}

func TestPublish_ObjectPushError(t *testing.T) {
	mockTNS := &mockTNSClient{
		pushErr:    errors.New("object push failed"),
		failOnCall: 2, // Fail on second call
	}
	object := map[string]interface{}{
		"id": "QmTestProject123",
	}
	indexes := map[string]interface{}{}

	err := Publish(mockTNS, object, indexes, "QmTestProject123", "main", "abc123")
	assert.ErrorContains(t, err, "publish project failed")
}

func TestPublish_CurrentCommitPushError(t *testing.T) {
	mockTNS := &mockTNSClient{
		pushErr:    errors.New("current commit push failed"),
		failOnCall: 3, // Fail on third call
	}
	object := map[string]interface{}{
		"id": "QmTestProject123",
	}
	indexes := map[string]interface{}{}

	err := Publish(mockTNS, object, indexes, "QmTestProject123", "main", "abc123")
	assert.ErrorContains(t, err, "publishing current commit")
}
