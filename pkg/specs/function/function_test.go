package functionSpec

import (
	"testing"

	"github.com/taubyte/tau/pkg/specs/common"
)

var (
	projectId      = "123456"
	appId          = "someApp1234"
	funcId         = "someFunc123456"
	commit         = "someCommit"
	resourceName   = "someFunc"
	domainName     = "taubyte"
	topLevelDomain = "com"
	rootDomain     = domainName + "." + topLevelDomain
	branch         = "master"
)

func TestFunctionBasicKey(t *testing.T) {
	key, err := Tns().BasicPath(branch, commit, projectId, appId, funcId)
	if err != nil {
		t.Error(err)
		return
	}

	expectedKey := common.BranchPathVariable.String() + "/" + branch + "/" + common.CommitPathVariable.String() + "/" + commit + "/" + common.ProjectPathVariable.String() + "/" + projectId + "/" + common.ApplicationPathVariable.String() + "/" + appId + "/" + PathVariable.String() + "/" + funcId
	if key.String() != expectedKey {
		t.Errorf("Got `%s` key expected `%s`", key, expectedKey)
		return
	}

	key, err = Tns().BasicPath(branch, commit, projectId, "", funcId)
	if err != nil {
		t.Error(err)
		return
	}

	expectedKey = common.BranchPathVariable.String() + "/" + branch + "/" + common.CommitPathVariable.String() + "/" + commit + "/" + common.ProjectPathVariable.String() + "/" + projectId + "/" + PathVariable.String() + "/" + funcId
	if key.String() != expectedKey {
		t.Errorf("Got `%s` key expected `%s`", key, expectedKey)
		return
	}
}

func TestFunctionHttp(t *testing.T) {
	key, err := Tns().HttpPath(rootDomain)
	if err != nil {
		t.Error(err)
		return
	}

	expectedKey := "http/" + PathVariable.String() + "/" + topLevelDomain + "/" + domainName
	if key.String() != expectedKey {
		t.Errorf("Got `%s` key expected `%s`", key, expectedKey)
		return
	}
}

func TestFunctionWasm(t *testing.T) {
	tnsPath, err := Tns().WasmModulePath(projectId, appId, resourceName)
	if err != nil {
		t.Error(err)
		return
	}

	expectedStringPath := "wasm/" + "project" + "/" + projectId + "/application/" + appId + "/modules/" + string(PathVariable) + "/" + resourceName
	if tnsPath.String() != expectedStringPath {
		t.Errorf("Got `%s` key expected `%s`", tnsPath.String(), expectedStringPath)
		return
	}

	expectedSlicePath := []string{"wasm", "project", projectId, "application", appId, "modules", string(PathVariable), resourceName}
	for idx, val := range expectedSlicePath {
		if tnsPath.Slice()[idx] != val {
			t.Errorf("Got `%s` expected `%s`", tnsPath.Slice()[idx], val)
			return
		}
	}
}
