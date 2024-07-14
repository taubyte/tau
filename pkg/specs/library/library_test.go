package librarySpec

import (
	"testing"

	"github.com/taubyte/tau/pkg/specs/common"
)

var (
	projectId = "123456"
	appId     = "someApp1234"
	libId     = "someLib123456"
	commit    = "someCommit"
	branch    = "Master"
)

func TestLibraryBasicKey(t *testing.T) {
	key, err := Tns().BasicPath(branch, commit, projectId, appId, libId)
	if err != nil {
		t.Error(err)
		return
	}

	expectedKey := common.BranchPathVariable.String() + "/" + branch + "/" + common.CommitPathVariable.String() + "/" + commit + "/" + common.ProjectPathVariable.String() + "/" + projectId + "/" + common.ApplicationPathVariable.String() + "/" + appId + "/" + PathVariable.String() + "/" + libId
	if key.String() != expectedKey {
		t.Errorf("Got `%s` key expected `%s`", key, expectedKey)
		return
	}

	key, err = Tns().BasicPath(branch, commit, projectId, "", libId)
	if err != nil {
		t.Error(err)
		return
	}

	expectedKey = common.BranchPathVariable.String() + "/" + branch + "/" + common.CommitPathVariable.String() + "/" + commit + "/" + common.ProjectPathVariable.String() + "/" + projectId + "/" + PathVariable.String() + "/" + libId
	if key.String() != expectedKey {
		t.Errorf("Got `%s` key expected `%s`", key, expectedKey)
		return
	}
}
