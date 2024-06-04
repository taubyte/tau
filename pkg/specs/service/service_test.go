package serviceSpec

import (
	"testing"

	"github.com/taubyte/tau/pkg/specs/common"
)

var (
	projectId = "123456"
	appId     = "someApp1234"
	name      = "somedatabase"
	branch    = "master"
	commit    = "qwertyuiop"
)

func TestDatabaseBasicKey(t *testing.T) {
	key, err := Tns().IndexValue(branch, projectId, appId, name)
	if err != nil {
		t.Error(err)
		return
	}

	expectedKey := "branches/" + branch + "/" + common.ProjectPathVariable.String() + "/" + projectId + "/" + common.ApplicationPathVariable.String() + "/" + appId + "/" + PathVariable.String() + "/" + name
	if key.String() != expectedKey {
		t.Errorf("Got `%s` key expected `%s`", key, expectedKey)
		return
	}

	key, err = Tns().EmptyPath(branch, commit, projectId, "")
	if err != nil {
		t.Error(err)
		return
	}

	expectedKey = "branches/" + branch + "/" + "commit/" + commit + "/" + common.ProjectPathVariable.String() + "/" + projectId + "/" + PathVariable.String()

	if key.String() != expectedKey {
		t.Errorf("Got `%s` key expected `%s`", key, expectedKey)
		return
	}
}
