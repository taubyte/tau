package messagingSpec

import (
	"testing"

	"github.com/taubyte/tau/pkg/specs/common"
	multihash "github.com/taubyte/utils/multihash"
)

var (
	projectId = "123456"
	appId     = "someApp1234"
	msgId     = "someMsg123456"
	commit    = "someCommit"
	branch    = "master"
)

func TestMessagingEmptyKey(t *testing.T) {
	key, err := Tns().EmptyPath(branch, commit, projectId, appId)
	if err != nil {
		t.Error(err)
		return
	}

	expectedKey := common.BranchPathVariable.String() + "/" + branch + "/" + common.CommitPathVariable.String() + "/" + commit + "/" + common.ProjectPathVariable.String() + "/" + projectId + "/" + common.ApplicationPathVariable.String() + "/" + appId + "/" + PathVariable.String()
	if key.String() != expectedKey {
		t.Errorf("Got `%s` key expected `%s`", key, expectedKey)
		return
	}

	key, err = Tns().EmptyPath(branch, commit, projectId, "")
	if err != nil {
		t.Error(err)
		return
	}

	expectedKey = common.BranchPathVariable.String() + "/" + branch + "/" + common.CommitPathVariable.String() + "/" + commit + "/" + common.ProjectPathVariable.String() + "/" + projectId + "/" + PathVariable.String()
	if key.String() != expectedKey {
		t.Errorf("Got `%s` key expected `%s`", key, expectedKey)
		return
	}
}

func TestMessagingBasicKey(t *testing.T) {
	key, err := Tns().BasicPath(branch, commit, projectId, appId, msgId)
	if err != nil {
		t.Error(err)
		return
	}

	expectedKey := common.BranchPathVariable.String() + "/" + branch + "/" + common.CommitPathVariable.String() + "/" + commit + "/" + common.ProjectPathVariable.String() + "/" + projectId + "/" + common.ApplicationPathVariable.String() + "/" + appId + "/" + PathVariable.String() + "/" + msgId
	if key.String() != expectedKey {
		t.Errorf("Got `%s` key expected `%s`", key, expectedKey)
		return
	}

	key, err = Tns().BasicPath(branch, commit, projectId, "", msgId)
	if err != nil {
		t.Error(err)
		return
	}

	expectedKey = common.BranchPathVariable.String() + "/" + branch + "/" + common.CommitPathVariable.String() + "/" + commit + "/" + common.ProjectPathVariable.String() + "/" + projectId + "/" + PathVariable.String() + "/" + msgId
	if key.String() != expectedKey {
		t.Errorf("Got `%s` key expected `%s`", key, expectedKey)
		return
	}
}

func TestWebSocketPath(t *testing.T) {
	key, err := Tns().WebSocketHashPath(projectId, appId)
	if err != nil {
		t.Error(err)
		return
	}

	hash := multihash.Hash(projectId + appId)
	expectedKey := "p2p/" + "pubsub/" + hash
	if key.String() != expectedKey {
		t.Errorf("Got `%s` key expected `%s`", key, expectedKey)
		return
	}
}
