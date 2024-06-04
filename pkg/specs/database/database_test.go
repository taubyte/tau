package databaseSpec

import (
	"testing"

	"github.com/taubyte/tau/pkg/specs/common"
)

var (
	projectId = "123456"
	appId     = "someApp1234"
	name      = "someDatabase"
)

func TestDatabaseBasicKey(t *testing.T) {
	key := Tns().IndexPath(projectId, appId, name)
	expectedKey := common.ProjectPathVariable.String() + "/" + projectId + "/" + common.ApplicationPathVariable.String() + "/" + appId + "/" + name

	if key.String() != expectedKey {
		t.Errorf("Got `%s` key expected `%s`", key, expectedKey)
		return
	}

	key = Tns().IndexPath(projectId, "", name)
	expectedKey = common.ProjectPathVariable.String() + "/" + projectId + "/" + name

	if key.String() != expectedKey {
		t.Errorf("Got `%s` key expected `%s`", key, expectedKey)
		return
	}

}
